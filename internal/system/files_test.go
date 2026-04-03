package system

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBuildCameraDestination(t *testing.T) {
	got := BuildCameraDestination("/mnt/Main/AIDEN", filepath.Join("/tmp", "clip.mov"), time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC))
	want := filepath.Join("/mnt/Main/AIDEN", "Camera", "Video", "2026", "2026-04-03")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEnsureWithinRoot(t *testing.T) {
	if err := EnsureWithinRoot("/mnt/Main/AIDEN", "/mnt/Main/AIDEN/Projects/Poland"); err != nil {
		t.Fatalf("expected path to be allowed: %v", err)
	}
	if err := EnsureWithinRoot("/mnt/Main/AIDEN", "/mnt/Main/Other"); err == nil {
		t.Fatalf("expected escape path to fail")
	}
}
