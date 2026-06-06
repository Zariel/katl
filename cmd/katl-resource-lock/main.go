package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zariel/katl/internal/resourcetest"
)

const defaultLockPath = "mkosi.profiles/resource-package-lock.json"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "katl-resource-lock: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required: add-rpm-package-set, refresh, or verify")
	}
	switch args[0] {
	case "add-rpm-package-set":
		return runAddRPMPackageSet(args[1:], stdout, stderr)
	case "refresh":
		return runRefresh(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func runAddRPMPackageSet(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("katl-resource-lock add-rpm-package-set", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "resource-test manifest to update")
	outputPath := flags.String("output", "", "updated manifest output path, default is --manifest")
	name := flags.String("name", "", "package set name")
	source := flags.String("source", "", "package set source, usually the mkosi profile path")
	root := flags.String("root", "", "mkosi root directory containing an RPM database")
	lockPath := flags.String("lock", "", "optional package lock whose digest should be recorded")
	distribution := flags.String("distribution", "", "package distribution name")
	release := flags.String("release", "", "package distribution release")
	architecture := flags.String("architecture", "", "package architecture")
	profileName := flags.String("profile-name", "", "optional mkosi profile name to add or update")
	profilePath := flags.String("profile-path", "", "optional mkosi profile path to add or update")
	profileConfigDigest := flags.String("profile-config-sha256", "", "optional mkosi profile config SHA-256")
	var repositories repositoryFlags
	flags.Var(&repositories, "repository", "package repository in id=baseURL form; may be repeated")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("--manifest is required")
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(*root) == "" {
		return fmt.Errorf("--root is required")
	}
	if *outputPath == "" {
		*outputPath = *manifestPath
	}

	manifest, err := readManifestForUpdate(*manifestPath)
	if err != nil {
		return err
	}
	packages, err := queryRPMPackages(*root)
	if err != nil {
		return err
	}
	lockDigest := ""
	if *lockPath != "" {
		lockData, err := os.ReadFile(*lockPath)
		if err != nil {
			return fmt.Errorf("read package lock %s: %w", *lockPath, err)
		}
		lockDigest = resourcetest.PackageLockDigest(lockData)
	}
	manifest.PackageSets = upsertPackageSet(manifest.PackageSets, resourcetest.PackageSet{
		Name:         *name,
		Source:       *source,
		LockDigest:   lockDigest,
		Distribution: *distribution,
		Release:      *release,
		Architecture: *architecture,
		Repositories: repositories.Repositories(),
		Packages:     packages,
	})
	if *profileName != "" || *profilePath != "" {
		if *profileName == "" || *profilePath == "" {
			return fmt.Errorf("--profile-name and --profile-path must be set together")
		}
		manifest.MkosiProfiles = upsertMkosiProfile(manifest.MkosiProfiles, resourcetest.MkosiProfile{
			Name:          *profileName,
			Path:          *profilePath,
			ConfigDigest:  *profileConfigDigest,
			PackageSetRef: *name,
		})
	}
	if err := resourcetest.ValidateManifest(manifest); err != nil {
		return err
	}
	if err := writeManifest(*outputPath, manifest); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "manifest: %s\n", *outputPath)
	fmt.Fprintf(stdout, "packageSet: %s\n", *name)
	fmt.Fprintf(stdout, "packages: %d\n", len(packages))
	if lockDigest != "" {
		fmt.Fprintf(stdout, "lockSHA256: %s\n", lockDigest)
	}
	return nil
}

func runRefresh(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("katl-resource-lock refresh", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "resource-test manifest to convert into a package lock")
	outputPath := flags.String("output", defaultLockPath, "package-lock output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("--manifest is required")
	}
	if strings.TrimSpace(*outputPath) == "" {
		return fmt.Errorf("--output is required")
	}

	manifest, err := readManifest(*manifestPath)
	if err != nil {
		return err
	}
	lock, err := resourcetest.PackageLockFromManifest(manifest)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal package lock: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		return fmt.Errorf("create package-lock directory: %w", err)
	}
	if err := os.WriteFile(*outputPath, data, 0o644); err != nil {
		return fmt.Errorf("write package lock %s: %w", *outputPath, err)
	}
	fmt.Fprintf(stdout, "lock: %s\n", *outputPath)
	fmt.Fprintf(stdout, "sha256: %s\n", resourcetest.PackageLockDigest(data))
	return nil
}

