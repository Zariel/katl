package vmtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zariel/katl/internal/installer/generation"
	"github.com/zariel/katl/internal/installer/katlosimage"
	"github.com/zariel/katl/internal/installer/manifest"
)

func TestInstalledRuntimeSysupdateRootUKITransfer(t *testing.T) {
	options := DefaultOptions()
	if !options.Enabled {
		t.Skip("set -katl.vmtest.run or KATL_VMTEST_RUN=1 to run installed runtime sysupdate root+UKI smoke")
	}
	runner := NewRunner(options)
	runtime := InstalledRuntimeConfig{}
	var plannedMAC string
	spec := NodeSpec{Name: "sysupdate-partx-1", Role: ControlPlane}
	if worldRun, ok := installedRuntimeWorldRunFor(t, "installed-runtime-sysupdate-root-uki", spec); ok {
		runner = worldRun.Runner
		runtime = worldRun.Config
		plannedMAC = worldRun.Node.MACAddress
	} else {
		_ = RequireWorld(t)
	}
	scenario := Scenario{Name: "installed-runtime-sysupdate-root-uki"}
	result, err := runner.Plan(scenario)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	result = requirePlannedVMHost(t, runner, scenario, result, HostRequirements{
		Libvirt: true,
		OVMF:    true,
		KVM:     runner.options().KVM,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	vm := runtime.VM
	vm.KVM = runner.options().KVM
	vm.RAMMiB = 4096
	vm.CPUs = 2
	vm.Timeout = 8 * time.Minute
	vm.Network.MAC = first(vm.Network.MAC, plannedMAC)
	vm.VSock.Enabled = true
	vm.Agent.RequireHealth = true
	vm.Agent.Timeout = 30 * time.Second
	node, err := StartInstalledRuntimeNode(ctx, result, InstalledRuntimeNodeConfig{
		Name: spec.Name,
		Runtime: InstalledRuntimeConfig{
			Disk:            runtime.Disk,
			DiskFormat:      runtime.DiskFormat,
			ESPArtifacts:    runtime.ESPArtifacts,
			FixtureManifest: runtime.FixtureManifest,
			NodeMetadata:    runtime.NodeMetadata,
			VM:              vm,
		},
	}, VMRunner{})
	if err != nil {
		t.Fatalf("StartInstalledRuntimeNode() error = %v", err)
	}
	defer func() {
		if err := node.Stop(); err != nil && err != context.Canceled {
			t.Logf("Stop() error = %v", err)
		}
	}()
	client, err := DialAgent(ctx, node.VSock.GuestCID, node.VSock.Port, node.Result.Artifacts.VSockTranscript)
	if err != nil {
		t.Fatalf("DialAgent() error = %v", err)
	}
	defer client.Close()
	guest := NewGuestControl(node.Result, client)
	defer func() {
		if t.Failed() {
			collectSysupdateFailureEvidence(ctx, guest)
		}
	}()

	currentGeneration := currentGenerationFromGuest(t, ctx, guest)
	currentSpec := readGuestFile(t, ctx, guest, "/var/lib/katl/generations/"+currentGeneration+"/spec.json")
	_, inactiveSlot, activeLabel, inactiveLabel := rootSlotsFromSpec(t, currentSpec)
	activeDevice := guestCommandOutput(t, ctx, guest, "active-root-device", "blkid", "-t", "PARTLABEL="+activeLabel, "-o", "device")
	activeDisk, activePart := partitionDiskAndNumber(t, strings.TrimSpace(activeDevice))
	guestCommand(t, ctx, guest, "mark-active-root-installed", "sfdisk", "--part-label", activeDisk, activePart, "katl_"+currentGeneration)
	guestCommand(t, ctx, guest, "refresh-active-root-partition", "partx", "--update", "--nr", activePart, activeDisk)
	inactiveDevice := guestCommandOutput(t, ctx, guest, "inactive-root-device", "blkid", "-t", "PARTLABEL="+inactiveLabel, "-o", "device")
	inactiveDisk, inactivePart := partitionDiskAndNumber(t, strings.TrimSpace(inactiveDevice))
	if inactiveDisk != activeDisk {
		t.Fatalf("active root disk %q differs from inactive root disk %q", activeDisk, inactiveDisk)
	}
	inactivePartUUID := strings.TrimSpace(guestCommandOutput(t, ctx, guest, "inactive-root-partuuid", "blkid", "-s", "PARTUUID", "-o", "value", strings.TrimSpace(inactiveDevice)))
	guestCommand(t, ctx, guest, "mark-inactive-root-empty", "sfdisk", "--part-label", inactiveDisk, inactivePart, "_empty")
	guestCommand(t, ctx, guest, "refresh-inactive-root-partition", "partx", "--update", "--nr", inactivePart, inactiveDisk)
	espDevice := strings.TrimSpace(guestCommandOutput(t, ctx, guest, "esp-device", "blkid", "-t", "PARTLABEL=KATL_ESP", "-o", "device"))
	guestCommand(t, ctx, guest, "mount-esp", "mount", espDevice, "/efi")
	guestCommand(t, ctx, guest, "esp-mounted", "findmnt", "--target", "/efi", "--output", "SOURCE,TARGET,FSTYPE,OPTIONS")

	version := "2026.06.17"
	generationID := "sysupdate-2026.06.17"
	rootBytes := []byte("katl sysupdate root prototype\n")
	ukiBytes := []byte("katl sysupdate uki prototype\n")
	upgradePayload, upgradeImagePath := writeSysupdateUpgradeImagePayload(t, result.RunDir, version, rootBytes, ukiBytes)
	sysupdateFixture := writeSysupdateGuestFixtureFromImage(t, ctx, guest, result.RunDir, version, upgradePayload)
	proof, err := upgradePayload.SingleImageProof(katlosimage.SingleImageProofRequest{
		ImagePath: upgradeImagePath,
		Sysupdate: &katlosimage.SysupdateProof{
			SourcePath:       "/var/lib/katl/test-artifacts/sysupdate/source",
			RootTransferPath: sysupdateFixture.RootTransferPath,
			UKITransferPath:  sysupdateFixture.UKITransferPath,
			RootSourcePath:   sysupdateFixture.RootSourcePath,
			UKISourcePath:    sysupdateFixture.UKISourcePath,
		},
	})
	if err != nil {
		t.Fatalf("single-image upgrade proof: %v", err)
	}
	if err := katlosimage.WriteSingleImageProof(result.Artifacts.SingleImageProof, proof); err != nil {
		t.Fatalf("write single-image upgrade proof: %v", err)
	}

	sysupdate := "/usr/lib/systemd/systemd-sysupdate"
	definitions := "/var/lib/katl/test-artifacts/sysupdate/sysupdate.d"
	guestCommand(t, ctx, guest, "sysupdate-list", sysupdate, "--no-pager", "--verify=no", "--sync=no", "--definitions="+definitions, "list")
	guestCommand(t, ctx, guest, "sysupdate-update", sysupdate, "--no-pager", "--verify=no", "--sync=no", "--definitions="+definitions, "update", version)

	sysupdateRootLabel := "katl_" + version
	candidateRootDevice := strings.TrimSpace(guestCommandOutput(t, ctx, guest, "candidate-root-label", "blkid", "-t", "PARTLABEL="+sysupdateRootLabel, "-o", "device"))
	if candidateRootDevice != strings.TrimSpace(inactiveDevice) {
		t.Fatalf("candidate root device = %q, want inactive device %q", candidateRootDevice, strings.TrimSpace(inactiveDevice))
	}
	gotRootBytes := guestCommandOutput(t, ctx, guest, "candidate-root-payload", "dd", "if="+candidateRootDevice, "bs="+strconv.Itoa(len(rootBytes)), "count=1", "status=none")
	if gotRootBytes != string(rootBytes) {
		t.Fatalf("candidate root payload = %q, want %q", gotRootBytes, string(rootBytes))
	}
	bootCountedUKIPath := "/efi/EFI/Linux/katl_" + version + "+1-0.efi"
	assertGuestExists(t, ctx, guest, bootCountedUKIPath)
	gotUKISHA := strings.Fields(guestCommandOutput(t, ctx, guest, "candidate-uki-sha256", "sha256sum", bootCountedUKIPath))
	if len(gotUKISHA) == 0 || gotUKISHA[0] != sha256Hex(ukiBytes) {
		t.Fatalf("candidate UKI SHA256 fields = %#v, want %s", gotUKISHA, sha256Hex(ukiBytes))
	}

	candidateSpec := candidateGenerationSpec(t, currentSpec, candidateGenerationSpecInput{
		GenerationID:   generationID,
		RuntimeVersion: version,
		Slot:           inactiveSlot,
		PartitionUUID:  inactivePartUUID,
		RootSHA256:     sha256Hex(rootBytes),
		UKIPath:        "/efi/EFI/Linux/katl_" + version + ".efi",
		LoaderEntry:    "loader/entries/katl-" + generationID + ".conf",
		CreatedAt:      time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
	})
	candidateStatus, err := generation.NewGenerationStatus(candidateSpec, generation.CommitStateCandidate, generation.BootStatePending, generation.HealthStateUnknown, candidateSpec.CreatedAt)
	if err != nil {
		t.Fatalf("candidate generation status: %v", err)
	}
	writeGuestJSON(t, ctx, guest, "/var/lib/katl/generations/"+generationID+"/spec.json", candidateSpec)
	writeGuestJSON(t, ctx, guest, "/var/lib/katl/generations/"+generationID+"/status.json", candidateStatus)
	readCandidateSpec := generationFromGuest(t, ctx, guest, generationID)
	if readCandidateSpec.Root.Slot != inactiveSlot || readCandidateSpec.Root.PartitionUUID != inactivePartUUID || readCandidateSpec.Boot.LoaderEntryPath == "" {
		t.Fatalf("candidate spec = %#v, slot=%q partuuid=%q", readCandidateSpec, inactiveSlot, inactivePartUUID)
	}

	selection := bootSelectionFromGuest(t, ctx, guest)
	trial := generation.BootSelectionRecord{
		APIVersion:                    generation.APIVersion,
		Kind:                          generation.BootSelectionKind,
		DefaultGenerationID:           selection.DefaultGenerationID,
		TrialGenerationID:             generationID,
		PreviousKnownGoodGenerationID: selection.DefaultGenerationID,
		DefaultBootEntry:              selection.DefaultBootEntry,
		TrialBootEntry:                "loader/entries/katl-" + generationID + ".conf",
		PreviousKnownGoodBootEntry:    selection.DefaultBootEntry,
		BootCountedTrialPath:          bootCountedUKIPath,
		PendingTransactionID:          "vmtest-sysupdate-root-uki",
		PendingHealthValidation:       true,
		PersistentDefaultPromotion:    generation.DefaultPromotionPending,
		UpdatedAt:                     time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
	}
	writeGuestJSON(t, ctx, guest, "/var/lib/katl/boot/selection.json", trial)
	readTrial := bootSelectionFromGuest(t, ctx, guest)
	if readTrial.TrialGenerationID != generationID || readTrial.BootCountedTrialPath != bootCountedUKIPath || readTrial.PreviousKnownGoodGenerationID != currentGeneration {
		t.Fatalf("trial boot selection = %#v", readTrial)
	}
	t.Logf("sysupdate staging verified; candidate boot promotion and rollback require Katl activation glue beyond this staging smoke")

	node.Result.finish(StatusPassed, "", runner.time())
	if err := runner.Write(scenario, node.Result); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

type sysupdateGuestFixture struct {
	RootPath         string
	UKIPath          string
	RootSourcePath   string
	UKISourcePath    string
	RootTransferPath string
	UKITransferPath  string
}

func writeSysupdateGuestFixtureFromImage(t *testing.T, ctx context.Context, guest *GuestControl, runDir, version string, payload katlosimage.Payload) sysupdateGuestFixture {
	t.Helper()
	rootBytes, err := os.ReadFile(payload.ComponentPath(payload.Runtime))
	if err != nil {
		t.Fatalf("read runtime-root image component: %v", err)
	}
	ukiBytes, err := os.ReadFile(payload.ComponentPath(payload.Boot))
	if err != nil {
		t.Fatalf("read runtime-uki image component: %v", err)
	}
	if got := sha256Hex(rootBytes); got != payload.Runtime.SHA256 {
		t.Fatalf("runtime-root source digest = %s, want image component %s", got, payload.Runtime.SHA256)
	}
	if got := sha256Hex(ukiBytes); got != payload.Boot.SHA256 {
		t.Fatalf("runtime-uki source digest = %s, want image component %s", got, payload.Boot.SHA256)
	}
	guestSource := "/var/lib/katl/test-artifacts/sysupdate/source"
	guestDefinitions := "/var/lib/katl/test-artifacts/sysupdate/sysupdate.d"
	hostSource := filepath.Join(runDir, "sysupdate-source")
	hostDefinitions := filepath.Join(runDir, "sysupdate.d")
	rootName := "katl_" + version + ".root.squashfs"
	ukiName := "katl_" + version + ".efi"
	rootPath := guestSource + "/" + rootName
	ukiPath := guestSource + "/" + ukiName
	hostRootPath := filepath.Join(hostSource, rootName)
	hostUKIPath := filepath.Join(hostSource, ukiName)
	if err := os.MkdirAll(hostSource, 0o755); err != nil {
		t.Fatalf("create host sysupdate source: %v", err)
	}
	if err := os.MkdirAll(hostDefinitions, 0o755); err != nil {
		t.Fatalf("create host sysupdate definitions: %v", err)
	}
	if err := os.WriteFile(hostRootPath, rootBytes, 0o644); err != nil {
		t.Fatalf("write host sysupdate root source: %v", err)
	}
	if err := os.WriteFile(hostUKIPath, ukiBytes, 0o644); err != nil {
		t.Fatalf("write host sysupdate UKI source: %v", err)
	}
	shaSums := fmt.Sprintf("%s  %s\n%s  %s\n", sha256Hex(rootBytes), rootName, sha256Hex(ukiBytes), ukiName)
	if err := os.WriteFile(filepath.Join(hostSource, "SHA256SUMS"), []byte(shaSums), 0o644); err != nil {
		t.Fatalf("write host sysupdate SHA256SUMS: %v", err)
	}
	hostRootTransfer := filepath.Join(hostDefinitions, "50-katl-root.transfer")
	hostUKITransfer := filepath.Join(hostDefinitions, "70-katl-uki.transfer")
	if err := os.WriteFile(hostRootTransfer, []byte(rootTransfer(guestSource)), 0o644); err != nil {
		t.Fatalf("write host root transfer: %v", err)
	}
	if err := os.WriteFile(hostUKITransfer, []byte(ukiTransfer(guestSource)), 0o644); err != nil {
		t.Fatalf("write host UKI transfer: %v", err)
	}
	writeGuestFile(t, ctx, guest, rootPath, rootBytes, 0o644)
	writeGuestFile(t, ctx, guest, ukiPath, ukiBytes, 0o644)
	writeGuestFile(t, ctx, guest, guestSource+"/SHA256SUMS", []byte(shaSums), 0o644)
	writeGuestFile(t, ctx, guest, guestDefinitions+"/50-katl-root.transfer", []byte(rootTransfer(guestSource)), 0o644)
	writeGuestFile(t, ctx, guest, guestDefinitions+"/70-katl-uki.transfer", []byte(ukiTransfer(guestSource)), 0o644)
	return sysupdateGuestFixture{
		RootPath:         rootPath,
		UKIPath:          ukiPath,
		RootSourcePath:   hostRootPath,
		UKISourcePath:    hostUKIPath,
		RootTransferPath: hostRootTransfer,
		UKITransferPath:  hostUKITransfer,
	}
}

func writeSysupdateUpgradeImagePayload(t *testing.T, runDir string, version string, rootBytes, ukiBytes []byte) (katlosimage.Payload, string) {
	t.Helper()
	imageRoot := filepath.Join(runDir, "katlos-upgrade-image")
	components := map[string][]byte{
		"components/runtime/root.squashfs": rootBytes,
		"components/boot/katl.efi":         ukiBytes,
		"components/sysext/kubernetes.raw": []byte("preserved Kubernetes sysext placeholder\n"),
	}
	digests := make(map[string]string, len(components))
	sizes := make(map[string]int64, len(components))
	for rel, data := range components {
		path := filepath.Join(imageRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create upgrade image component dir: %v", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write upgrade image component: %v", err)
		}
		digests[rel] = sha256Hex(data)
		sizes[rel] = int64(len(data))
	}
	index := katlosimage.Index{
		APIVersion:       katlosimage.APIVersion,
		Kind:             katlosimage.Kind,
		ImageRole:        katlosimage.RoleUpgrade,
		Format:           katlosimage.FormatSquashFS,
		Version:          version,
		BuildID:          "vmtest-sysupdate-" + version,
		Architecture:     "x86_64",
		RuntimeInterface: "katl-runtime-1",
		CreatedAt:        "2026-06-17T12:00:00Z",
		Components: []katlosimage.Component{
			{
				Name:         "runtime-root",
				Role:         katlosimage.ComponentRuntimeRoot,
				Path:         "components/runtime/root.squashfs",
				Format:       "squashfs",
				SizeBytes:    sizes["components/runtime/root.squashfs"],
				SHA256:       digests["components/runtime/root.squashfs"],
				Version:      version,
				Architecture: "x86_64",
				Compatibility: katlosimage.Compatibility{
					RuntimeInterface: "katl-runtime-1",
				},
				InstallTarget: katlosimage.InstallTarget{
					Kind:       "root-slot",
					Filesystem: "squashfs",
				},
			},
			{
				Name:         "runtime-uki",
				Role:         katlosimage.ComponentRuntimeUKI,
				Path:         "components/boot/katl.efi",
				Format:       "uki",
				SizeBytes:    sizes["components/boot/katl.efi"],
				SHA256:       digests["components/boot/katl.efi"],
				Version:      version,
				Architecture: "x86_64",
				Compatibility: katlosimage.Compatibility{
					RuntimeInterface: "katl-runtime-1",
					RuntimeRoot: katlosimage.RuntimeRoot{
						Interface:      "katl-runtime-1",
						ArtifactPath:   "components/runtime/root.squashfs",
						ArtifactSHA256: digests["components/runtime/root.squashfs"],
					},
					KernelCommandLine: []string{"quiet"},
				},
				InstallTarget: katlosimage.InstallTarget{
					Kind:     "esp-or-xbootldr",
					Filename: "katl.efi",
				},
			},
			{
				Name:           "kubernetes",
				Role:           katlosimage.ComponentKubernetes,
				Path:           "components/sysext/kubernetes.raw",
				Format:         "raw",
				SizeBytes:      sizes["components/sysext/kubernetes.raw"],
				SHA256:         digests["components/sysext/kubernetes.raw"],
				Version:        "v1.36.1",
				PayloadVersion: "v1.36.1",
				Architecture:   "x86_64",
				Compatibility: katlosimage.Compatibility{
					RuntimeInterface: "katl-runtime-1",
					RuntimeRoot: katlosimage.RuntimeRoot{
						Interface:      "katl-runtime-1",
						ArtifactPath:   "components/runtime/root.squashfs",
						ArtifactSHA256: digests["components/runtime/root.squashfs"],
					},
				},
				InstallTarget: katlosimage.InstallTarget{Kind: "systemd-sysext", Name: "kubernetes.raw"},
			},
		},
	}
	if err := os.MkdirAll(filepath.Join(imageRoot, "katlos"), 0o755); err != nil {
		t.Fatalf("create upgrade image index dir: %v", err)
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal upgrade image index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageRoot, "katlos", "image.json"), append(indexData, '\n'), 0o644); err != nil {
		t.Fatalf("write upgrade image index: %v", err)
	}
	imagePath := filepath.Join(runDir, "katlos-upgrade-"+version+"-x86_64.squashfs")
	if output, err := exec.Command("mksquashfs", imageRoot, imagePath, "-noappend", "-quiet").CombinedOutput(); err != nil {
		t.Fatalf("build upgrade image squashfs: %v\n%s", err, output)
	}
	imageInfo, err := os.Stat(imagePath)
	if err != nil {
		t.Fatalf("stat upgrade image squashfs: %v", err)
	}
	imageSHA, err := fileSHA256(imagePath)
	if err != nil {
		t.Fatalf("hash upgrade image squashfs: %v", err)
	}
	expected := manifest.KatlosImage{
		LocalRef:         filepath.Base(imagePath),
		SHA256:           imageSHA,
		SizeBytes:        uint64(imageInfo.Size()),
		Version:          version,
		Architecture:     "x86_64",
		RuntimeInterface: "katl-runtime-1",
		Role:             katlosimage.RoleUpgrade,
	}
	payload, err := katlosimage.ResolveDirectory(context.Background(), imageRoot, expected)
	if err != nil {
		t.Fatalf("resolve upgrade image payload: %v", err)
	}
	return payload, imagePath
}

func rootTransfer(source string) string {
	return fmt.Sprintf(`[Transfer]
ProtectVersion=0

[Source]
Type=regular-file
Path=%s
MatchPattern=katl_@v.root.squashfs

[Target]
Type=partition
Path=auto
MatchPattern=katl_@v
MatchPartitionType=root
ReadOnly=1
InstancesMax=2
`, source)
}

func ukiTransfer(source string) string {
	return fmt.Sprintf(`[Transfer]
ProtectVersion=0

[Source]
Type=regular-file
Path=%s
MatchPattern=katl_@v.efi

[Target]
Type=regular-file
Path=/EFI/Linux
PathRelativeTo=boot
MatchPattern=katl_@v+@l-@d.efi katl_@v+@l.efi katl_@v.efi
Mode=0644
TriesLeft=1
TriesDone=0
InstancesMax=2
`, source)
}

func rootSlotsFromSpec(t *testing.T, spec string) (string, string, string, string) {
	t.Helper()
	switch {
	case strings.Contains(spec, `"slot": "root-a"`):
		return "root-a", "root-b", "KATL_ROOT_A", "KATL_ROOT_B"
	case strings.Contains(spec, `"slot": "root-b"`):
		return "root-b", "root-a", "KATL_ROOT_B", "KATL_ROOT_A"
	default:
		t.Fatalf("current generation spec does not identify root-a/root-b slot:\n%s", spec)
		return "", "", "", ""
	}
}

type candidateGenerationSpecInput struct {
	GenerationID   string
	RuntimeVersion string
	Slot           string
	PartitionUUID  string
	RootSHA256     string
	UKIPath        string
	LoaderEntry    string
	CreatedAt      time.Time
}

func candidateGenerationSpec(t *testing.T, currentSpec string, input candidateGenerationSpecInput) generation.GenerationSpec {
	t.Helper()
	var current generation.GenerationSpec
	if err := json.Unmarshal([]byte(currentSpec), &current); err != nil {
		t.Fatalf("decode current generation spec: %v\n%s", err, currentSpec)
	}
	spec := current
	spec.GenerationID = input.GenerationID
	spec.RuntimeVersion = input.RuntimeVersion
	spec.PreviousGenerationID = current.GenerationID
	spec.Root.Slot = input.Slot
	spec.Root.PartitionUUID = input.PartitionUUID
	spec.Root.RuntimeVersion = input.RuntimeVersion
	spec.Root.RuntimeArtifactSHA256 = input.RootSHA256
	spec.Boot.UKIPath = input.UKIPath
	spec.Boot.LoaderEntryPath = input.LoaderEntry
	spec.CreatedAt = input.CreatedAt
	if err := generation.ValidateGenerationSpec(spec); err != nil {
		t.Fatalf("candidate generation spec invalid: %v\n%#v", err, spec)
	}
	return spec
}

func partitionDiskAndNumber(t *testing.T, device string) (string, string) {
	t.Helper()
	if device == "" {
		t.Fatal("partition device is empty")
	}
	lastDigit := len(device) - 1
	for lastDigit >= 0 && device[lastDigit] >= '0' && device[lastDigit] <= '9' {
		lastDigit--
	}
	if lastDigit == len(device)-1 {
		t.Fatalf("partition device %q has no numeric suffix", device)
	}
	disk := device[:lastDigit+1]
	part := device[lastDigit+1:]
	if strings.HasSuffix(disk, "p") {
		disk = strings.TrimSuffix(disk, "p")
	}
	if disk == "" || part == "" || disk == device {
		t.Fatalf("could not split partition device %q", device)
	}
	return disk, part
}

func bootSelectionFromGuest(t *testing.T, ctx context.Context, guest *GuestControl) generation.BootSelectionRecord {
	t.Helper()
	data := readGuestFile(t, ctx, guest, "/var/lib/katl/boot/selection.json")
	var selection generation.BootSelectionRecord
	if err := json.Unmarshal([]byte(data), &selection); err != nil {
		t.Fatalf("decode boot selection: %v\n%s", err, data)
	}
	if err := generation.ValidateBootSelection(selection); err != nil {
		t.Fatalf("guest boot selection is invalid: %v\n%s", err, data)
	}
	return selection
}

func generationFromGuest(t *testing.T, ctx context.Context, guest *GuestControl, generationID string) generation.GenerationSpec {
	t.Helper()
	specData := readGuestFile(t, ctx, guest, "/var/lib/katl/generations/"+generationID+"/spec.json")
	statusData := readGuestFile(t, ctx, guest, "/var/lib/katl/generations/"+generationID+"/status.json")
	var spec generation.GenerationSpec
	if err := json.Unmarshal([]byte(specData), &spec); err != nil {
		t.Fatalf("decode candidate generation spec: %v\n%s", err, specData)
	}
	var status generation.GenerationStatus
	if err := json.Unmarshal([]byte(statusData), &status); err != nil {
		t.Fatalf("decode candidate generation status: %v\n%s", err, statusData)
	}
	if err := generation.ValidateGenerationStatus(spec, status); err != nil {
		t.Fatalf("candidate generation invalid: %v\nspec=%s\nstatus=%s", err, specData, statusData)
	}
	return spec
}

func writeGuestJSON(t *testing.T, ctx context.Context, guest *GuestControl, path string, value any) {
	t.Helper()
	data, err := generation.MarshalCanonicalJSON(value)
	if err != nil {
		t.Fatalf("marshal guest JSON %s: %v", path, err)
	}
	writeGuestFile(t, ctx, guest, path, data, 0o644)
}

func writeGuestFile(t *testing.T, ctx context.Context, guest *GuestControl, path string, content []byte, mode fs.FileMode) {
	t.Helper()
	if _, err := guest.WriteFile(ctx, GuestFileRequest{
		Name:    filepath.Base(path),
		Path:    path,
		Content: content,
		Mode:    mode,
	}); err != nil {
		t.Fatalf("write guest file %s: %v", path, err)
	}
}

func guestCommand(t *testing.T, ctx context.Context, guest *GuestControl, name string, argv ...string) GuestCommandArtifact {
	t.Helper()
	record, err := guest.RunCommand(ctx, GuestCommandRequest{
		Name:         name,
		Argv:         argv,
		Timeout:      2 * time.Minute,
		StdoutLimit:  1 << 20,
		StderrLimit:  1 << 20,
		AllowFailure: false,
	})
	if err != nil {
		t.Fatalf("%s failed: %v\nstdout:\n%s\nstderr:\n%s", name, err, readOptionalFile(t, record.Stdout), readOptionalFile(t, record.Stderr))
	}
	return record
}

func guestCommandOutput(t *testing.T, ctx context.Context, guest *GuestControl, name string, argv ...string) string {
	t.Helper()
	record := guestCommand(t, ctx, guest, name, argv...)
	return readFile(t, record.Stdout)
}

func collectSysupdateFailureEvidence(ctx context.Context, guest *GuestControl) {
	for _, req := range []GuestCommandRequest{
		{Name: "sysupdate-status", Argv: []string{"/usr/lib/systemd/systemd-sysupdate", "--no-pager", "--verify=no", "--definitions=/var/lib/katl/test-artifacts/sysupdate/sysupdate.d", "list"}, AllowFailure: true},
		{Name: "root-labels", Argv: []string{"blkid", "-o", "export"}, AllowFailure: true, StdoutLimit: 1 << 20},
		{Name: "sysupdate-files", Argv: []string{"find", "/var/lib/katl/test-artifacts/sysupdate", "-maxdepth", "4", "-type", "f", "-print"}, AllowFailure: true},
		{Name: "efi-linux-files", Argv: []string{"find", "/efi/EFI/Linux", "-maxdepth", "1", "-type", "f", "-print"}, AllowFailure: true},
	} {
		_, _ = guest.RunCommand(ctx, req)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		return ""
	}
	return readFile(t, path)
}
