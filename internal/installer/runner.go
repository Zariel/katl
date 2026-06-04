package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zariel/katl/internal/installer/generation"
	"github.com/zariel/katl/internal/installer/kubeadmconfig"
	"github.com/zariel/katl/internal/installer/manifest"
	installstatus "github.com/zariel/katl/internal/installer/status"
)

type StepID string

const (
	DiscoverInstallerInput StepID = "DiscoverInstallerInput"
	WaitForLocalConfig     StepID = "WaitForLocalConfig"
	LoadManifest           StepID = "LoadManifest"
	SelectNode             StepID = "SelectNode"
	CollectHardwareFacts   StepID = "CollectHardwareFacts"
	VerifyTrust            StepID = "VerifyTrust"
	PlanInstall            StepID = "PlanInstall"
	PrepareDisk            StepID = "PrepareDisk"
	CreatePartitions       StepID = "CreatePartitions"
	FormatFilesystems      StepID = "FormatFilesystems"
	MountTarget            StepID = "MountTarget"
	InstallRootSlot        StepID = "InstallRootSlot"
	InstallBootArtifacts   StepID = "InstallBootArtifacts"
	InstallExtensions      StepID = "InstallExtensions"
	InstallSeed            StepID = "InstallSeed"
	InstallMountUnits      StepID = "InstallMountUnits"
	WriteInstallRecord     StepID = "WriteInstallRecord"
	VerifyTarget           StepID = "VerifyTarget"
	Reboot                 StepID = "Reboot"
)

type Context struct {
	ManifestPath   string
	StateDir       string
	TargetRoot     string
	BootRoot       string
	Commands       CommandRunner
	Store          StateStore
	Manifest       manifest.Manifest
	LoaderRecord   *generation.Record
	KubeadmConfigs map[string]kubeadmconfig.Plan
	IdentityRandom io.Reader
	Completed      []StepID
	Chown          func(path string, uid int, gid int) error
	InputMode      string
	InputSource    string
	RequestDigest  string
	PreviousStatus *installstatus.Record
}

type Step interface {
	ID() StepID
	Run(context.Context, *Context) error
}

var ErrInstallRefused = errors.New("install refused")

type Plan []Step

func DefaultPlan() Plan {
	return NewPlan(PlanOptions{})
}

type PlanOptions struct {
	PreseededManifest bool
}

func NewPlan(options PlanOptions) Plan {
	plan := Plan{
		stubStep{id: DiscoverInstallerInput},
	}

	if !options.PreseededManifest {
		plan = append(plan, stubStep{id: WaitForLocalConfig})
	}

	plan = append(plan,
		loadManifestStep{},
		stubStep{id: SelectNode},
		stubStep{id: CollectHardwareFacts},
		stubStep{id: VerifyTrust},
		stubStep{id: PlanInstall},
		stubStep{id: PrepareDisk},
		stubStep{id: CreatePartitions},
		stubStep{id: FormatFilesystems},
		stubStep{id: MountTarget},
		stubStep{id: InstallRootSlot},
		stubStep{id: InstallBootArtifacts},
		stubStep{id: InstallExtensions},
		installSeedStep{},
		stubStep{id: InstallMountUnits},
		writeInstallRecordStep{},
		stubStep{id: VerifyTarget},
		stubStep{id: Reboot},
	)

	return plan
}

func PreseededManifestPlan() Plan {
	return NewPlan(PlanOptions{PreseededManifest: true})
}

func (p Plan) IDs() []StepID {
	ids := make([]StepID, 0, len(p))
	for _, step := range p {
		ids = append(ids, step.ID())
	}
	return ids
}

type Runner struct {
	plan Plan
	ctx  *Context
}

func NewRunner(plan Plan, ctx *Context) Runner {
	return Runner{plan: plan, ctx: ctx}
}