func runVerify(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("katl-resource-lock verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "resource-test manifest to verify")
	lockPath := flags.String("lock", defaultLockPath, "package lock path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("--manifest is required")
	}
	if strings.TrimSpace(*lockPath) == "" {
		return fmt.Errorf("--lock is required")
	}

	manifest, err := readManifest(*manifestPath)
	if err != nil {
		return err
	}
	lockData, err := os.ReadFile(*lockPath)
	if err != nil {
		return fmt.Errorf("read package lock %s: %w", *lockPath, err)
	}
	lock, err := resourcetest.DecodePackageLock(strings.NewReader(string(lockData)))
	if err != nil {
		return fmt.Errorf("decode package lock %s: %w", *lockPath, err)
	}
	digest := resourcetest.PackageLockDigest(lockData)
	if err := resourcetest.VerifyPackageLock(resourcetest.PackageLockVerification{
		Lock:       lock,
		Manifest:   manifest,
		LockDigest: digest,
	}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "verified: %s\n", *lockPath)
	fmt.Fprintf(stdout, "sha256: %s\n", digest)
	return nil
}

type repositoryFlags []resourcetest.PackageRepository

func (f *repositoryFlags) String() string {
	var values []string
	for _, repo := range *f {
		values = append(values, repo.ID+"="+repo.BaseURL)
	}
	return strings.Join(values, ",")
}

func (f *repositoryFlags) Set(value string) error {
	id, baseURL, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("repository must use id=baseURL form")
	}
	*f = append(*f, resourcetest.PackageRepository{ID: strings.TrimSpace(id), BaseURL: strings.TrimSpace(baseURL)})
	return nil
}

func (f repositoryFlags) Repositories() []resourcetest.PackageRepository {
	return append([]resourcetest.PackageRepository(nil), f...)
}

func upsertPackageSet(sets []resourcetest.PackageSet, set resourcetest.PackageSet) []resourcetest.PackageSet {
	for i := range sets {
		if sets[i].Name == set.Name {
			sets[i] = set
			return sets
		}
	}
	return append(sets, set)
}

func upsertMkosiProfile(profiles []resourcetest.MkosiProfile, profile resourcetest.MkosiProfile) []resourcetest.MkosiProfile {
	for i := range profiles {
		if profiles[i].Name == profile.Name {
			profiles[i] = profile
			return profiles
		}
	}
	return append(profiles, profile)
}

func writeManifest(path string, manifest resourcetest.Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal resource manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write resource manifest %s: %w", path, err)
	}
	return nil
}

var queryRPMPackages = queryRootRPMPackages

func queryRootRPMPackages(root string) ([]resourcetest.Package, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve rpm root %s: %w", root, err)
	}
	output, err := exec.Command("rpm", "--root", absoluteRoot, "--dbpath", "/usr/lib/sysimage/rpm", "-qa", "--queryformat", "%{NAME}\t%{EPOCHNUM}:%{VERSION}-%{RELEASE}.%{ARCH}\n").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("query rpm packages under %s: %w: %s", root, err, strings.TrimSpace(string(output)))
	}
	packages, err := resourcetest.ParseRPMPackages(bytes.NewReader(output))
	if err != nil {
		return nil, fmt.Errorf("parse rpm packages under %s: %w", root, err)
	}
	return packages, nil
}

func readManifest(path string) (resourcetest.Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return resourcetest.Manifest{}, fmt.Errorf("read resource manifest %s: %w", path, err)
	}
	defer file.Close()
	manifest, err := resourcetest.DecodeManifest(file)
	if err != nil {
		return resourcetest.Manifest{}, fmt.Errorf("decode resource manifest %s: %w", path, err)
	}
	return manifest, nil
}

func readManifestForUpdate(path string) (resourcetest.Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return resourcetest.Manifest{}, fmt.Errorf("read resource manifest %s: %w", path, err)
	}
	defer file.Close()
	var manifest resourcetest.Manifest
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return resourcetest.Manifest{}, fmt.Errorf("decode resource manifest %s: %w", path, err)
	}
	return manifest, nil
}
