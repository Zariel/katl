package generation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	APIVersion = "katl.dev/v1alpha1"
	Kind       = "GenerationRecord"
)

type Record struct {
	APIVersion        string             `json:"apiVersion"`
	Kind              string             `json:"kind"`
	GenerationID      string             `json:"generationID"`
	RuntimeVersion    string             `json:"runtimeVersion"`
	Root              RootSelection      `json:"root"`
	Boot              BootSelection      `json:"boot"`
	Sysexts           []ExtensionRef     `json:"sysexts"`
	Confexts          []GeneratedConfext `json:"confexts"`
	KernelCommandLine []string           `json:"kernelCommandLine"`
	CreatedAt         time.Time          `json:"createdAt"`
	BootState         string             `json:"bootState"`
	HealthState       string             `json:"healthState"`
}

type RootSelection struct {
	Slot                  string `json:"slot"`
	PartitionUUID         string `json:"partitionUUID"`
	RuntimeArtifactSHA256 string `json:"runtimeArtifactSHA256"`
}

type BootSelection struct {
	UKIPath string `json:"ukiPath"`
}

type ExtensionRef struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	ActivationPath string `json:"activationPath"`
	SHA256         string `json:"sha256"`
}

type GeneratedConfext struct {
	Name           string               `json:"name"`
	Path           string               `json:"path"`
	ActivationPath string               `json:"activationPath"`
	SHA256         string               `json:"sha256"`
	Compatibility  ConfextCompatibility `json:"compatibility"`
}

type ConfextCompatibility struct {
	ID           string `json:"id"`
	VersionID    string `json:"versionID"`
	ConfextLevel int    `json:"confextLevel"`
}

type FirstInstallRequest struct {
	GenerationID          string
	RuntimeVersion        string
	RootSlot              string
	RootPartitionUUID     string
	RuntimeArtifactSHA256 string
	UKIPath               string
	Sysexts               []ExtensionRef
	GeneratedConfext      GeneratedConfext
	KernelCommandLine     []string
	CreatedAt             time.Time
}

func NewFirstInstallRecord(request FirstInstallRequest) (Record, error) {
	if strings.TrimSpace(request.GenerationID) == "" {
		return Record{}, fmt.Errorf("generation id is required")
	}
	if strings.TrimSpace(request.RuntimeVersion) == "" {
		return Record{}, fmt.Errorf("runtime version is required")
	}
	if strings.TrimSpace(request.RootSlot) == "" {
		return Record{}, fmt.Errorf("root slot is required")
	}
	if strings.TrimSpace(request.RootPartitionUUID) == "" {
		return Record{}, fmt.Errorf("root partition UUID is required")
	}
	if err := validateSHA256("runtime artifact", request.RuntimeArtifactSHA256); err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(request.UKIPath) == "" {
		return Record{}, fmt.Errorf("UKI path is required")
	}
	if err := validateExtensionRefs(request.Sysexts); err != nil {
		return Record{}, err
	}
	confext, err := normalizeGeneratedConfext(request.GeneratedConfext)
	if err != nil {
		return Record{}, err
	}

	createdAt := request.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	return Record{
		APIVersion:     APIVersion,
		Kind:           Kind,
		GenerationID:   request.GenerationID,
		RuntimeVersion: request.RuntimeVersion,
		Root: RootSelection{
			Slot:                  request.RootSlot,
			PartitionUUID:         request.RootPartitionUUID,
			RuntimeArtifactSHA256: strings.ToLower(request.RuntimeArtifactSHA256),
		},
		Boot:              BootSelection{UKIPath: request.UKIPath},
		Sysexts:           append([]ExtensionRef(nil), request.Sysexts...),
		Confexts:          []GeneratedConfext{confext},
		KernelCommandLine: append([]string(nil), request.KernelCommandLine...),
		CreatedAt:         createdAt.UTC(),
		BootState:         "pending",
		HealthState:       "unknown",
	}, nil
}

func MarshalRecord(record Record) ([]byte, error) {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal generation record: %w", err)
	}
	return append(data, '\n'), nil
}

func WriteRecord(path string, record Record) error {
	data, err := MarshalRecord(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create generation metadata directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write generation metadata: %w", err)
	}
	return nil
}

func DigestDirectory(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("digest root is required")
	}
	var entries []string
	if err := filepath.WalkDir(root, func(path string, dirent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if dirent.IsDir() {
			return nil
		}
		info, err := dirent.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("digest input %s is not a regular file", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", fmt.Errorf("walk digest root: %w", err)
	}
	sort.Strings(entries)

	hash := sha256.New()
	for _, rel := range entries {
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "path=%s mode=%04o\n", rel, info.Mode().Perm())
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(hash, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		fmt.Fprintln(hash)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeGeneratedConfext(confext GeneratedConfext) (GeneratedConfext, error) {
	if strings.TrimSpace(confext.Name) == "" {
		confext.Name = "katl-node"
	}
	if strings.TrimSpace(confext.Path) == "" {
		return GeneratedConfext{}, fmt.Errorf("generated confext path is required")
	}
	if strings.TrimSpace(confext.ActivationPath) == "" {
		confext.ActivationPath = "/run/confexts/" + confext.Name
	}
	if err := validateSHA256("generated confext", confext.SHA256); err != nil {
		return GeneratedConfext{}, err
	}
	if strings.TrimSpace(confext.Compatibility.ID) == "" || strings.TrimSpace(confext.Compatibility.VersionID) == "" || confext.Compatibility.ConfextLevel < 1 {
		return GeneratedConfext{}, fmt.Errorf("generated confext compatibility metadata is required")
	}
	confext.SHA256 = strings.ToLower(confext.SHA256)
	return confext, nil
}

func validateExtensionRefs(refs []ExtensionRef) error {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Path) == "" || strings.TrimSpace(ref.ActivationPath) == "" {
			return fmt.Errorf("sysext name, path, and activation path are required")
		}
		if _, ok := seen[ref.Name]; ok {
			return fmt.Errorf("duplicate sysext %q", ref.Name)
		}
		seen[ref.Name] = struct{}{}
		if err := validateSHA256("sysext "+ref.Name, ref.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func validateSHA256(name string, value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("%s SHA-256 must be %d lowercase hex characters", name, sha256.Size*2)
	}
	if value != strings.ToLower(value) {
		return fmt.Errorf("%s SHA-256 must be lowercase hex", name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%s SHA-256 is invalid: %w", name, err)
	}
	return nil
}