func (r Runner) Run(ctx context.Context) error {
	if r.ctx == nil {
		return fmt.Errorf("installer context is required")
	}
	if r.ctx.Commands == nil {
		return fmt.Errorf("command runner is required")
	}
	if r.ctx.Store == nil {
		return fmt.Errorf("state store is required")
	}
	if err := loadPreviousStatus(ctx, r.ctx); err != nil {
		return err
	}

	for _, step := range r.plan {
		if err := step.Run(ctx, r.ctx); err != nil {
			if statusErr := recordFailure(ctx, r.ctx, step.ID(), err); statusErr != nil {
				return fmt.Errorf("%s: %w", step.ID(), errors.Join(err, fmt.Errorf("record failure status: %w", statusErr)))
			}
			return fmt.Errorf("%s: %w", step.ID(), err)
		}
	}

	return nil
}

type loadManifestStep struct{}

func (loadManifestStep) ID() StepID {
	return LoadManifest
}

func (loadManifestStep) Run(ctx context.Context, install *Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if install.ManifestPath == "" {
		return fmt.Errorf("manifest path is required")
	}
	data, err := os.ReadFile(install.ManifestPath)
	if err != nil {
		return fmt.Errorf("open manifest: %w", err)
	}
	install.RequestDigest = installstatus.Digest(data)
	if install.InputSource == "" {
		install.InputSource = install.ManifestPath
	}
	decoded, err := manifest.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	install.Manifest = decoded
	digest, err := installstatus.DigestManifest(decoded)
	if err != nil {
		return err
	}
	install.RequestDigest = digest
	if err := refuseChangedInterruptedRequest(install); err != nil {
		return err
	}
	return recordStep(ctx, install, LoadManifest)
}

type installSeedStep struct{}

func (installSeedStep) ID() StepID {
	return InstallSeed
}

func (installSeedStep) Run(ctx context.Context, install *Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if install.TargetRoot == "" {
		return fmt.Errorf("target root is required")
	}
	request := generation.IdentityRequest{
		AuthorizedKeys: install.Manifest.Node.Identity.SSH.AuthorizedKeys,
		Random:         install.IdentityRandom,
	}
	if install.LoaderRecord != nil {
		bootRoot := install.BootRoot
		if bootRoot == "" {
			bootRoot = filepath.Join(install.TargetRoot, "efi")
		}
		if _, err := generation.WriteInstallIdentity(generation.InstallIdentityRequest{
			TargetRoot: install.TargetRoot,
			BootRoot:   bootRoot,
			Identity:   request,
			Loader:     generation.LoaderRequest{Record: *install.LoaderRecord},
		}); err != nil {
			return err
		}
	} else if _, err := generation.WriteIdentity(install.TargetRoot, request); err != nil {
		return err
	}
	return recordStep(ctx, install, InstallSeed)
}

type writeInstallRecordStep struct{}

func (writeInstallRecordStep) ID() StepID {
	return WriteInstallRecord
}

func (writeInstallRecordStep) Run(ctx context.Context, install *Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if install.LoaderRecord == nil {
		return fmt.Errorf("loader generation record is required to materialize generated confext")
	}
	result, err := MaterializeInstallRecord(InstallRecordRequest{
		TargetRoot:     install.TargetRoot,
		Manifest:       install.Manifest,
		KubeadmConfigs: install.KubeadmConfigs,
		Record:         *install.LoaderRecord,
		Chown:          install.Chown,
	})
	if err != nil {
		return err
	}
	install.LoaderRecord = &result.Record
	return recordStep(ctx, install, WriteInstallRecord)
}

type stubStep struct {
	id StepID
}

func (s stubStep) ID() StepID {
	return s.id
}

func (s stubStep) Run(ctx context.Context, install *Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return recordStep(ctx, install, s.id)
}

func recordStep(ctx context.Context, install *Context, id StepID) error {
	install.Completed = append(install.Completed, id)
	if err := install.Store.SaveCheckpoint(ctx, Checkpoint{
		CurrentStep:    id,
		CompletedSteps: append([]StepID(nil), install.Completed...),
	}); err != nil {
		return err
	}
	record := statusFromContext(install, statusForStep(id), id, nil)
	if err := install.Store.SaveStatus(ctx, record); err != nil {
		return err
	}
	return writeTargetStatus(ctx, install, id, record)
}

