package scriptstest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildVMTestArtifactsBaseStampHitSkipsMkosi(t *testing.T) {
	repo := repoRoot(t)
	fixture := newVMTestArtifactFixture(t, repo)

	runBuildVMTestArtifacts(t, repo, fixture.env()...)
	runBuildVMTestArtifacts(t, repo, fixture.env()...)

	if got := readLinesForScripts(t, fixture.mkosiArgs); len(got) != 1 || got[0] != "--profile runtime-base -f build" {
		t.Fatalf("mkosi args = %#v, want one runtime-base build", got)
	}
	if got := readLinesForScripts(t, fixture.runtimeBuildArgs); len(got) != 2 {
		t.Fatalf("runtime build runs = %#v, want 2", got)
	}
	if got := readLinesForScripts(t, fixture.mksquashfsArgs); len(got) != 2 {
		t.Fatalf("mksquashfs runs = %#v, want 2", got)
	}
}

func TestBuildVMTestArtifactsBaseStampMissRebuildsMkosi(t *testing.T) {
	repo := repoRoot(t)
	fixture := newVMTestArtifactFixture(t, repo)

	runBuildVMTestArtifacts(t, repo, fixture.env()...)
	env := append(fixture.env(), "KATL_MKOSI_IMAGE=changed-builder")
	runBuildVMTestArtifacts(t, repo, env...)

	if got := readLinesForScripts(t, fixture.mkosiArgs); len(got) != 2 {
		t.Fatalf("mkosi args = %#v, want two runtime-base builds", got)
	}
}

func TestBuildVMTestArtifactsRuntimeChangeRebuildsOverlayOnly(t *testing.T) {
	repo := repoRoot(t)
	fixture := newVMTestArtifactFixture(t, repo)

	runBuildVMTestArtifacts(t, repo, fixture.env()...)
	if err := os.WriteFile(fixture.runtimeBuild, []byte(fakeRuntimeBuildScript("changed runtime marker")), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", fixture.runtimeBuild, err)
	}
	runBuildVMTestArtifacts(t, repo, fixture.env()...)

	if got := readLinesForScripts(t, fixture.mkosiArgs); len(got) != 1 {
		t.Fatalf("mkosi args = %#v, want one runtime-base build", got)
	}
	if got := readLinesForScripts(t, fixture.runtimeBuildArgs); len(got) != 2 {
		t.Fatalf("runtime build runs = %#v, want 2", got)
	}
	if got := readLinesForScripts(t, fixture.mksquashfsArgs); len(got) != 2 {
		t.Fatalf("mksquashfs runs = %#v, want 2", got)
	}
}

func TestBuildVMTestArtifactsMissingOverlayfsFailsClearly(t *testing.T) {
	repo := repoRoot(t)
	fixture := newVMTestArtifactFixture(t, repo)
	noOverlay := filepath.Join(fixture.workDir, "filesystems.no-overlay")
	if err := os.WriteFile(noOverlay, []byte("nodev\ttmpfs\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", noOverlay, err)
	}

	cmd := exec.Command(filepath.Join(repo, "scripts", "build-vmtest-artifacts"), "--runtime-only")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), append(fixture.env(), "KATL_VMTEST_ARTIFACTS_FILESYSTEMS="+noOverlay)...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("build-vmtest-artifacts unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(string(output), "overlayfs is required for vmtest artifact prep") {
		t.Fatalf("output missing overlayfs failure:\n%s", output)
	}
	if _, err := os.Stat(fixture.mkosiArgs); !os.IsNotExist(err) {
		t.Fatalf("mkosi ran despite missing overlayfs, stat err = %v", err)
	}
}

type vmtestArtifactFixture struct {
	workDir          string
	buildDir         string
	binDir           string
	mkosi            string
	mkosiArgs        string
	runtimeBuild     string
	runtimeBuildArgs string
	mksquashfsArgs   string
	tool             string
	filesystems      string
}

