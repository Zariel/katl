package generation

import (
	"fmt"
	"strings"
	"time"
)

type LivePromotionRequest struct {
	Root           string
	GenerationID   string
	OperationID    string
	Reason         string
	Now            time.Time
	SetBootDefault BootDefaultSetter
}

type LivePromotionResult struct {
	GenerationID       string
	PreviousGeneration string
	DefaultBootEntry   string
	BootDefaultSet     bool
}

// PromoteLiveGeneration records a candidate that is already active and healthy
// as both the effective runtime generation and the persistent boot default.
func PromoteLiveGeneration(request LivePromotionRequest) (LivePromotionResult, error) {
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	root := cleanRoot(request.Root)
	generationID := strings.TrimSpace(request.GenerationID)
	if generationID == "" {
		return LivePromotionResult{}, fmt.Errorf("live promotion generationID is required")
	}
	spec, status, err := ReadGeneration(root, generationID)
	if err != nil {
		return LivePromotionResult{}, err
	}
	if status.CommitState != CommitStateCandidate {
		return LivePromotionResult{}, fmt.Errorf("generation %s commitState %s cannot be promoted live", generationID, status.CommitState)
	}
	entry := strings.TrimSpace(spec.Boot.LoaderEntryPath)
	if entry == "" {
		return LivePromotionResult{}, fmt.Errorf("generation %s loaderEntryPath is required for live promotion", generationID)
	}
	selection, err := ReadBootSelection(root)
	if err != nil {
		return LivePromotionResult{}, err
	}
	previousID := strings.TrimSpace(selection.DefaultGenerationID)
	if previousID == "" {
		return LivePromotionResult{}, fmt.Errorf("live promotion requires a current default generation")
	}
	if previous := strings.TrimSpace(spec.PreviousGenerationID); previous != "" && previous != previousID {
		return LivePromotionResult{}, fmt.Errorf("generation %s previousGenerationID %s does not match current default %s", generationID, previous, previousID)
	}
	bootDefaultSet := false
	if strings.TrimSpace(selection.DefaultBootEntry) != entry {
		if request.SetBootDefault == nil {
			return LivePromotionResult{}, fmt.Errorf("boot default update required for %s but no updater is configured", entry)
		}
		if err := request.SetBootDefault(root, entry); err != nil {
			return LivePromotionResult{}, fmt.Errorf("set boot default %s: %w", entry, err)
		}
		bootDefaultSet = true
	}

	status.CommitState = CommitStateCommitted
	status.BootState = BootStateGood
	status.HealthState = HealthStateHealthy
	status.UpdatedAt = now
	status.CommittedAt = &now
	status.CommittedByOperation = strings.TrimSpace(request.OperationID)
	status.StatusTransitions = append(status.StatusTransitions, StatusTransition{
		At:          now,
		OperationID: strings.TrimSpace(request.OperationID),
		Reason:      transitionReason(request.Reason, "live generation activation passed health checks"),
		CommitState: status.CommitState,
		BootState:   status.BootState,
		HealthState: status.HealthState,
	})
	if err := WriteGenerationStatus(root, spec, status); err != nil {
		return LivePromotionResult{}, err
	}

	previousEntry := strings.TrimSpace(selection.DefaultBootEntry)
	selection.DefaultGenerationID = generationID
	selection.TargetBootGenerationID = ""
	selection.TrialGenerationID = ""
	selection.PreviousKnownGoodGenerationID = previousID
	selection.BootedGenerationID = generationID
	selection.FailedBootGenerationID = ""
	selection.DefaultBootEntry = entry
	selection.TargetBootEntry = ""
	selection.TrialBootEntry = ""
	selection.PreviousKnownGoodBootEntry = previousEntry
	selection.BootedBootEntry = entry
	selection.BootCountedTrialPath = ""
	selection.PendingTransactionID = ""
	selection.PendingHealthValidation = false
	selection.PersistentDefaultPromotion = DefaultPromotionDone
	selection.RecoveryRequired = false
	selection.UpdatedAt = now
	if err := WriteBootSelection(root, selection); err != nil {
		return LivePromotionResult{}, err
	}
	if previousID != generationID {
		if err := supersedePreviousGeneration(root, previousID, generationID, now); err != nil {
			return LivePromotionResult{}, err
		}
	}
	return LivePromotionResult{
		GenerationID:       generationID,
		PreviousGeneration: previousID,
		DefaultBootEntry:   entry,
		BootDefaultSet:     bootDefaultSet,
	}, nil
}
