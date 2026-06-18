package nodeextensionbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFixture(t *testing.T) {
	root := t.TempDir()
	fixture, err := WriteFixture(FixtureRequest{
		OutputDir:       root,
		AppID:           "generic-fixture",
		PayloadVersion:  "generic-fixture-v0.1.0",
		ArtifactVersion: "generic-fixture-v0.1.0-build.1",
		Payload:         []byte("node extension payload"),
		CreatedAt:       "2026-06-18T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("WriteFixture() error = %v", err)
	}

	for _, path := range []string{
		fixture.BundlePath,
		fixture.IndexPath,
		fixture.CatalogPath,
		fixture.AppCatalogPath,
		filepath.Join(root, "bundles", "generic-fixture", "generic-fixture-v0.1.0", "x86_64", "catalog-entry.json"),
		filepath.Join(root, "bundles", "generic-fixture", "generic-fixture-v0.1.0", "x86_64", "package-provenance.json"),
		filepath.Join(root, "checksums.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat fixture output %s: %v", path, err)
		}
	}

	var bundle Bundle
	readJSON(t, fixture.BundlePath, &bundle)
	if bundle.Kind != BundleKind || bundle.ArtifactKind != ArtifactKind || bundle.AppID != "generic-fixture" {
		t.Fatalf("bundle identity = %#v", bundle)
	}
	if len(bundle.Capabilities) != 1 || bundle.Capabilities[0].Name != "fixture.node-extension.delivery" {
		t.Fatalf("bundle capabilities = %#v", bundle.Capabilities)
	}
	if len(bundle.Compatibility.SupportedRuntimeInterfaces) != 1 || bundle.Compatibility.SupportedRuntimeInterfaces[0] != "katl-runtime-1" {
		t.Fatalf("bundle runtime compatibility = %#v", bundle.Compatibility)
	}
	if len(bundle.Configuration.ConfigHandoffPaths) != 1 || bundle.Configuration.ConfigHandoffPaths[0] != "/etc/katl/apps/generic-fixture/config.yaml" {
		t.Fatalf("bundle config handoff = %#v", bundle.Configuration)
	}
	if bundle.Status.LiveStatusPath != "/run/katl/apps/generic-fixture/status.json" || !strings.Contains(bundle.Status.DurableSnapshotPath, "/var/lib/katl/operations/<operation-id>/apps/generic-fixture/status.json") {
		t.Fatalf("bundle status = %#v", bundle.Status)
	}
	if bundle.Provenance.BuildInputDigest != fixture.PayloadDigest || len(bundle.Signatures) != 1 || bundle.Signatures[0].Type != "unsigned-fixture" {
		t.Fatalf("bundle provenance/signature = %#v %#v", bundle.Provenance, bundle.Signatures)
	}
	if len(bundle.Payloads) != 1 || bundle.Payloads[0].Digest != fixture.PayloadDigest || bundle.Payloads[0].MediaType != SysextRawMediaType {
		t.Fatalf("bundle payload descriptors = %#v", bundle.Payloads)
	}
	if fixture.BundleManifestDigest != "sha256:"+fileSHA256(t, fixture.BundlePath) {
		t.Fatalf("bundle digest = %q, want file digest", fixture.BundleManifestDigest)
	}
	assertBlobEquals(t, root, fixture.BundleManifestDigest, fixture.BundlePath)
	assertBlobBytes(t, root, fixture.PayloadDigest, []byte("node extension payload"))
	assertMetadataDescriptors(t, root, filepath.Dir(fixture.BundlePath), bundle.Metadata)
	var bundleCatalogFragment IndexEntry
	readJSON(t, filepath.Join(filepath.Dir(fixture.BundlePath), "catalog-entry.json"), &bundleCatalogFragment)
	if bundleCatalogFragment.BundleManifestDigest != "" || bundleCatalogFragment.SysextPayloadDigest != fixture.PayloadDigest {
		t.Fatalf("bundle catalog fragment = %#v", bundleCatalogFragment)
	}

	var index Index
	readJSON(t, fixture.IndexPath, &index)
	if len(index.Entries) != 1 || index.Entries[0].BundleManifestDigest != fixture.BundleManifestDigest || index.Entries[0].SysextPayloadDigest != fixture.PayloadDigest {
		t.Fatalf("index = %#v", index)
	}
	if len(index.Entries[0].Capabilities) != 1 || index.Entries[0].Capabilities[0].Name != "fixture.node-extension.delivery" {
		t.Fatalf("index capabilities = %#v", index.Entries[0].Capabilities)
	}

	var catalog Catalog
	readJSON(t, fixture.CatalogPath, &catalog)
	if len(catalog.Entries) != 1 || catalog.Entries[0].AppID != "generic-fixture" || catalog.Entries[0].BundleManifestDigest != fixture.BundleManifestDigest {
		t.Fatalf("catalog = %#v", catalog)
	}
	var appCatalog Catalog
	readJSON(t, fixture.AppCatalogPath, &appCatalog)
	if appCatalog.AppID != "generic-fixture" || len(appCatalog.Entries) != 1 || appCatalog.Entries[0].BundleManifestDigest != fixture.BundleManifestDigest {
		t.Fatalf("app catalog = %#v", appCatalog)
	}

	checksums := readText(t, filepath.Join(root, "checksums.txt"))
	for _, want := range []string{
		"index.json",
		"catalog/node-extensions.json",
		"catalog/generic-fixture.json",
		"bundles/generic-fixture/generic-fixture-v0.1.0/x86_64/bundle.json",
	} {
		if !strings.Contains(checksums, want) {
			t.Fatalf("checksums.txt missing %s", want)
		}
	}
	for _, path := range []string{fixture.BundlePath, fixture.IndexPath, fixture.CatalogPath, fixture.AppCatalogPath} {
		data := readText(t, path)
		if strings.Contains(data, root) {
			t.Fatalf("%s contains host path: %s", path, data)
		}
	}
}

