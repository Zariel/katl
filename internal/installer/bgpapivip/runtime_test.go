package bgpapivip

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCommandBirdClientControlsOnlyAPIRoute(t *testing.T) {
	runner := &recordingCommandRunner{outputs: [][]byte{
		[]byte("BIRD ready.\n"),
		[]byte("BIRD ready.\n"),
	}}
	client := CommandBirdClient{Runner: runner}
	if err := client.SetAdvertisement(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := client.SetAdvertisement(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"birdc", "-s", BirdControlSocketPath, "disable", "katl_api"},
		{"birdc", "-s", BirdControlSocketPath, "enable", "katl_api"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestCommandBirdClientReportsBoundedPeerState(t *testing.T) {
	config := minimalConfig()
	config, err := Normalize(config)
	if err != nil {
		t.Fatal(err)
	}
	config.RouteExchange = []RouteExchange{{Name: "cilium"}}
	runner := &recordingCommandRunner{outputs: [][]byte{[]byte(`Name Proto Table State Since Info
katl_fabric_router_a BGP katl_fabric up 12:00:00 Established
	Local address: 10.0.0.11
	Routes: 0 imported, 1 exported, 0 preferred
katl_exchange_cilium BGP katl_exchange_cilium_table up 12:00:00 Established
	Routes: 3 imported, 0 exported, 3 preferred
katl_exchange_cilium_to_fabric Pipe katl_exchange_cilium_table up 12:00:00
	Routes: 0 imported, 3 exported, 3 preferred
`)}}
	client := CommandBirdClient{Runner: runner, Config: config}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.ControlSocketReady || len(status.Peers) != 3 || status.Peers[0].SessionState != "established" || status.Peers[0].ExportedRoutes != 1 || status.RouterID != "10.0.0.11" {
		t.Fatalf("status = %#v", status)
	}
	if status.Peers[1].AcceptedRoutes != 3 || status.Peers[2].ExportedRoutes != 3 {
		t.Fatalf("route exchange status = %#v", status.Peers[1:])
	}
	want := [][]string{{"birdc", "-s", BirdControlSocketPath, "show", "protocols", "all"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestCommandBirdClientFailsClosedWhenControlSocketIsUnavailable(t *testing.T) {
	runner := &recordingCommandRunner{err: errors.New("exit status 1")}
	client := CommandBirdClient{Runner: runner}
	status, err := client.Status(context.Background())
	if err == nil || status.ControlSocketReady || status.ProcessActive {
		t.Fatalf("status = %#v, error = %v", status, err)
	}
}

type recordingCommandRunner struct {
	outputs  [][]byte
	err      error
	commands [][]string
}

func (r *recordingCommandRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, append([]string{name}, args...))
	var output []byte
	if len(r.outputs) > 0 {
		output = r.outputs[0]
		r.outputs = r.outputs[1:]
	}
	return output, r.err
}
