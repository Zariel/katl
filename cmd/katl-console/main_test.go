package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestJournalRingIsBounded(t *testing.T) {
	ring := newJournalRing(2)
	ring.Add("one")
	ring.Add("two")
	ring.Add("three")
	if got := ring.Lines(); !reflect.DeepEqual(got, []string{"two", "three"}) {
		t.Fatalf("Lines() = %#v", got)
	}
}

func TestWriteSnapshotReplacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console", "rendered.txt")
	if err := writeSnapshot(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshot(path, []byte("second\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second\n" {
		t.Fatalf("snapshot = %q", data)
	}
}
