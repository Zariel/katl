package artifact

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestVerifyArtifactsValidArtifact(t *testing.T) {
	specs := []ArtifactSpec{
		{
			Name:      "runtime-root",
			Kind:      ArtifactRuntimeRoot,
			URL:       "https://artifacts.example/katl/runtime-root.squashfs",
			SHA256:    "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
			SizeBytes: 5,
		},
	}

	results, err := VerifyArtifacts(context.Background(), specs, testTrustPolicy(), staticArtifactFetcher{
		"https://artifacts.example/katl/runtime-root.squashfs": "hello",
	})
	if err != nil {
		t.Fatalf("VerifyArtifacts() error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("verification count = %d, want 1", len(results))
	}
	if results[0].SHA256 != specs[0].SHA256 || results[0].SizeBytes != 5 {
		t.Fatalf("verification = %#v", results[0])
	}
	if len(results[0].TrustRoots) != 1 || results[0].TrustRoots[0] != "lab-ca" {
		t.Fatalf("trust roots = %#v", results[0].TrustRoots)
	}
}

func TestVerifyArtifactsDigestMismatch(t *testing.T) {
	specs := []ArtifactSpec{
		{
			Name:   "runtime-root",
			Kind:   ArtifactRuntimeRoot,
			URL:    "https://artifacts.example/katl/runtime-root.squashfs",
			SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}

	_, err := VerifyArtifacts(context.Background(), specs, testTrustPolicy(), staticArtifactFetcher{
		"https://artifacts.example/katl/runtime-root.squashfs": "hello",
	})
	if !errors.Is(err, ErrArtifactDigestMismatch) {
		t.Fatalf("VerifyArtifacts() error = %v, want ErrArtifactDigestMismatch", err)
	}
}

func TestVerifyArtifactsMissingTrustInput(t *testing.T) {
	specs := []ArtifactSpec{
		{
			Name:   "runtime-root",
			Kind:   ArtifactRuntimeRoot,
			URL:    "https://artifacts.example/katl/runtime-root.squashfs",
			SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		},
	}

	_, err := VerifyArtifacts(context.Background(), specs, TrustPolicy{}, staticArtifactFetcher{})
	if !errors.Is(err, ErrMissingTrustInput) {
		t.Fatalf("VerifyArtifacts() error = %v, want ErrMissingTrustInput", err)
	}
}

func TestVerifyArtifactsManifestArtifactMismatch(t *testing.T) {
	tests := []struct {
		name  string
		specs []ArtifactSpec
	}{
		{
			name: "missing runtime root",
			specs: []ArtifactSpec{
				{
					Name:   "katl",
					Kind:   ArtifactUKI,
					URL:    "https://artifacts.example/katl/katl.efi",
					SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
				},
			},
		},
		{
			name: "duplicate artifact with different URL",
			specs: []ArtifactSpec{
				{
					Name:   "runtime-root",
					Kind:   ArtifactRuntimeRoot,
					URL:    "https://artifacts.example/katl/runtime-root.squashfs",
					SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
				},
				{
					Name:   "runtime-root",
					Kind:   ArtifactRuntimeRoot,
					URL:    "https://mirror.example/katl/runtime-root.squashfs",
					SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
				},
			},
		},
		{
			name: "size mismatch",
			specs: []ArtifactSpec{
				{
					Name:      "runtime-root",
					Kind:      ArtifactRuntimeRoot,
					URL:       "https://artifacts.example/katl/runtime-root.squashfs",
					SHA256:    "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
					SizeBytes: 6,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyArtifacts(context.Background(), tt.specs, testTrustPolicy(), staticArtifactFetcher{
				"https://artifacts.example/katl/runtime-root.squashfs": "hello",
				"https://mirror.example/katl/runtime-root.squashfs":    "hello",
				"https://artifacts.example/katl/katl.efi":              "hello",
			})
			if !errors.Is(err, ErrArtifactSetMismatch) {
				t.Fatalf("VerifyArtifacts() error = %v, want ErrArtifactSetMismatch", err)
			}
		})
	}
}

func testTrustPolicy() TrustPolicy {
	return TrustPolicy{
		Roots: []TrustRoot{
			{Name: "lab-ca", Type: "ca-certificate-pem", Inline: "test root"},
		},
	}
}

type staticArtifactFetcher map[string]string

func (f staticArtifactFetcher) FetchArtifact(_ context.Context, spec ArtifactSpec) (io.ReadCloser, error) {
	data, ok := f[spec.URL]
	if !ok {
		return nil, errors.New("missing artifact fixture")
	}
	return io.NopCloser(strings.NewReader(data)), nil
}
