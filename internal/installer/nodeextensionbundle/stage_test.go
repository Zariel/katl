package nodeextensionbundle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchAndStage(t *testing.T) {
	f := writeStageFixture(t)
	server := stageFixtureServer(t, f.RootDir)
	cacheDir := t.TempDir()

	staged, err := FetchAndStage(context.Background(), Request{
		Source:           server.URL,
		Ref:              stageRef(t, f),
		CacheDir:         cacheDir,
		RuntimeInterface: "katl-runtime-1",
		Architecture:     "x86_64",
		Client:           server.Client(),
	})
	if err != nil {
		t.Fatalf("FetchAndStage() error = %v", err)
	}

	if staged.AppID != "generic-fixture" || staged.PayloadVersion != "generic-fixture-v0.1.0" || staged.Architecture != "x86_64" {
		t.Fatalf("staged identity = %#v", staged)
	}
	if staged.BundleManifestDigest != f.BundleManifestDigest || staged.SysextPayloadDigest != f.PayloadDigest {
		t.Fatalf("staged digests = %#v, fixture bundle %s payload %s", staged, f.BundleManifestDigest, f.PayloadDigest)
	}
	for _, path := range []string{
		staged.SysextPath,
		filepath.Join(staged.BundleDir, "bundle.json"),
		filepath.Join(staged.BundleDir, "package-provenance.json"),
		filepath.Join(staged.BundleDir, "catalog-entry.json"),
		filepath.Join(cacheDir, "index.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat staged path %s: %v", path, err)
		}
	}
	payload, err := os.ReadFile(staged.SysextPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "node extension payload" {
		t.Fatalf("payload = %q", payload)
	}

	ref := staged.ExtensionRef
	if ref.Name != "generic-fixture" || ref.Path != staged.SysextPath || ref.ActivationPath != "/run/extensions/katl-node-extension-generic-fixture.raw" {
		t.Fatalf("extension ref location = %#v", ref)
	}
	if ref.SHA256 != strings.TrimPrefix(f.PayloadDigest, "sha256:") || ref.PayloadVersion != "generic-fixture-v0.1.0" || ref.ArtifactVersion == "" {
		t.Fatalf("extension ref identity = %#v", ref)
	}
	if len(ref.Compatibility.RuntimeInterfaces) != 1 || ref.Compatibility.RuntimeInterfaces[0] != "katl-runtime-1" {
		t.Fatalf("extension ref compatibility = %#v", ref.Compatibility)
	}

	var index Index
	readJSON(t, filepath.Join(cacheDir, "index.json"), &index)
	if len(index.Entries) != 1 || index.Entries[0].BundleManifestDigest != staged.BundleManifestDigest || index.Entries[0].CatalogEntryPath == "" {
		t.Fatalf("local index = %#v", index)
	}
}

func TestFetchAndStageRejectsDescriptorDigestMismatch(t *testing.T) {
	f := writeStageFixture(t)
	corruptStageBlob(t, f.RootDir, f.PayloadDigest, []byte("changed payload"))
	server := stageFixtureServer(t, f.RootDir)

	_, err := FetchAndStage(context.Background(), stageRequest(t, f, server))
	if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "descriptor systemd-sysext digest got") {
		t.Fatalf("FetchAndStage() error = %v, want descriptor digest mismatch", err)
	}
}

func TestFetchAndStageRejectsIncompatibleRuntime(t *testing.T) {
	f := writeStageFixture(t)
	request := stageRequest(t, f, stageFixtureServer(t, f.RootDir))
	request.RuntimeInterface = "katl-runtime-2"

	_, err := FetchAndStage(context.Background(), request)
	if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "runtime interface") {
		t.Fatalf("FetchAndStage() error = %v, want runtime rejection", err)
	}
}

func TestFetchAndStageRejectsMissingPayload(t *testing.T) {
	f := writeStageFixture(t)
	if err := os.Remove(stageBlobPath(f.RootDir, f.PayloadDigest)); err != nil {
		t.Fatal(err)
	}
	server := stageFixtureServer(t, f.RootDir)

	_, err := FetchAndStage(context.Background(), stageRequest(t, f, server))
	if err == nil || !strings.Contains(err.Error(), "fetch descriptor systemd-sysext") || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("FetchAndStage() error = %v, want missing payload fetch error", err)
	}
}

func TestFetchAndStageRejectsMissingDescriptor(t *testing.T) {
	f := rewriteStageBundle(t, writeStageFixture(t), func(bundle *Bundle) {
		bundle.Metadata = bundle.Metadata[:1]
	})
	server := stageFixtureServer(t, f.RootDir)

	_, err := FetchAndStage(context.Background(), stageRequest(t, f, server))
	if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "missing catalog fragment descriptor") {
		t.Fatalf("FetchAndStage() error = %v, want missing descriptor", err)
	}
}

func TestFetchAndStageRejectsStaleRef(t *testing.T) {
	f := writeStageFixture(t)
	request := stageRequest(t, f, stageFixtureServer(t, f.RootDir))
	request.Ref = "generic-fixture/generic-fixture-v0.1.0@sha256:" + strings.Repeat("0", sha256.Size*2)

	_, err := FetchAndStage(context.Background(), request)
	if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "no index entry matches ref") {
		t.Fatalf("FetchAndStage() error = %v, want stale ref", err)
	}
}

