package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

type ArtifactKind string

const (
	ArtifactRuntimeRoot ArtifactKind = "runtime-root"
	ArtifactUKI         ArtifactKind = "uki"
	ArtifactSysext      ArtifactKind = "sysext"
	ArtifactConfext     ArtifactKind = "confext"
	ArtifactManifest    ArtifactKind = "manifest"
)

type ArtifactSpec struct {
	Name       string
	Kind       ArtifactKind
	URL        string
	SHA256     string
	SizeBytes  int64
	Generation string
}

type TrustPolicy struct {
	Roots []TrustRoot
}

type TrustRoot struct {
	Name   string
	Type   string
	Inline string
	URL    string
	SHA256 string
}

type ArtifactFetcher interface {
	FetchArtifact(context.Context, ArtifactSpec) (io.ReadCloser, error)
}

type ArtifactVerification struct {
	Name       string
	Kind       ArtifactKind
	URL        string
	SHA256     string
	SizeBytes  int64
	TrustRoots []string
}

var (
	ErrMissingTrustInput      = errors.New("artifact trust input is required")
	ErrInvalidArtifactSpec    = errors.New("invalid artifact specification")
	ErrArtifactDigestMismatch = errors.New("artifact digest mismatch")
	ErrArtifactSetMismatch    = errors.New("artifact set does not match manifest")
)

func VerifyArtifacts(ctx context.Context, specs []ArtifactSpec, trust TrustPolicy, fetcher ArtifactFetcher) ([]ArtifactVerification, error) {
	if err := validateTrustPolicy(trust); err != nil {
		return nil, err
	}
	if fetcher == nil {
		return nil, fmt.Errorf("artifact fetcher is required")
	}

	if err := validateArtifactSet(specs); err != nil {
		return nil, err
	}

	results := make([]ArtifactVerification, 0, len(specs))
	for _, spec := range specs {
		result, err := verifyArtifact(ctx, spec, trust, fetcher)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func validateTrustPolicy(policy TrustPolicy) error {
	if len(policy.Roots) == 0 {
		return ErrMissingTrustInput
	}
	for _, root := range policy.Roots {
		if strings.TrimSpace(root.Name) == "" || strings.TrimSpace(root.Type) == "" {
			return fmt.Errorf("%w: trust roots require name and type", ErrMissingTrustInput)
		}
		if root.Inline == "" && (root.URL == "" || root.SHA256 == "") {
			return fmt.Errorf("%w: remote trust root %q requires URL and SHA-256 pin", ErrMissingTrustInput, root.Name)
		}
		if root.SHA256 != "" {
			if _, err := parseSHA256(root.SHA256); err != nil {
				return fmt.Errorf("%w: trust root %q SHA-256 is invalid: %v", ErrMissingTrustInput, root.Name, err)
			}
		}
	}
	return nil
}

func validateArtifactSet(specs []ArtifactSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("%w: at least one artifact is required", ErrInvalidArtifactSpec)
	}

	seen := make(map[string]ArtifactSpec, len(specs))
	hasRuntimeRoot := false
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			return fmt.Errorf("%w: artifact name is required", ErrInvalidArtifactSpec)
		}
		if spec.Kind == "" {
			return fmt.Errorf("%w: artifact %q kind is required", ErrInvalidArtifactSpec, spec.Name)
		}
		if spec.Kind == ArtifactRuntimeRoot {
			hasRuntimeRoot = true
		}
		if _, err := url.ParseRequestURI(spec.URL); err != nil {
			return fmt.Errorf("%w: artifact %q URL is invalid: %v", ErrInvalidArtifactSpec, spec.Name, err)
		}
		if _, err := parseSHA256(spec.SHA256); err != nil {
			return fmt.Errorf("%w: artifact %q SHA-256 is invalid: %v", ErrInvalidArtifactSpec, spec.Name, err)
		}
		key := string(spec.Kind) + "/" + spec.Name
		if previous, ok := seen[key]; ok && previous.URL != spec.URL {
			return fmt.Errorf("%w: artifact %s declared with multiple URLs", ErrArtifactSetMismatch, key)
		}
		seen[key] = spec
	}
	if !hasRuntimeRoot {
		return fmt.Errorf("%w: runtime root artifact is required", ErrArtifactSetMismatch)
	}

	return nil
}

func verifyArtifact(ctx context.Context, spec ArtifactSpec, trust TrustPolicy, fetcher ArtifactFetcher) (ArtifactVerification, error) {
	reader, err := fetcher.FetchArtifact(ctx, spec)
	if err != nil {
		return ArtifactVerification{}, fmt.Errorf("fetch artifact %q: %w", spec.Name, err)
	}
	defer reader.Close()

	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return ArtifactVerification{}, fmt.Errorf("hash artifact %q: %w", spec.Name, err)
	}

	gotDigest := hex.EncodeToString(hash.Sum(nil))
	if gotDigest != strings.ToLower(spec.SHA256) {
		return ArtifactVerification{}, fmt.Errorf("%w: artifact %q got %s want %s", ErrArtifactDigestMismatch, spec.Name, gotDigest, spec.SHA256)
	}
	if spec.SizeBytes > 0 && size != spec.SizeBytes {
		return ArtifactVerification{}, fmt.Errorf("%w: artifact %q got %d bytes want %d", ErrArtifactSetMismatch, spec.Name, size, spec.SizeBytes)
	}

	return ArtifactVerification{
		Name:       spec.Name,
		Kind:       spec.Kind,
		URL:        spec.URL,
		SHA256:     gotDigest,
		SizeBytes:  size,
		TrustRoots: trustRootNames(trust),
	}, nil
}

func parseSHA256(value string) ([]byte, error) {
	if len(value) != sha256.Size*2 {
		return nil, fmt.Errorf("must be %d lowercase hex characters", sha256.Size*2)
	}
	if value != strings.ToLower(value) {
		return nil, fmt.Errorf("must be lowercase hex")
	}
	return hex.DecodeString(value)
}

func trustRootNames(policy TrustPolicy) []string {
	names := make([]string, 0, len(policy.Roots))
	for _, root := range policy.Roots {
		names = append(names, root.Name)
	}
	return names
}
