package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SlotStore interface {
	SaveSlotState(context.Context, RootSlotState) error
	LoadSlotState(context.Context) (RootSlotState, error)
}

type RootSlotState struct {
	PlannedSlot        RootSlot  `json:"plannedSlot"`
	ArtifactDigest     string    `json:"artifactDigest"`
	ActiveSlotGuard    RootSlot  `json:"activeSlotGuard"`
	WriteStarted       bool      `json:"writeStarted"`
	WriteComplete      bool      `json:"writeComplete"`
	ValidationComplete bool      `json:"validationComplete"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type FileSlotStore struct {
	dir string
	now func() time.Time
}

var (
	ErrSlotRetryRequired = errors.New("partial root slot write requires explicit retry")
	ErrSlotStateConflict = errors.New("root slot state conflicts with requested write")
)

func NewFileSlotStore(dir string) *FileSlotStore {
	return &FileSlotStore{dir: dir, now: time.Now}
}

func (s *FileSlotStore) SaveSlotState(ctx context.Context, state RootSlotState) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create root slot state directory: %w", err)
	}
	state.UpdatedAt = s.now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal root slot state: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(s.path(), data, 0o644); err != nil {
		return fmt.Errorf("write root slot state: %w", err)
	}
	return nil
}

func (s *FileSlotStore) LoadSlotState(ctx context.Context) (RootSlotState, error) {
	select {
	case <-ctx.Done():
		return RootSlotState{}, ctx.Err()
	default:
	}
	data, err := os.ReadFile(s.path())
	if err != nil {
		return RootSlotState{}, err
	}
	var state RootSlotState
	if err := json.Unmarshal(data, &state); err != nil {
		return RootSlotState{}, fmt.Errorf("decode root slot state: %w", err)
	}
	return state, nil
}

func (s *FileSlotStore) path() string {
	return filepath.Join(s.dir, "root-slot.json")
}

func runRootSlot(ctx context.Context, store SlotStore, request RootSlotInstallRequest, retry bool, install RootSlotInstaller) (RootSlotInstallResult, error) {
	if install == nil {
		install = func(context.Context, RootSlotInstallRequest) (RootSlotInstallResult, error) {
			return WriteRootSlot(request)
		}
	}
	want := slotState(request.Plan)
	if store == nil {
		return install(ctx, request)
	}

	current, err := store.LoadSlotState(ctx)
	if err == nil {
		if !sameSlotState(current, want) {
			return RootSlotInstallResult{}, ErrSlotStateConflict
		}
		if current.ValidationComplete {
			return RootSlotInstallResult{
				Slot:         request.Plan.Slot,
				GPTLabel:     request.Plan.TargetPartition.GPTLabel,
				BytesWritten: request.Plan.ExpectedSizeBytes,
				SHA256:       strings.ToLower(strings.TrimSpace(request.Plan.ArtifactDigest)),
			}, nil
		}
		if current.WriteStarted && !retry {
			return RootSlotInstallResult{}, ErrSlotRetryRequired
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RootSlotInstallResult{}, fmt.Errorf("load root slot state: %w", err)
	}

	want.WriteStarted = true
	if err := store.SaveSlotState(ctx, want); err != nil {
		return RootSlotInstallResult{}, err
	}
	result, err := install(ctx, request)
	if err != nil {
		return RootSlotInstallResult{}, err
	}
	want.WriteComplete = true
	if err := store.SaveSlotState(ctx, want); err != nil {
		return RootSlotInstallResult{}, err
	}
	want.ValidationComplete = true
	if err := store.SaveSlotState(ctx, want); err != nil {
		return RootSlotInstallResult{}, err
	}
	return result, nil
}

func slotState(plan RootSlotWritePlan) RootSlotState {
	return RootSlotState{
		PlannedSlot:     plan.Slot,
		ArtifactDigest:  strings.ToLower(strings.TrimSpace(plan.ArtifactDigest)),
		ActiveSlotGuard: plan.ActiveSlotGuard,
	}
}

func sameSlotState(got RootSlotState, want RootSlotState) bool {
	return got.PlannedSlot == want.PlannedSlot &&
		strings.EqualFold(strings.TrimSpace(got.ArtifactDigest), strings.TrimSpace(want.ArtifactDigest)) &&
		got.ActiveSlotGuard == want.ActiveSlotGuard
}
