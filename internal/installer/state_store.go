package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	installstatus "github.com/zariel/katl/internal/installer/status"
)

type StateStore interface {
	SaveCheckpoint(context.Context, Checkpoint) error
	LoadCheckpoint(context.Context) (Checkpoint, error)
	SaveStatus(context.Context, installstatus.Record) error
	LoadStatus(context.Context) (installstatus.Record, error)
}

type Checkpoint struct {
	CurrentStep    StepID    `json:"currentStep"`
	CompletedSteps []StepID  `json:"completedSteps"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type InstallState struct {
	ManifestPath string     `json:"manifestPath"`
	Checkpoint   Checkpoint `json:"checkpoint"`
}

type FileStateStore struct {
	dir string
	now func() time.Time
}

func NewFileStateStore(dir string) *FileStateStore {
	return &FileStateStore{
		dir: dir,
		now: time.Now,
	}
}

func (s *FileStateStore) SaveCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	checkpoint.UpdatedAt = s.now().UTC()
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(s.checkpointPath(), data, 0o644); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	return nil
}

func (s *FileStateStore) LoadCheckpoint(ctx context.Context) (Checkpoint, error) {
	select {
	case <-ctx.Done():
		return Checkpoint{}, ctx.Err()
	default:
	}

	data, err := os.ReadFile(s.checkpointPath())
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read checkpoint: %w", err)
	}

	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode checkpoint: %w", err)
	}

	return checkpoint, nil
}

func (s *FileStateStore) SaveStatus(ctx context.Context, record installstatus.Record) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = s.now().UTC()
	}
	if err := installstatus.WriteFile(s.statusPath(), record); err != nil {
		return err
	}
	return nil
}

func (s *FileStateStore) LoadStatus(ctx context.Context) (installstatus.Record, error) {
	select {
	case <-ctx.Done():
		return installstatus.Record{}, ctx.Err()
	default:
	}

	record, err := installstatus.ReadFile(s.statusPath())
	if err != nil {
		return installstatus.Record{}, err
	}
	return record, nil
}

func (s *FileStateStore) checkpointPath() string {
	return filepath.Join(s.dir, "state.json")
}

func (s *FileStateStore) statusPath() string {
	return filepath.Join(s.dir, "status.json")
}

type MemoryStateStore struct {
	Checkpoints []Checkpoint
	Statuses    []installstatus.Record
}

func (s *MemoryStateStore) SaveCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.Checkpoints = append(s.Checkpoints, checkpoint)
	return nil
}

func (s *MemoryStateStore) LoadCheckpoint(context.Context) (Checkpoint, error) {
	if len(s.Checkpoints) == 0 {
		return Checkpoint{}, os.ErrNotExist
	}

	return s.Checkpoints[len(s.Checkpoints)-1], nil
}

func (s *MemoryStateStore) SaveStatus(ctx context.Context, record installstatus.Record) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.Statuses = append(s.Statuses, record)
	return nil
}

func (s *MemoryStateStore) LoadStatus(context.Context) (installstatus.Record, error) {
	if len(s.Statuses) == 0 {
		return installstatus.Record{}, os.ErrNotExist
	}

	return s.Statuses[len(s.Statuses)-1], nil
}
