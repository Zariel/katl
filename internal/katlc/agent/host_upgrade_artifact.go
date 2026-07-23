package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	agentapi "github.com/katl-dev/katl/internal/katlc/agentapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	StageHostUpgradeArtifactRequestKind = "StageHostUpgradeArtifactRequest"
	maxHostUpgradeArtifactSize          = uint64(8 << 30)
	maxHostUpgradeArtifactChunkSize     = 2 << 20
	hostUpgradeUploadDirectory          = "host-upgrade/uploads"
)

func (s *Server) StageHostUpgradeArtifact(stream grpc.ClientStreamingServer[agentapi.StageHostUpgradeArtifactRequest, agentapi.HostUpgradeArtifactStaged]) error {
	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return status.Error(codes.InvalidArgument, "artifact metadata and content are required")
	}
	if err != nil {
		return err
	}
	if err := s.validateHostUpgradeArtifactMetadata(first); err != nil {
		return err
	}

	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	active, err := s.activeOperationIDs()
	if err != nil {
		return status.Errorf(codes.Internal, "read operation locks: %v", err)
	}
	if len(active) > 0 {
		return status.Errorf(codes.FailedPrecondition, "cannot stage a KatlOS upgrade while operation %s is active", strings.Join(active, ","))
	}

	directory := filepath.Join(runtimeRoot(s.Root), "var/lib/katl/artifacts", filepath.FromSlash(hostUpgradeUploadDirectory))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return status.Errorf(codes.Internal, "prepare KatlOS artifact staging directory: %v", err)
	}
	temporary, err := os.CreateTemp(directory, "."+first.Sha256+"-*.partial")
	if err != nil {
		return status.Errorf(codes.Internal, "create KatlOS artifact staging file: %v", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return status.Errorf(codes.Internal, "protect KatlOS artifact staging file: %v", err)
	}

	hash := sha256.New()
	var received uint64
	writeChunk := func(chunk []byte) error {
		if len(chunk) > maxHostUpgradeArtifactChunkSize {
			return status.Errorf(codes.ResourceExhausted, "artifact chunk exceeds %d bytes", maxHostUpgradeArtifactChunkSize)
		}
		if received > first.SizeBytes || uint64(len(chunk)) > first.SizeBytes-received {
			return status.Errorf(codes.InvalidArgument, "artifact content exceeds declared size %d", first.SizeBytes)
		}
		if len(chunk) == 0 {
			return nil
		}
		written, err := temporary.Write(chunk)
		if err != nil {
			return status.Errorf(codes.Internal, "write KatlOS artifact: %v", err)
		}
		if written != len(chunk) {
			return status.Error(codes.Internal, "write KatlOS artifact: short write")
		}
		_, _ = hash.Write(chunk)
		received += uint64(written)
		return nil
	}
	if err := writeChunk(first.Chunk); err != nil {
		return err
	}
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if request.ApiVersion != "" || request.Kind != "" || request.Actor != "" || request.ExpectedMachineId != "" || request.Sha256 != "" || request.SizeBytes != 0 {
			return status.Error(codes.InvalidArgument, "artifact metadata is only allowed in the first chunk")
		}
		if err := writeChunk(request.Chunk); err != nil {
			return err
		}
	}
	if received != first.SizeBytes {
		return status.Errorf(codes.InvalidArgument, "artifact content size %d does not match declared size %d", received, first.SizeBytes)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != first.Sha256 {
		return status.Errorf(codes.InvalidArgument, "artifact SHA-256 %s does not match declared identity", got)
	}
	if err := temporary.Sync(); err != nil {
		return status.Errorf(codes.Internal, "sync KatlOS artifact: %v", err)
	}
	if err := temporary.Close(); err != nil {
		return status.Errorf(codes.Internal, "close KatlOS artifact: %v", err)
	}
	filename := first.Sha256 + ".squashfs"
	finalPath := filepath.Join(directory, filename)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return status.Errorf(codes.Internal, "commit KatlOS artifact: %v", err)
	}
	committed = true
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	localRef := filepath.ToSlash(filepath.Join(hostUpgradeUploadDirectory, filename))
	return stream.SendAndClose(&agentapi.HostUpgradeArtifactStaged{
		LocalRef:  localRef,
		Sha256:    first.Sha256,
		SizeBytes: first.SizeBytes,
	})
}

func (s *Server) validateHostUpgradeArtifactMetadata(request *agentapi.StageHostUpgradeArtifactRequest) error {
	if request == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	if request.ApiVersion != APIVersion {
		return status.Errorf(codes.InvalidArgument, "apiVersion must be %q", APIVersion)
	}
	if request.Kind != StageHostUpgradeArtifactRequestKind {
		return status.Errorf(codes.InvalidArgument, "kind must be %q", StageHostUpgradeArtifactRequestKind)
	}
	if strings.TrimSpace(request.Actor) == "" {
		return status.Error(codes.InvalidArgument, "actor is required")
	}
	machineID, err := s.machineID()
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "read machine id: %v", err)
	}
	if strings.TrimSpace(request.ExpectedMachineId) == "" || request.ExpectedMachineId != machineID {
		return status.Error(codes.FailedPrecondition, "expectedMachineID does not match node machine id")
	}
	if err := validateArtifactSHA256(request.Sha256); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if request.SizeBytes == 0 {
		return status.Error(codes.InvalidArgument, "artifact sizeBytes must be positive")
	}
	if request.SizeBytes > maxHostUpgradeArtifactSize {
		return status.Errorf(codes.ResourceExhausted, "artifact sizeBytes exceeds %d", maxHostUpgradeArtifactSize)
	}
	return nil
}

func validateArtifactSHA256(value string) error {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return fmt.Errorf("artifact sha256 must be %d lowercase hexadecimal characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("artifact sha256 must be %d lowercase hexadecimal characters", sha256.Size*2)
	}
	return nil
}