func TestFetchAndStageRejectsRawSysextSource(t *testing.T) {
	_, err := FetchAndStage(context.Background(), Request{
		Source:           "https://artifacts.example.invalid/katl-node-extension-generic.SYSEXT.RAW?token=secret",
		Ref:              "generic-fixture/generic-fixture-v0.1.0@sha256:" + strings.Repeat("0", sha256.Size*2),
		CacheDir:         t.TempDir(),
		RuntimeInterface: "katl-runtime-1",
		Architecture:     "x86_64",
		Client:           http.DefaultClient,
	})
	if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "raw sysext URLs") {
		t.Fatalf("FetchAndStage() error = %v, want raw sysext rejection", err)
	}
}

func TestFetchAndStageRejectsUnsafePayloadFileName(t *testing.T) {
	f := rewriteStageBundle(t, writeStageFixture(t), func(bundle *Bundle) {
		for i := range bundle.Payloads {
			if bundle.Payloads[i].Role == sysextRole {
				bundle.Payloads[i].FileName = "../generic.raw"
			}
		}
	})
	server := stageFixtureServer(t, f.RootDir)

	_, err := FetchAndStage(context.Background(), stageRequest(t, f, server))
	if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "systemd-sysext fileName") {
		t.Fatalf("FetchAndStage() error = %v, want unsafe file name rejection", err)
	}
}

func TestFetchAndStageRejectsUnscopedConfigPath(t *testing.T) {
	f := rewriteStageBundle(t, writeStageFixture(t), func(bundle *Bundle) {
		bundle.Configuration.ConfigHandoffPaths = []string{"/etc/kubernetes/admin.conf"}
	})
	server := stageFixtureServer(t, f.RootDir)

	_, err := FetchAndStage(context.Background(), stageRequest(t, f, server))
	if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "outside Katl-owned app scope") {
		t.Fatalf("FetchAndStage() error = %v, want config scope rejection", err)
	}
}

func TestFetchAndStageRedactsSourceCredentials(t *testing.T) {
	f := writeStageFixture(t)
	server := stageFixtureServer(t, f.RootDir)
	source, err := url.Parse(server.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	source.User = url.UserPassword("user", "secret")
	request := stageRequest(t, f, server)
	request.Source = source.String()

	_, err = FetchAndStage(context.Background(), request)
	if err == nil {
		t.Fatal("FetchAndStage() error = nil, want missing index")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "user:") {
		t.Fatalf("FetchAndStage() leaked source credentials: %v", err)
	}
}

func writeStageFixture(t *testing.T) Fixture {
	t.Helper()
	f, err := WriteFixture(FixtureRequest{
		OutputDir:       t.TempDir(),
		AppID:           "generic-fixture",
		PayloadVersion:  "generic-fixture-v0.1.0",
		ArtifactVersion: "generic-fixture-v0.1.0-build.1",
		Payload:         []byte("node extension payload"),
		CreatedAt:       "2026-06-18T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("WriteFixture() error = %v", err)
	}
	return f
}

func stageFixtureServer(t *testing.T, root string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.FileServer(http.Dir(root)))
	t.Cleanup(server.Close)
	return server
}

func stageRef(t *testing.T, f Fixture) string {
	t.Helper()
	var index Index
	readJSON(t, f.IndexPath, &index)
	if len(index.Entries) != 1 {
		t.Fatalf("index entries = %d, want 1", len(index.Entries))
	}
	entry := index.Entries[0]
	return entry.AppID + "/" + entry.PayloadVersion + "@" + entry.BundleManifestDigest
}

func stageRequest(t *testing.T, f Fixture, server *httptest.Server) Request {
	t.Helper()
	return Request{
		Source:           server.URL,
		Ref:              stageRef(t, f),
		CacheDir:         t.TempDir(),
		RuntimeInterface: "katl-runtime-1",
		Architecture:     "x86_64",
		Client:           server.Client(),
	}
}

func rewriteStageBundle(t *testing.T, f Fixture, mutate func(*Bundle)) Fixture {
	t.Helper()
	var bundle Bundle
	readJSON(t, f.BundlePath, &bundle)
	mutate(&bundle)
	bundleBytes := mustMarshalStageJSON(t, bundle)
	bundleDigest := writeStageBlob(t, f.RootDir, bundleBytes)
	if err := os.WriteFile(f.BundlePath, bundleBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var index Index
	readJSON(t, f.IndexPath, &index)
	for i := range index.Entries {
		if index.Entries[i].AppID == bundle.AppID && index.Entries[i].PayloadVersion == bundle.PayloadVersion {
			index.Entries[i].BundleManifestDigest = bundleDigest
		}
	}
	if err := os.WriteFile(f.IndexPath, mustMarshalStageJSON(t, index), 0o644); err != nil {
		t.Fatal(err)
	}
	f.BundleManifestDigest = bundleDigest
	return f
}

func corruptStageBlob(t *testing.T, root string, digest string, data []byte) {
	t.Helper()
	if err := os.WriteFile(stageBlobPath(root, digest), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeStageBlob(t *testing.T, root string, data []byte) string {
	t.Helper()
	digest := "sha256:" + dataSHA256(data)
	if err := os.WriteFile(stageBlobPath(root, digest), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return digest
}

func stageBlobPath(root string, digest string) string {
	return filepath.Join(root, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

func mustMarshalStageJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}
