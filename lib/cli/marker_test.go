package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMarkerRoundtrip(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	m, err := ReadMarker()
	if err != nil {
		t.Fatalf("ReadMarker err = %v", err)
	}
	if m == nil {
		t.Fatal("ReadMarker = nil, want marker")
	}
	if m.SessionID != "s1" || m.Reason != "compact" {
		t.Errorf("marker = %+v, want {s1 compact}", m)
	}
	if _, perr := time.Parse(time.RFC3339, m.CreatedAt); perr != nil {
		t.Errorf("CreatedAt %q not RFC3339: %v", m.CreatedAt, perr)
	}

	if err := ClearMarker(); err != nil {
		t.Fatalf("ClearMarker err = %v", err)
	}
	m2, err := ReadMarker()
	if err != nil || m2 != nil {
		t.Errorf("after ClearMarker ReadMarker = (%v, %v), want (nil, nil)", m2, err)
	}
}

func TestMarkerAbsentNil(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	m, err := ReadMarker()
	if m != nil || err != nil {
		t.Errorf("ReadMarker = (%v, %v), want (nil, nil)", m, err)
	}
	if err := ClearMarker(); err != nil {
		t.Errorf("ClearMarker(부재) err = %v, want nil", err)
	}
}

func TestMarkerCorruptNil(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, "pending-distill.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("파손 마커 기록 실패: %v", err)
	}
	m, err := ReadMarker()
	if m != nil || err == nil {
		t.Errorf("ReadMarker = (%v, %v), want (nil, err)", m, err)
	}
}

func TestMarkerFileConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)

	if got := MarkerFile(); got != filepath.Join(dir, "pending-distill.json") {
		t.Errorf("MarkerFile = %q, want %q", got, filepath.Join(dir, "pending-distill.json"))
	}
}
