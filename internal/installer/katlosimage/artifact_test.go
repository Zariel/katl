package katlosimage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactMetadataVerifiesCompanionImage(t *testing.T) {
	directory := t.TempDir()
	image := filepath.Join(directory, "katlos-upgrade-2026.7.0-dev.12-x86_64.squashfs")
	content := []byte("KatlOS upgrade image")
	if err := os.WriteFile(image, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	metadata := ArtifactMetadata{
		APIVersion:        APIVersion,
		Kind:              ArtifactMetadataKind,
		ImageRole:         RoleUpgrade,
		Format:            FormatSquashFS,
		Version:           "2026.7.0-dev.12",
		BuildID:           "test-build",
		Architecture:      "x86_64",
		RuntimeInterface:  "katl-runtime-1",
		Path:              filepath.Base(image),
		SizeBytes:         int64(len(content)),
		SHA256:            hex.EncodeToString(digest[:]),
		ChecksumPath:      filepath.Base(image) + ".sha256",
		EmbeddedIndexPath: "katlos/image.json",
		CreatedAt:         "2026-07-23T12:00:00Z",
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadataPath := image + ".json"
	if err := os.WriteFile(metadataPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadArtifactMetadata(metadataPath, RoleUpgrade)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.VerifyFile(image); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(image, []byte("modified upgrade image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := got.VerifyFile(image); err == nil || (!strings.Contains(err.Error(), "size") && !strings.Contains(err.Error(), "SHA-256")) {
		t.Fatalf("VerifyFile() error = %v", err)
	}
}

func TestArtifactMetadataRejectsWrongRole(t *testing.T) {
	metadata := ArtifactMetadata{
		APIVersion:        APIVersion,
		Kind:              ArtifactMetadataKind,
		ImageRole:         RoleInstall,
		Format:            FormatSquashFS,
		Version:           "2026.7.0",
		BuildID:           "test-build",
		Architecture:      "x86_64",
		RuntimeInterface:  "katl-runtime-1",
		Path:              "katlos-install.squashfs",
		SizeBytes:         1,
		SHA256:            strings.Repeat("a", 64),
		ChecksumPath:      "katlos-install.squashfs.sha256",
		EmbeddedIndexPath: "katlos/image.json",
		CreatedAt:         "2026-07-23T12:00:00Z",
	}
	if err := metadata.Validate(RoleUpgrade); err == nil || !strings.Contains(err.Error(), "role must be upgrade") {
		t.Fatalf("Validate() error = %v", err)
	}
}
