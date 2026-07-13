package generation

import (
	"testing"
	"time"
)

func TestPromoteLiveGenerationMakesCandidateCurrentAndBootDefault(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	writeBootHealthGeneration(t, root, "gen0", "", CommitStateCommitted, BootStateGood, HealthStateHealthy, now.Add(-time.Hour))
	writeBootHealthGeneration(t, root, "gen1", "gen0", CommitStateCandidate, BootStatePending, HealthStateUnknown, now.Add(-time.Minute))
	writeBootHealthSelection(t, root, BootSelectionRecord{
		APIVersion:          APIVersion,
		Kind:                BootSelectionKind,
		DefaultGenerationID: "gen0",
		BootedGenerationID:  "gen0",
		DefaultBootEntry:    "loader/entries/katl-gen0.conf",
		BootedBootEntry:     "loader/entries/katl-gen0.conf",
		UpdatedAt:           now.Add(-time.Minute),
	})

	result, err := PromoteLiveGeneration(LivePromotionRequest{
		Root: root, GenerationID: "gen1", OperationID: "upgrade-1", Reason: "online Kubernetes upgrade passed health checks", Now: now,
		SetBootDefault: bootHealthDefaultRecorder(t, "loader/entries/katl-gen1.conf"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousGeneration != "gen0" || !result.BootDefaultSet || result.DefaultBootEntry != "loader/entries/katl-gen1.conf" {
		t.Fatalf("result = %#v", result)
	}
	selection, err := ReadBootSelection(root)
	if err != nil {
		t.Fatal(err)
	}
	if selection.DefaultGenerationID != "gen1" || selection.BootedGenerationID != "gen1" || selection.PendingHealthValidation || selection.PreviousKnownGoodGenerationID != "gen0" {
		t.Fatalf("selection = %#v", selection)
	}
	_, current, err := ReadGeneration(root, "gen1")
	if err != nil {
		t.Fatal(err)
	}
	if current.CommitState != CommitStateCommitted || current.BootState != BootStateGood || current.HealthState != HealthStateHealthy || current.CommittedByOperation != "upgrade-1" {
		t.Fatalf("current status = %#v", current)
	}
	_, previous, err := ReadGeneration(root, "gen0")
	if err != nil {
		t.Fatal(err)
	}
	if previous.CommitState != CommitStateSuperseded {
		t.Fatalf("previous commitState = %s", previous.CommitState)
	}
}

func TestPromoteLiveGenerationRequiresBootDefaultUpdater(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	writeBootHealthGeneration(t, root, "gen0", "", CommitStateCommitted, BootStateGood, HealthStateHealthy, now.Add(-time.Hour))
	writeBootHealthGeneration(t, root, "gen1", "gen0", CommitStateCandidate, BootStatePending, HealthStateUnknown, now.Add(-time.Minute))
	writeBootHealthSelection(t, root, BootSelectionRecord{APIVersion: APIVersion, Kind: BootSelectionKind, DefaultGenerationID: "gen0", DefaultBootEntry: "loader/entries/katl-gen0.conf", UpdatedAt: now})

	if _, err := PromoteLiveGeneration(LivePromotionRequest{Root: root, GenerationID: "gen1", Now: now}); err == nil {
		t.Fatal("PromoteLiveGeneration() error = nil, want missing boot default updater")
	}
}
