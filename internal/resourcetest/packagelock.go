package resourcetest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const PackageLockKind = "ResourcePackageLock"

type PackageLock struct {
	APIVersion    string                  `json:"apiVersion"`
	Kind          string                  `json:"kind"`
	Tools         []Tool                  `json:"tools,omitempty"`
	MkosiProfiles []PackageLockProfile    `json:"mkosiProfiles"`
	PackageSets   []PackageLockPackageSet `json:"packageSets"`
}

type PackageLockProfile struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	ConfigDigest  string `json:"configSHA256,omitempty"`
	PackageSetRef string `json:"packageSetRef"`
	MkosiVersion  string `json:"mkosiVersion,omitempty"`
}

type PackageLockPackageSet struct {
	Name         string              `json:"name"`
	Source       string              `json:"source,omitempty"`
	Distribution string              `json:"distribution,omitempty"`
	Release      string              `json:"release,omitempty"`
	Architecture string              `json:"architecture,omitempty"`
	Repositories []PackageRepository `json:"repositories,omitempty"`
	Packages     []Package           `json:"packages"`
}

type PackageRepository struct {
	ID      string `json:"id"`
	BaseURL string `json:"baseURL,omitempty"`
	GPGKey  string `json:"gpgKey,omitempty"`
}

type PackageLockVerification struct {
	Lock       PackageLock
	Manifest   Manifest
	LockDigest string
}

func DecodePackageLock(r io.Reader) (PackageLock, error) {
	var lock PackageLock
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		return PackageLock{}, err
	}
	if err := ValidatePackageLock(lock); err != nil {
		return PackageLock{}, err
	}
	return lock, nil
}

func PackageLockDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func VerifyPackageLock(verification PackageLockVerification) error {
	if err := ValidatePackageLock(verification.Lock); err != nil {
		return err
	}
	if err := ValidateManifest(verification.Manifest); err != nil {
		return err
	}
	if !validSHA256(verification.LockDigest) {
		return errors.New("lock digest must be lowercase SHA-256")
	}

	manifestProfiles := map[string]MkosiProfile{}
	for _, profile := range verification.Manifest.MkosiProfiles {
		manifestProfiles[profile.Name] = profile
	}
	manifestSets := map[string]PackageSet{}
	for _, set := range verification.Manifest.PackageSets {
		manifestSets[set.Name] = set
	}
	lockSets := map[string]PackageLockPackageSet{}
	for _, set := range verification.Lock.PackageSets {
		lockSets[set.Name] = set
	}

	for _, lockedProfile := range verification.Lock.MkosiProfiles {
		manifestProfile, ok := manifestProfiles[lockedProfile.Name]
		if !ok {
			return fmt.Errorf("mkosi profile %q is missing from resource manifest", lockedProfile.Name)
		}
		if manifestProfile.Path != lockedProfile.Path {
			return fmt.Errorf("mkosi profile %q path drift: got %q, want %q", lockedProfile.Name, manifestProfile.Path, lockedProfile.Path)
		}
		if lockedProfile.ConfigDigest != "" && manifestProfile.ConfigDigest != lockedProfile.ConfigDigest {
			return fmt.Errorf("mkosi profile %q config digest drift", lockedProfile.Name)
		}
		if manifestProfile.PackageSetRef != lockedProfile.PackageSetRef {
			return fmt.Errorf("mkosi profile %q package set drift: got %q, want %q", lockedProfile.Name, manifestProfile.PackageSetRef, lockedProfile.PackageSetRef)
		}

		lockedSet, ok := lockSets[lockedProfile.PackageSetRef]
		if !ok {
			return fmt.Errorf("package set %q is missing from package lock", lockedProfile.PackageSetRef)
		}
		manifestSet, ok := manifestSets[lockedProfile.PackageSetRef]
		if !ok {
			return fmt.Errorf("package set %q is missing from resource manifest", lockedProfile.PackageSetRef)
		}
		if manifestSet.LockDigest != verification.LockDigest {
			return fmt.Errorf("package set %q lock digest drift: got %q, want %q", manifestSet.Name, manifestSet.LockDigest, verification.LockDigest)
		}
		if lockedSet.Source != "" && manifestSet.Source != lockedSet.Source {
			return fmt.Errorf("package set %q source drift: got %q, want %q", manifestSet.Name, manifestSet.Source, lockedSet.Source)
		}
		if err := comparePackages(manifestSet.Name, manifestSet.Packages, lockedSet.Packages); err != nil {
			return err
		}
	}
	return nil
}