func newVMTestArtifactFixture(t *testing.T, repo string) vmtestArtifactFixture {
	t.Helper()
	workDir := testBuildDir(t, repo, "vmtest artifacts ")
	binDir := filepath.Join(workDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", binDir, err)
	}

	fixture := vmtestArtifactFixture{
		workDir:          workDir,
		buildDir:         filepath.Join(workDir, "mkosi"),
		binDir:           binDir,
		mkosi:            filepath.Join(workDir, "fake-mkosi"),
		mkosiArgs:        filepath.Join(workDir, "mkosi-args.txt"),
		runtimeBuild:     filepath.Join(workDir, "runtime-build"),
		runtimeBuildArgs: filepath.Join(workDir, "runtime-build-args.txt"),
		mksquashfsArgs:   filepath.Join(workDir, "mksquashfs-args.txt"),
		tool:             filepath.Join(workDir, "katl-mkosi-artifacts"),
		filesystems:      filepath.Join(workDir, "filesystems"),
	}
	if err := os.MkdirAll(fixture.buildDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", fixture.buildDir, err)
	}
	if err := os.WriteFile(fixture.filesystems, []byte("nodev\toverlay\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", fixture.filesystems, err)
	}

	writeFakeExecutable(t, workDir, "fake-mkosi", `printf '%s\n' "$*" >> "$KATL_FAKE_MKOSI_ARGS"
mkdir -p "$KATL_FAKE_BUILD_DIR/katl-runtime-base-root/boot/fedora/6.12.0" "$KATL_FAKE_BUILD_DIR/katl-runtime-base-root/usr/lib"
printf kernel > "$KATL_FAKE_BUILD_DIR/katl-runtime-base-root/boot/fedora/6.12.0/linux"
printf initrd > "$KATL_FAKE_BUILD_DIR/katl-runtime-base-root/boot/fedora/6.12.0/initrd"
printf 'ID=katl\n' > "$KATL_FAKE_BUILD_DIR/katl-runtime-base-root/usr/lib/os-release"
`)
	if err := os.WriteFile(fixture.runtimeBuild, []byte(fakeRuntimeBuildScript("runtime marker")), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", fixture.runtimeBuild, err)
	}
	writeFakeExecutable(t, binDir, "mount", `target="${@: -1}"
opts=""
while (($# > 0)); do
  if [[ "$1" == "-o" ]]; then
    opts="$2"
    break
  fi
  shift
done
lower="${opts#lowerdir=}"
lower="${lower%%,*}"
upper="${opts#*upperdir=}"
upper="${upper%%,*}"
mkdir -p "$target"
cp -a "$lower/." "$target/"
cp -a "$upper/." "$target/"
`)
	writeFakeExecutable(t, binDir, "umount", "exit 0\n")
	writeFakeExecutable(t, binDir, "mksquashfs", `printf '%s -> %s\n' "$1" "$2" >> "$KATL_FAKE_MKSQUASHFS_ARGS"
printf squashfs > "$2"
`)
	writeFakeExecutable(t, binDir, "ukify", `out=""
while (($# > 0)); do
  if [[ "$1" == "--output" ]]; then
    out="$2"
    break
  fi
  shift
done
printf uki > "$out"
`)
	writeFakeExecutable(t, workDir, "katl-mkosi-artifacts", `case "$1" in
  write-runtime-root)
    artifact=""
    while (($# > 0)); do
      if [[ "$1" == "--artifact" ]]; then artifact="$2"; fi
      shift
    done
    printf 'runtime-sha\n'
    printf '{}' > "$artifact.json"
    printf 'runtime-sha  %s\n' "$(basename "$artifact")" > "$artifact.sha256"
    ;;
  write-runtime-uki)
    artifact=""
    while (($# > 0)); do
      if [[ "$1" == "--artifact" ]]; then artifact="$2"; fi
      shift
    done
    printf '{}' > "$artifact.json"
    printf 'uki-sha  %s\n' "$(basename "$artifact")" > "$artifact.sha256"
    ;;
  *)
    echo "unexpected katl-mkosi-artifacts command: $*" >&2
    exit 1
    ;;
esac
`)
	return fixture
}

func fakeRuntimeBuildScript(marker string) string {
	return "#!/usr/bin/env bash\nset -euo pipefail\n" +
		"printf '%s\\n' \"$DESTDIR\" >> \"$KATL_FAKE_RUNTIME_BUILD_ARGS\"\n" +
		"mkdir -p \"$DESTDIR/usr/bin\"\n" +
		"printf '" + marker + "' > \"$DESTDIR/usr/bin/katlc\"\n"
}

func (fixture vmtestArtifactFixture) env() []string {
	return []string{
		"PATH=" + fixture.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"KATL_VMTEST_ARTIFACTS_BUILD_DIR=" + fixture.buildDir,
		"KATL_VMTEST_ARTIFACTS_MKOSI=" + fixture.mkosi,
		"KATL_VMTEST_ARTIFACTS_RUNTIME_BUILD=" + fixture.runtimeBuild,
		"KATL_VMTEST_ARTIFACTS_TOOL=" + fixture.tool,
		"KATL_VMTEST_ARTIFACTS_FILESYSTEMS=" + fixture.filesystems,
		"KATL_FAKE_BUILD_DIR=" + fixture.buildDir,
		"KATL_FAKE_MKOSI_ARGS=" + fixture.mkosiArgs,
		"KATL_FAKE_RUNTIME_BUILD_ARGS=" + fixture.runtimeBuildArgs,
		"KATL_FAKE_MKSQUASHFS_ARGS=" + fixture.mksquashfsArgs,
	}
}

func runBuildVMTestArtifacts(t *testing.T, repo string, env ...string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repo, "scripts", "build-vmtest-artifacts"), "--runtime-only")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), env...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-vmtest-artifacts failed: %v\n%s", err, output)
	}
}
