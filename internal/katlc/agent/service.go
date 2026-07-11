package agent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/katl-dev/katl/internal/installer/operation"
	agentapi "github.com/katl-dev/katl/internal/katlc/agentapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var timeNow = func() time.Time { return time.Now().UTC() }

type ServeConfig struct {
	Root                           string
	Listen                         string
	AuthTokenFile                  string
	AllowUnauthenticatedForTesting bool
	Dispatcher                     Dispatcher
}

type dispatcherShutdown interface {
	Shutdown(context.Context) error
}

const dispatcherShutdownTimeout = 30 * time.Second

func Serve(ctx context.Context, config ServeConfig) error {
	root := strings.TrimSpace(config.Root)
	if root == "" {
		root = "/"
	}
	store, err := operation.NewStore(filepath.Join(root, "var/lib/katl/operations"))
	if err != nil {
		return err
	}
	listen := strings.TrimSpace(config.Listen)
	if listen == "" {
		listen = DefaultListen
	}
	network, address, err := parseListen(listen)
	if err != nil {
		return err
	}
	var opts []grpc.ServerOption
	if !config.AllowUnauthenticatedForTesting {
		token, err := readAuthToken(config.AuthTokenFile)
		if err != nil {
			return err
		}
		opts = append(opts, grpc.UnaryInterceptor(unaryTokenInterceptor(token)))
		opts = append(opts, grpc.StreamInterceptor(streamTokenInterceptor(token)))
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := grpc.NewServer(opts...)
	agentServer := NewServer(root, store)
	if _, err := AuditStartup(store, timeNow()); err != nil {
		return err
	}
	dispatcher := config.Dispatcher
	if dispatcher == nil {
		dispatcher = NewExecutor(root, store, agentServer.AgentStartID)
	}
	agentServer.Dispatcher = dispatcher
	agentapi.RegisterKatlcAgentServer(server, agentServer)
	errc := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil {
			errc <- err
		}
		close(errc)
	}()
	select {
	case <-ctx.Done():
		server.GracefulStop()
		return errors.Join(ctx.Err(), shutdownDispatcher(dispatcher))
	case err := <-errc:
		return errors.Join(err, shutdownDispatcher(dispatcher))
	}
}

func shutdownDispatcher(dispatcher Dispatcher) error {
	shutdown, ok := dispatcher.(dispatcherShutdown)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), dispatcherShutdownTimeout)
	defer cancel()
	return shutdown.Shutdown(ctx)
}

func InitToken(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("token path is required")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("token path must be absolute")
	}
	if data, err := os.ReadFile(path); err == nil {
		if strings.TrimSpace(string(data)) == "" {
			return fmt.Errorf("token file is empty: %s", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	token := append([]byte(base64.RawURLEncoding.EncodeToString(raw[:])), '\n')
	return writeSecretNoReplace(path, token)
}

func parseListen(value string) (string, string, error) {
	scheme, address, ok := strings.Cut(value, "://")
	if !ok {
		return "", "", fmt.Errorf("listen address must use scheme://address")
	}
	switch scheme {
	case "tcp":
		if strings.TrimSpace(address) == "" {
			return "", "", fmt.Errorf("tcp listen address is required")
		}
		return "tcp", address, nil
	default:
		return "", "", fmt.Errorf("unsupported listen scheme %q", scheme)
	}
}

func readAuthToken(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("--auth-token-file is required unless --allow-unauthenticated-for-testing is set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("auth token file is empty: %s", path)
	}
	return token, nil
}

func unaryTokenInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := checkAuth(ctx, token); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func streamTokenInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := checkAuth(stream.Context(), token); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

func checkAuth(ctx context.Context, token string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "authorization metadata is required")
	}
	var got string
	for _, value := range md.Get("authorization") {
		if strings.HasPrefix(value, "Bearer ") {
			got = strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
			break
		}
	}
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid bearer token")
	}
	return nil
}

func writeSecretNoReplace(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	_ = os.Remove(tmpPath)
	return nil
}
