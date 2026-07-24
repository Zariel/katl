package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type LocalMeta struct {
	Name              string            `json:"name"`
	Kind              ArtifactKind      `json:"kind"`
	Format            string            `json:"format"`
	Path              string            `json:"path"`
	SizeBytes         int64             `json:"sizeBytes"`
	SHA256            string            `json:"sha256"`
	Compression       string            `json:"compression,omitempty"`
	Generation        string            `json:"generation,omitempty"`
	Version           string            `json:"version,omitempty"`
	PayloadVersion    string            `json:"payloadVersion,omitempty"`
	Architecture      string            `json:"architecture"`
	SourceRepo        *SourceRepo       `json:"sourceRepo,omitempty"`
	PackageVersions   map[string]string `json:"packageVersions,omitempty"`
	RuntimeInterface  string            `json:"runtimeInterface,omitempty"`
	CompatibleBoot    *BootCompat       `json:"compatibleBoot,omitempty"`
	CompatibleRuntime *Compat           `json:"compatibleRuntime,omitempty"`
	Created           string            `json:"created"`
}

type BootCompat struct {
	Kind              ArtifactKind `json:"kind"`
	RuntimeInterface  string       `json:"runtimeInterface"`
	ArtifactSHA256    string       `json:"artifactSHA256,omitempty"`
	KernelCommandLine []string     `json:"kernelCommandLine,omitempty"`
}

type SourceRepo struct {
	ID      string `json:"id"`
	BaseURL string `json:"baseURL"`
	Minor   string `json:"minor"`
}

type Compat struct {
	Interface      string `json:"interface"`
	ArtifactPath   string `json:"artifactPath,omitempty"`
	ArtifactSHA256 string `json:"artifactSHA256,omitempty"`
}

func ReadLocal(path string) (LocalMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LocalMeta{}, err
	}

	var meta LocalMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return LocalMeta{}, err
	}
	if err := meta.validate(); err != nil {
		return LocalMeta{}, err
	}
	return meta, nil
}

func (m LocalMeta) Spec(baseURL string) ArtifactSpec {
	return ArtifactSpec{
		Name:       m.Name,
		Kind:       m.Kind,
		URL:        strings.TrimRight(baseURL, "/") + "/" + m.Path,
		SHA256:     m.SHA256,
		SizeBytes:  m.SizeBytes,
		Generation: m.Generation,
	}
}

func (m LocalMeta) VerifyFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat local artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local artifact is not a regular file")
	}
	if filepath.Base(path) != filepath.FromSlash(m.Path) {
		return fmt.Errorf("local artifact filename %q does not match metadata %q", filepath.Base(path), m.Path)
	}
	if info.Size() != m.SizeBytes {
		return fmt.Errorf("local artifact size %d does not match metadata %d", info.Size(), m.SizeBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open local artifact: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash local artifact: %w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != m.SHA256 {
		return fmt.Errorf("local artifact SHA-256 %s does not match metadata %s", got, m.SHA256)
	}
	return nil
}

func (m LocalMeta) validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: local artifact name is required", ErrInvalidArtifactSpec)
	}
	if m.Kind == "" {
		return fmt.Errorf("%w: local artifact kind is required", ErrInvalidArtifactSpec)
	}
	if strings.TrimSpace(m.Format) == "" {
		return fmt.Errorf("%w: local artifact format is required", ErrInvalidArtifactSpec)
	}
	if strings.TrimSpace(m.Path) == "" {
		return fmt.Errorf("%w: local artifact path is required", ErrInvalidArtifactSpec)
	}
	if m.SizeBytes <= 0 {
		return fmt.Errorf("%w: local artifact size must be positive", ErrInvalidArtifactSpec)
	}
	if _, err := parseSHA256(m.SHA256); err != nil {
		return fmt.Errorf("%w: local artifact SHA-256 is invalid: %v", ErrInvalidArtifactSpec, err)
	}
	return nil
}
