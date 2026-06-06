package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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
		return fmt.Errorf("command is required: refresh or verify")
	}
	switch args[0] {
	case "refresh":
		return runRefresh(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
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