func ValidatePackageLock(lock PackageLock) error {
	if lock.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q", APIVersion)
	}
	if lock.Kind != PackageLockKind {
		return fmt.Errorf("kind must be %q", PackageLockKind)
	}
	if len(lock.MkosiProfiles) == 0 {
		return errors.New("mkosiProfiles is required")
	}
	if len(lock.PackageSets) == 0 {
		return errors.New("packageSets is required")
	}
	sets := map[string]bool{}
	for i, set := range lock.PackageSets {
		if err := validateLockedPackageSet(set); err != nil {
			return fmt.Errorf("packageSets[%d]: %w", i, err)
		}
		if sets[set.Name] {
			return fmt.Errorf("packageSets[%d]: duplicate package set %q", i, set.Name)
		}
		sets[set.Name] = true
	}
	profiles := map[string]bool{}
	for i, profile := range lock.MkosiProfiles {
		if strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.Path) == "" {
			return fmt.Errorf("mkosiProfiles[%d]: name and path are required", i)
		}
		if profiles[profile.Name] {
			return fmt.Errorf("mkosiProfiles[%d]: duplicate profile %q", i, profile.Name)
		}
		profiles[profile.Name] = true
		if profile.ConfigDigest != "" && !validSHA256(profile.ConfigDigest) {
			return fmt.Errorf("mkosiProfiles[%d]: configSHA256 must be lowercase SHA-256", i)
		}
		if strings.TrimSpace(profile.PackageSetRef) == "" {
			return fmt.Errorf("mkosiProfiles[%d]: packageSetRef is required", i)
		}
		if !sets[profile.PackageSetRef] {
			return fmt.Errorf("mkosiProfiles[%d]: packageSetRef %q is not defined", i, profile.PackageSetRef)
		}
	}
	return nil
}

func validateLockedPackageSet(set PackageLockPackageSet) error {
	if strings.TrimSpace(set.Name) == "" {
		return errors.New("name is required")
	}
	if len(set.Repositories) == 0 {
		return errors.New("repositories is required")
	}
	repositories := map[string]bool{}
	for i, repo := range set.Repositories {
		if strings.TrimSpace(repo.ID) == "" {
			return fmt.Errorf("repositories[%d]: id is required", i)
		}
		if repositories[repo.ID] {
			return fmt.Errorf("repositories[%d]: duplicate repository %q", i, repo.ID)
		}
		repositories[repo.ID] = true
	}
	if len(set.Packages) == 0 {
		return errors.New("packages is required")
	}
	for i, pkg := range set.Packages {
		if strings.TrimSpace(pkg.Name) == "" || strings.TrimSpace(pkg.NEVRA) == "" {
			return fmt.Errorf("packages[%d]: name and nevra are required", i)
		}
		if pkg.Checksum != "" && !validSHA256(pkg.Checksum) {
			return fmt.Errorf("packages[%d]: sha256 must be lowercase SHA-256", i)
		}
	}
	return nil
}

func comparePackages(name string, got, want []Package) error {
	gotPackages := map[string]Package{}
	for _, pkg := range got {
		gotPackages[pkg.Name] = pkg
	}
	for _, locked := range want {
		actual, ok := gotPackages[locked.Name]
		if !ok {
			return fmt.Errorf("package set %q missing package %q", name, locked.Name)
		}
		if actual.NEVRA != locked.NEVRA {
			return fmt.Errorf("package set %q package %q NEVRA drift: got %q, want %q", name, locked.Name, actual.NEVRA, locked.NEVRA)
		}
		if locked.Checksum != "" && actual.Checksum != locked.Checksum {
			return fmt.Errorf("package set %q package %q checksum drift", name, locked.Name)
		}
		delete(gotPackages, locked.Name)
	}
	if len(gotPackages) > 0 {
		for name := range gotPackages {
			return fmt.Errorf("package set contains unlocked package %q", name)
		}
	}
	return nil
}
