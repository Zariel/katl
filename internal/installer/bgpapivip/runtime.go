package bgpapivip

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

const BirdControlSocketPath = "/run/katl-bird/bird.ctl"

type CommandRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type CommandBirdClient struct {
	Runner CommandRunner
	Birdc  string
	Socket string
	Config Config
}

func (c CommandBirdClient) Status(ctx context.Context) (BirdRuntimeStatus, error) {
	output, err := c.run(ctx, "show", "protocols", "all")
	status := BirdRuntimeStatus{
		ProcessActive:      err == nil,
		ControlSocketReady: err == nil,
		ControlSocketPath:  c.socket(),
		ReadinessState:     "not-ready",
	}
	if err != nil {
		status.FailureReason = boundedCommandFailure(output, err)
		return status, fmt.Errorf("query endpoint routing status: %s", status.FailureReason)
	}
	status.ReadinessState = "ready"
	status.Peers = parseProtocolStatus(string(output), c.Config)
	for _, peer := range status.Peers {
		if status.RouterID == "" && peer.LocalAddress != "" {
			status.RouterID = peer.LocalAddress
		}
	}
	if c.Config.Routing.RouterID != "" {
		status.RouterID = c.Config.Routing.RouterID
	}
	return status, nil
}

func (c CommandBirdClient) SetAdvertisement(ctx context.Context, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	output, err := c.run(ctx, action, "katl_api")
	if err != nil {
		return fmt.Errorf("%s endpoint route: %s", action, boundedCommandFailure(output, err))
	}
	return nil
}

func (c CommandBirdClient) run(ctx context.Context, args ...string) ([]byte, error) {
	runner := c.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	birdc := strings.TrimSpace(c.Birdc)
	if birdc == "" {
		birdc = "birdc"
	}
	command := []string{"-s", c.socket()}
	command = append(command, args...)
	return runner.Output(ctx, birdc, command...)
}

func (c CommandBirdClient) socket() string {
	if strings.TrimSpace(c.Socket) != "" {
		return strings.TrimSpace(c.Socket)
	}
	return BirdControlSocketPath
}

type LinuxInterfaceChecker struct{}

func (LinuxInterfaceChecker) Ready(_ context.Context, config Config) (bool, error) {
	iface, err := net.InterfaceByName(config.VIPInterface.Name)
	if err != nil {
		return false, nil
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return false, fmt.Errorf("inspect endpoint interface: %w", err)
	}
	want := strings.SplitN(config.Endpoint.VIP, "/", 2)[0]
	for _, address := range addresses {
		if strings.SplitN(address.String(), "/", 2)[0] == want {
			return true, nil
		}
	}
	return false, nil
}

func parseProtocolStatus(output string, config Config) []PeerRuntimeStatus {
	known := map[string]PeerRuntimeStatus{}
	for _, peer := range config.FabricPeers {
		known[protocolName(peer)] = PeerRuntimeStatus{
			Name:          peer.Address,
			ASN:           peer.ASN,
			Kind:          "fabric",
			AddressFamily: config.Endpoint.AddressFamily,
			AdminState:    "unknown",
			SessionState:  "unknown",
		}
	}
	for _, exchange := range config.RouteExchange {
		known["katl_exchange_"+safeSymbol(exchange.Name)] = PeerRuntimeStatus{
			Name:          exchange.Name,
			Kind:          "route-exchange",
			AddressFamily: "ipv4",
			AdminState:    "unknown",
			SessionState:  "unknown",
		}
		known["katl_exchange_"+safeSymbol(exchange.Name)+"_to_fabric"] = PeerRuntimeStatus{
			Name:          exchange.Name,
			Kind:          "route-exchange-export",
			AddressFamily: "ipv4",
			AdminState:    "unknown",
			SessionState:  "unknown",
		}
	}
	current := ""
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		topLevel := len(line) > 0 && line[0] != ' ' && line[0] != '\t'
		if topLevel && len(fields) >= 5 {
			if peer, ok := known[fields[0]]; ok {
				peer.AdminState = strings.ToLower(fields[3])
				peer.SessionState = peer.AdminState
				if len(fields) > 5 {
					peer.SessionState = strings.ToLower(strings.Join(fields[5:], "-"))
				}
				known[fields[0]] = peer
				current = fields[0]
				continue
			}
			current = ""
			continue
		}
		peer, ok := known[current]
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(trimmed, "Local address: "); ok {
			if values := strings.Fields(value); len(values) > 0 {
				peer.LocalAddress = values[0]
			}
		}
		var accepted, exported uint64
		if _, err := fmt.Sscanf(trimmed, "Routes: %d imported, %d exported", &accepted, &exported); err == nil {
			peer.AcceptedRoutes = accepted
			peer.ExportedRoutes = exported
		}
		known[current] = peer
	}
	out := make([]PeerRuntimeStatus, 0, len(known))
	for _, peer := range config.FabricPeers {
		out = append(out, known[protocolName(peer)])
	}
	for _, exchange := range config.RouteExchange {
		out = append(out, known["katl_exchange_"+safeSymbol(exchange.Name)])
		out = append(out, known["katl_exchange_"+safeSymbol(exchange.Name)+"_to_fabric"])
	}
	return out
}

func boundedCommandFailure(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if len(message) > 1024 {
		message = message[:1024]
	}
	if message == "" {
		message = err.Error()
	}
	return message
}
