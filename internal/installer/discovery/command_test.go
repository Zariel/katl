package discovery

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestCommandDiscoverySourceCollectsReadOnlyFacts(t *testing.T) {
	runner := &fixtureOutputRunner{
		outputs: map[string][]byte{
			"lsblk": []byte(`{
  "blockdevices": [
    {
      "name": "nvme0n1",
      "path": "/dev/nvme0n1",
      "type": "disk",
      "size": 68719476736,
      "ro": false,
      "model": "KATL_TEST_DISK",
      "serial": "root-serial",
      "wwn": "0x5000katlroot",
      "fstype": null,
      "pttype": "gpt",
      "parttype": null,
      "mountpoints": [],
      "children": [
        {
          "name": "nvme0n1p1",
          "path": "/dev/nvme0n1p1",
          "type": "part",
          "size": 1073741824,
          "ro": false,
          "model": null,
          "serial": null,
          "wwn": null,
          "fstype": "vfat",
          "pttype": null,
          "parttype": "esp",
          "mountpoints": ["/boot"]
        }
      ]
    }
  ]
}`),
			"findmnt": []byte(`{
  "filesystems": [
    {
      "source": "/dev/nvme0n1p1",
      "target": "/boot",
      "fstype": "vfat",
      "options": "rw,nosuid,nodev"
    }
  ]
}`),
			"ip": []byte(`[
  {
    "ifname": "lo",
    "address": "00:00:00:00:00:00",
    "link_type": "loopback",
    "operstate": "UNKNOWN"
  },
  {
    "ifname": "eno1",
    "address": "52:54:00:12:34:56",
    "link_type": "ether",
    "operstate": "UP"
  }
]`),
		},
	}

	facts, err := NewCommandDiscoverySource(runner).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(facts.BlockDevices) != 1 {
		t.Fatalf("block device count = %d, want 1", len(facts.BlockDevices))
	}
	disk := facts.BlockDevices[0]
	if disk.Path != "/dev/nvme0n1" || disk.PartitionSignature != "gpt" {
		t.Fatalf("disk = %#v, want /dev/nvme0n1 with gpt signature", disk)
	}
	if got := disk.Partitions[0].FilesystemSignature; got != "vfat" {
		t.Fatalf("partition filesystem signature = %q, want vfat", got)
	}

	wantNICs := []NICFact{
		{Name: "eno1", MACAddress: "52:54:00:12:34:56", OperState: "up"},
	}
	if !reflect.DeepEqual(facts.NICs, wantNICs) {
		t.Fatalf("NICs = %#v, want %#v", facts.NICs, wantNICs)
	}

	wantMounts := []MountFact{
		{Source: "/dev/nvme0n1p1", Target: "/boot", Filesystem: "vfat", Options: []string{"rw", "nosuid", "nodev"}},
	}
	if !reflect.DeepEqual(facts.Mounts, wantMounts) {
		t.Fatalf("mounts = %#v, want %#v", facts.Mounts, wantMounts)
	}

	wantCommands := []string{"lsblk", "findmnt", "ip"}
	if !reflect.DeepEqual(runner.calls, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.calls, wantCommands)
	}
}

type fixtureOutputRunner struct {
	outputs map[string][]byte
	calls   []string
}

func (r *fixtureOutputRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	switch name {
	case "lsblk", "findmnt", "ip":
	default:
		return nil, fmt.Errorf("unexpected command %q", name)
	}

	r.calls = append(r.calls, name)
	output, ok := r.outputs[name]
	if !ok {
		return nil, fmt.Errorf("missing fixture for %q", name)
	}
	return output, nil
}
