package sysextcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/katl-dev/katl/internal/installer/artifact"
)

const (
	APIVersion = "katl.dev/v1alpha1"
	Kind       = "KubernetesSysextCatalog"
)

var (
	ErrInvalidCatalog = errors.New("invalid Kubernetes sysext catalog")

	payloadVersionRE = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)
)

type Catalog struct {
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Entries    []Entry `json:"entries"`
}

type Entry struct {
	Name              string              `json:"name"`
	ArtifactVersion   string              `json:"artifactVersion"`
	PayloadVersion    string              `json:"payloadVersion"`
	KubernetesMinor   string              `json:"kubernetesMinor"`
	Architecture      string              `json:"architecture"`
	SHA256            string              `json:"sha256"`
	SizeBytes         int64               `json:"sizeBytes"`
	SourceRepo        artifact.SourceRepo `json:"sourceRepo"`
	URL               string              `json:"url,omitempty"`
	LocalPath         string              `json:"localPath,omitempty"`
	RuntimeInterfaces []string            `json:"runtimeInterfaces"`
	PackageVersions   map[string]string   `json:"packageVersions,omitempty"`
}

type Runtime struct {
	Interface    string
	Architecture string
}

func Read(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}

	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("%w: decode catalog: %v", ErrInvalidCatalog, err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func Marshal(catalog Catalog) ([]byte, error) {
	if err := Validate(catalog); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal Kubernetes sysext catalog: %w", err)
	}
	return append(data, '\n'), nil
}

