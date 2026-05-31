package generation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewFirstInstallRecordSerializesConfextSelection(t *testing.T) {
	record, err := NewFirstInstallRecord(FirstInstallRequest{
		GenerationID:          "2026.05.31-001",
		RuntimeVersion:        "0.1.0",
		RootSlot:              "root-a",
		RootPartitionUUID:     "11111111-2222-3333-4444-555555555555",
		RuntimeArtifactSHA256: strings.Repeat("a", 64),
		UKIPath:               "/efi/EFI/Linux/katl-2026.05.31-001.efi",
		Sysexts: []ExtensionRef{
			{
				Name:           "kubernetes",
				Path:           "/var/lib/katl/generations/2026.05.31-001/sysext/kubernetes.raw",
				ActivationPath: "/run/extensions/kubernetes.raw",
				SHA256:         strings.Repeat("b", 64),
			},
		},
		GeneratedConfext: GeneratedConfext{
			Name:           "katl-node",
			Path:           "/var/lib/katl/generations/2026.05.31-001/confext",
			ActivationPath: "/run/confexts/katl-node",
			SHA256:         strings.Repeat("c", 64),
			Compatibility: ConfextCompatibility{
				ID:           "katl",
				VersionID:    "0.1.0",
				ConfextLevel: 1,
			},
		},
		KernelCommandLine: []string{"console=ttyS0", "root=PARTUUID=${KATL_ROOT_A_PARTUUID}"},
		CreatedAt:         time.Date(2026, 5, 31, 22, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewFirstInstallRecord() error = %v", err)
	}

	data, err := MarshalRecord(record)
	if err != nil {
		t.Fatalf("MarshalRecord() error = %v", err)
	}
	want := `{
  "apiVersion": "katl.dev/v1alpha1",
  "kind": "GenerationRecord",
  "generationID": "2026.05.31-001",
  "runtimeVersion": "0.1.0",
  "root": {
    "slot": "root-a",
    "partitionUUID": "11111111-2222-3333-4444-555555555555",
    "runtimeArtifactSHA256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  },
  "boot": {
    "ukiPath": "/efi/EFI/Linux/katl-2026.05.31-001.efi"
  },
  "sysexts": [
    {
      "name": "kubernetes",
      "path": "/var/lib/katl/generations/2026.05.31-001/sysext/kubernetes.raw",
      "activationPath": "/run/extensions/kubernetes.raw",
      "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    }
  ],
  "confexts": [
    {
      "name": "katl-node",
      "path": "/var/lib/katl/generations/2026.05.31-001/confext",
      "activationPath": "/run/confexts/katl-node",
      "sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
      "compatibility": {
        "id": "katl",
        "versionID": "0.1.0",
        "confextLevel": 1
      }
    }
  ],
  "kernelCommandLine": [
    "console=ttyS0",
    "root=PARTUUID=${KATL_ROOT_A_PARTUUID}"
  ],
  "createdAt": "2026-05-31T22:30:00Z",
  "bootState": "pending",
  "healthState": "unknown"
}
`
	if string(data) != want {
		t.Fatalf("record json:\n%s\nwant:\n%s", data, want)
	}

	if len(record.Confexts) != 1 || record.Confexts[0].ActivationPath != "/run/confexts/katl-node" {
		t.Fatalf("confext selection = %#v", record.Confexts)
	}
	if record.Root.Slot != "root-a" || len(record.Sysexts) != 1 {
		t.Fatalf("rollback selection is not recorded as one generation: %#v", record)
	}
}

func TestWriteRecordPersistsMetadataJSON(t *testing.T) {
	record, err := NewFirstInstallRecord(validFirstInstallRequest(t.TempDir()))
	if err != nil {
		t.Fatalf("NewFirstInstallRecord() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "metadata.json")
	if err := WriteRecord(path, record); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var decoded Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if decoded.GenerationID != record.GenerationID || decoded.Confexts[0].SHA256 != record.Confexts[0].SHA256 {
		t.Fatalf("decoded record = %#v, want %#v", decoded, record)
	}
}

func TestDigestDirectoryIsDeterministic(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "etc", "b.conf"), "b\n", 0o600)
	mustWrite(t, filepath.Join(root, "etc", "a.conf"), "a\n", 0o644)

	digestA, err := DigestDirectory(root)
	if err != nil {
		t.Fatalf("DigestDirectory() error = %v", err)
	}
	digestB, err := DigestDirectory(root)
	if err != nil {
		t.Fatalf("DigestDirectory() second error = %v", err)
	}
	if digestA != digestB {
		t.Fatalf("digest changed between runs: %s != %s", digestA, digestB)
	}

	mustWrite(t, filepath.Join(root, "etc", "a.conf"), "changed\n", 0o644)
	digestC, err := DigestDirectory(root)
	if err != nil {
		t.Fatalf("DigestDirectory() changed error = %v", err)
	}
	if digestC == digestA {
		t.Fatalf("digest did not change after content update: %s", digestC)
	}
}

func TestFirstInstallRecordRequiresConfextMetadata(t *testing.T) {
	request := validFirstInstallRequest(t.TempDir())
	request.GeneratedConfext.Compatibility = ConfextCompatibility{}
	_, err := NewFirstInstallRecord(request)
	if err == nil {
		t.Fatal("NewFirstInstallRecord() error = nil, want compatibility failure")
	}
	if !strings.Contains(err.Error(), "compatibility metadata is required") {
		t.Fatalf("error = %q, want compatibility failure", err)
	}
}

func validFirstInstallRequest(root string) FirstInstallRequest {
	return FirstInstallRequest{
		GenerationID:          "2026.05.31-001",
		RuntimeVersion:        "0.1.0",
		RootSlot:              "root-a",
		RootPartitionUUID:     "11111111-2222-3333-4444-555555555555",
		RuntimeArtifactSHA256: strings.Repeat("a", 64),
		UKIPath:               "/efi/EFI/Linux/katl-2026.05.31-001.efi",
		GeneratedConfext: GeneratedConfext{
			Name:           "katl-node",
			Path:           filepath.Join(root, "generations", "2026.05.31-001", "confext"),
			ActivationPath: "/run/confexts/katl-node",
			SHA256:         strings.Repeat("c", 64),
			Compatibility: ConfextCompatibility{
				ID:           "katl",
				VersionID:    "0.1.0",
				ConfextLevel: 1,
			},
		},
		CreatedAt: time.Date(2026, 5, 31, 22, 30, 0, 0, time.UTC),
	}
}

func mustWrite(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}