func recordFailure(ctx context.Context, install *Context, id StepID, err error) error {
	record := statusFromContext(install, failureState(install, id, err), id, err)
	if saveErr := install.Store.SaveStatus(ctx, record); saveErr != nil {
		return saveErr
	}
	return writeTargetStatus(ctx, install, id, record)
}

func statusFromContext(install *Context, state string, current StepID, err error) installstatus.Record {
	record := installstatus.New(state, timeNow())
	record.CurrentStep = string(current)
	record.CompletedSteps = stepStrings(install.Completed)
	record.InputMode = install.InputMode
	record.InputSource = installstatus.RedactSource(install.InputSource)
	record.RequestDigest = install.RequestDigest
	record.KatlosImage = installstatus.ImageFromManifest(install.Manifest)
	record.TargetDiskStableID = targetDiskStableID(install.Manifest.Install.TargetDisk)
	if install.LoaderRecord != nil {
		record.SelectedRootSlot = install.LoaderRecord.Root.Slot
		record.InstalledGeneration = install.LoaderRecord.GenerationID
		record.BootArtifactVersion = install.LoaderRecord.Boot.UKIPath
	}
	if err != nil {
		record.LastError = installstatus.RedactError(err)
		record.RefusalReason = record.LastError
		if state == installstatus.StateFailedBeforeMutation || state == installstatus.StateInstallRefused {
			record.RetryHint = "fix input or environment and rerun before disk mutation"
		} else {
			record.RetryHint = "inspect target state before rerun or repair"
			record.DestructiveMutation = true
		}
	}
	return record
}

func statusForStep(id StepID) string {
	if id == WaitForLocalConfig {
		return installstatus.StateWaitingForConfig
	}
	if id == Reboot {
		return installstatus.StateRebootRequested
	}
	return installstatus.StateRunning
}

func failureState(install *Context, id StepID, err error) string {
	if errors.Is(err, ErrInstallRefused) {
		return installstatus.StateInstallRefused
	}
	if !mutationStarted(install.Completed, id) {
		return installstatus.StateFailedBeforeMutation
	}
	return installstatus.StateFailedAfterMutation
}

func mutationStarted(completed []StepID, current StepID) bool {
	if current == PrepareDisk {
		return true
	}
	for _, step := range completed {
		if step == PrepareDisk || step == CreatePartitions || step == FormatFilesystems || step == MountTarget || step == InstallRootSlot {
			return true
		}
	}
	return false
}

func writeTargetStatus(ctx context.Context, install *Context, current StepID, record installstatus.Record) error {
	if !targetStatusReady(install, current) {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	path, err := installstatus.RuntimeStatusPath(install.TargetRoot)
	if err != nil {
		return err
	}
	return installstatus.WriteFile(path, record)
}

func targetStatusReady(install *Context, current StepID) bool {
	if install == nil || install.TargetRoot == "" {
		return false
	}
	if current == MountTarget {
		return true
	}
	for _, step := range install.Completed {
		if step == MountTarget {
			return true
		}
	}
	return false
}

func loadPreviousStatus(ctx context.Context, install *Context) error {
	if install.PreviousStatus != nil {
		return nil
	}
	record, err := install.Store.LoadStatus(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load previous install status: %w", err)
	}
	install.PreviousStatus = &record
	return nil
}

func refuseChangedInterruptedRequest(install *Context) error {
	if install.PreviousStatus == nil || !install.PreviousStatus.DestructiveMutation {
		return nil
	}
	previousDigest := install.PreviousStatus.RequestDigest
	if previousDigest == "" || previousDigest == install.RequestDigest {
		return nil
	}
	return fmt.Errorf("%w: previous destructive install request digest %s does not match current request digest %s", ErrInstallRefused, previousDigest, install.RequestDigest)
}

func stepStrings(steps []StepID) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		out = append(out, string(step))
	}
	return out
}

func targetDiskStableID(selector manifest.DiskSelector) string {
	switch {
	case selector.ByID != "":
		return selector.ByID
	case selector.WWN != "":
		return "wwn:" + selector.WWN
	case selector.Serial != "":
		return "serial:" + selector.Serial
	default:
		return ""
	}
}

func timeNow() time.Time {
	return time.Now().UTC()
}