func Validate(catalog Catalog) error {
	if catalog.APIVersion != APIVersion {
		return fmt.Errorf("%w: apiVersion must be %q", ErrInvalidCatalog, APIVersion)
	}
	if catalog.Kind != Kind {
		return fmt.Errorf("%w: kind must be %q", ErrInvalidCatalog, Kind)
	}
	if len(catalog.Entries) == 0 {
		return fmt.Errorf("%w: at least one entry is required", ErrInvalidCatalog)
	}

	seen := make(map[string]struct{}, len(catalog.Entries))
	for i, entry := range catalog.Entries {
		if err := validateEntry(entry); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		key := entry.Name + "/" + entry.ArtifactVersion + "/" + entry.PayloadVersion + "/" + entry.Architecture
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: duplicate entry %s", ErrInvalidCatalog, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ValidateForRuntime(entry Entry, runtime Runtime) error {
	if err := validateEntry(entry); err != nil {
		return err
	}
	if strings.TrimSpace(runtime.Architecture) == "" {
		return fmt.Errorf("%w: runtime architecture is required", ErrInvalidCatalog)
	}
	if strings.TrimSpace(runtime.Interface) == "" {
		return fmt.Errorf("%w: runtime interface is required", ErrInvalidCatalog)
	}
	if entry.Architecture != runtime.Architecture {
		return fmt.Errorf("%w: sysext %q architecture %q is incompatible with runtime architecture %q", ErrInvalidCatalog, entry.Name, entry.Architecture, runtime.Architecture)
	}
	for _, candidate := range entry.RuntimeInterfaces {
		if candidate == runtime.Interface {
			return nil
		}
	}
	return fmt.Errorf("%w: sysext %q does not support runtime interface %q", ErrInvalidCatalog, entry.Name, runtime.Interface)
}

func EntryFromLocalMeta(meta artifact.LocalMeta) (Entry, error) {
	if meta.Kind != artifact.ArtifactSysext {
		return Entry{}, fmt.Errorf("%w: local metadata kind %q is not %q", ErrInvalidCatalog, meta.Kind, artifact.ArtifactSysext)
	}

	entry := Entry{
		Name:              meta.Name,
		ArtifactVersion:   meta.Version,
		PayloadVersion:    meta.PayloadVersion,
		KubernetesMinor:   KubernetesMinor(meta.PayloadVersion),
		Architecture:      meta.Architecture,
		SHA256:            meta.SHA256,
		SizeBytes:         meta.SizeBytes,
		LocalPath:         meta.Path,
		RuntimeInterfaces: runtimeInterfacesFromLocalMeta(meta),
		PackageVersions:   copyPackageVersions(meta.PackageVersions),
	}
	if meta.SourceRepo != nil {
		entry.SourceRepo = *meta.SourceRepo
	}
	if err := validateEntry(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func KubernetesMinor(payloadVersion string) string {
	match := payloadVersionRE.FindStringSubmatch(payloadVersion)
	if match == nil {
		return ""
	}
	return "v" + match[1] + "." + match[2]
}

func validateEntry(entry Entry) error {
	if strings.TrimSpace(entry.Name) == "" {
		return fmt.Errorf("%w: sysext name is required", ErrInvalidCatalog)
	}
	if strings.TrimSpace(entry.ArtifactVersion) == "" {
		return fmt.Errorf("%w: sysext %q artifact version is required", ErrInvalidCatalog, entry.Name)
	}
	if strings.TrimSpace(entry.PayloadVersion) == "" {
		return fmt.Errorf("%w: sysext %q payload version is required", ErrInvalidCatalog, entry.Name)
	}
	minor := KubernetesMinor(entry.PayloadVersion)
	if minor == "" {
		return fmt.Errorf("%w: sysext %q payload version %q must be vMAJOR.MINOR.PATCH", ErrInvalidCatalog, entry.Name, entry.PayloadVersion)
	}
	if entry.KubernetesMinor != minor {
		return fmt.Errorf("%w: sysext %q Kubernetes minor %q does not match payload version %q", ErrInvalidCatalog, entry.Name, entry.KubernetesMinor, entry.PayloadVersion)
	}
	if strings.TrimSpace(entry.Architecture) == "" {
		return fmt.Errorf("%w: sysext %q architecture is required", ErrInvalidCatalog, entry.Name)
	}
	if err := validateSHA256(entry.SHA256); err != nil {
		return fmt.Errorf("%w: sysext %q SHA-256 is invalid: %v", ErrInvalidCatalog, entry.Name, err)
	}
	if entry.SizeBytes <= 0 {
		return fmt.Errorf("%w: sysext %q size must be positive", ErrInvalidCatalog, entry.Name)
	}
	if strings.TrimSpace(entry.URL) == "" && strings.TrimSpace(entry.LocalPath) == "" {
		return fmt.Errorf("%w: sysext %q requires a URL or local path", ErrInvalidCatalog, entry.Name)
	}
	if strings.TrimSpace(entry.URL) != "" {
		parsed, err := url.Parse(entry.URL)
		if err != nil {
			return fmt.Errorf("%w: sysext %q URL is invalid: %v", ErrInvalidCatalog, entry.Name, err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%w: sysext %q URL must be absolute", ErrInvalidCatalog, entry.Name)
		}
	}
	if strings.TrimSpace(entry.SourceRepo.ID) == "" || strings.TrimSpace(entry.SourceRepo.BaseURL) == "" || strings.TrimSpace(entry.SourceRepo.Minor) == "" {
		return fmt.Errorf("%w: sysext %q source repo id, baseURL, and minor are required", ErrInvalidCatalog, entry.Name)
	}
	if entry.SourceRepo.Minor != entry.KubernetesMinor {
		return fmt.Errorf("%w: sysext %q source repo minor %q does not match Kubernetes minor %q", ErrInvalidCatalog, entry.Name, entry.SourceRepo.Minor, entry.KubernetesMinor)
	}
	if len(entry.RuntimeInterfaces) == 0 {
		return fmt.Errorf("%w: sysext %q runtime interfaces are required", ErrInvalidCatalog, entry.Name)
	}
	for _, runtimeInterface := range entry.RuntimeInterfaces {
		if strings.TrimSpace(runtimeInterface) == "" {
			return fmt.Errorf("%w: sysext %q runtime interfaces must be non-empty", ErrInvalidCatalog, entry.Name)
		}
	}
	return nil
}

func validateSHA256(value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("must be %d lowercase hex characters", sha256.Size*2)
	}
	if value != strings.ToLower(value) {
		return fmt.Errorf("must be lowercase hex")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return err
	}
	return nil
}

func runtimeInterfacesFromLocalMeta(meta artifact.LocalMeta) []string {
	seen := map[string]struct{}{}
	var interfaces []string
	for _, runtimeInterface := range []string{meta.RuntimeInterface, compatRuntimeInterface(meta)} {
		if strings.TrimSpace(runtimeInterface) == "" {
			continue
		}
		if _, ok := seen[runtimeInterface]; ok {
			continue
		}
		seen[runtimeInterface] = struct{}{}
		interfaces = append(interfaces, runtimeInterface)
	}
	return interfaces
}

func compatRuntimeInterface(meta artifact.LocalMeta) string {
	if meta.CompatibleRuntime == nil {
		return ""
	}
	return meta.CompatibleRuntime.Interface
}

func copyPackageVersions(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for name, version := range source {
		copied[name] = version
	}
	return copied
}
