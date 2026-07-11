package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	installstatus "github.com/katl-dev/katl/internal/installer/status"
)

func TestFileStateStorePersistsStatus(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStateStore(dir)
	store.now = func() time.Time {
		return time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	}
	record := installstatus.New(installstatus.StateRunning, time.Time{})
	record.RequestDigest = "digest"

	if err := store.SaveStatus(context.Background(), record); err != nil {
		t.Fatalf("SaveStatus() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "status.json")); err != nil {
		t.Fatalf("status file missing: %v", err)
	}
	loaded, err := store.LoadStatus(context.Background())
	if err != nil {
		t.Fatalf("LoadStatus() error = %v", err)
	}
	if loaded.State != installstatus.StateRunning || loaded.RequestDigest != "digest" {
		t.Fatalf("loaded status = %#v", loaded)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Fatalf("updatedAt was not populated")
	}

	reopened := NewFileStateStore(dir)
	loaded, err = reopened.LoadStatus(context.Background())
	if err != nil {
		t.Fatalf("LoadStatus() after restart error = %v", err)
	}
	if loaded.State != installstatus.StateRunning || loaded.RequestDigest != "digest" {
		t.Fatalf("reloaded status = %#v", loaded)
	}
}