func TestFixtureCanBeServedFromLocalHTTPSStaticSource(t *testing.T) {
	root := t.TempDir()
	fixture, err := WriteFixture(FixtureRequest{OutputDir: root})
	if err != nil {
		t.Fatalf("WriteFixture() error = %v", err)
	}
	server := httptest.NewTLSServer(http.FileServer(http.Dir(root)))
	defer server.Close()
	client := server.Client()

	for _, rel := range []string{
		"index.json",
		strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(fixture.BundlePath, root)), "/"),
		"blobs/sha256/" + strings.TrimPrefix(fixture.BundleManifestDigest, "sha256:"),
	} {
		resp, err := client.Get(server.URL + "/" + rel)
		if err != nil {
			t.Fatalf("GET %s: %v", rel, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("GET %s status %d len %d", rel, resp.StatusCode, len(body))
		}
	}
}

func TestWriteFixtureRejectsUnsafeAppID(t *testing.T) {
	_, err := WriteFixture(FixtureRequest{
		OutputDir: t.TempDir(),
		AppID:     "../route-helper",
	})
	if err == nil || !strings.Contains(err.Error(), "safe path segment") {
		t.Fatalf("WriteFixture() error = %v, want safe path segment", err)
	}
}

func assertMetadataDescriptors(t *testing.T, root string, bundleDir string, descriptors []Descriptor) {
	t.Helper()
	byName := map[string]Descriptor{}
	for _, descriptor := range descriptors {
		byName[descriptor.FileName] = descriptor
	}
	for _, name := range []string{"package-provenance.json", "catalog-entry.json"} {
		descriptor, ok := byName[name]
		if !ok {
			t.Fatalf("descriptor %s missing from %#v", name, descriptors)
		}
		path := filepath.Join(bundleDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if descriptor.SizeBytes != int64(len(data)) {
			t.Fatalf("%s descriptor size = %d, want %d", name, descriptor.SizeBytes, len(data))
		}
		if descriptor.Digest != "sha256:"+dataSHA256(data) {
			t.Fatalf("%s descriptor digest = %q, want sha256:%s", name, descriptor.Digest, dataSHA256(data))
		}
		assertBlobEquals(t, root, descriptor.Digest, path)
	}
}

func assertBlobEquals(t *testing.T, root string, digest string, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	assertBlobBytes(t, root, digest, data)
}

func assertBlobBytes(t *testing.T, root string, digest string, want []byte) {
	t.Helper()
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("digest %q missing sha256 prefix", digest)
	}
	blobPath := filepath.Join(root, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
	got, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("read blob %s: %v", blobPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("blob %s does not match expected bytes", blobPath)
	}
}

func readJSON(t *testing.T, path string, dest any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return dataSHA256(data)
}

func dataSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
