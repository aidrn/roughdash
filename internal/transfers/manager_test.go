package transfers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadTransferAssemblesAndVerifiesChunks(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	payload := []byte("roughdash sync transfer payload")
	sum := sha256.Sum256(payload)
	session, err := manager.Create(context.Background(), Session{
		Direction:    DirectionUpload,
		ProjectID:    "project",
		RelativePath: "Folder/file.txt",
		Size:         int64(len(payload)),
		ChunkSize:    8,
		SHA256:       hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for index, offset := int64(0), 0; offset < len(payload); index++ {
		end := offset + int(session.ChunkSize)
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := manager.WriteChunk(session.ID, index, bytes.NewReader(payload[offset:end])); err != nil {
			t.Fatalf("write chunk %d: %v", index, err)
		}
		offset = end
	}
	destination := filepath.Join(t.TempDir(), "assembled.txt")
	if _, err := manager.Complete(session.ID, destination); err != nil {
		t.Fatalf("complete session: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("unexpected payload %q", got)
	}
}

func TestUploadTransferRejectsChecksumMismatch(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	session, err := manager.Create(context.Background(), Session{
		Direction:    DirectionUpload,
		ProjectID:    "project",
		RelativePath: "file.txt",
		Size:         4,
		ChunkSize:    4,
		SHA256:       "wrong",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := manager.WriteChunk(session.ID, 0, bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	if _, err := manager.Complete(session.ID, filepath.Join(t.TempDir(), "file.txt")); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestUploadTransferRejectsShortPayloadWithoutChecksum(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	session, err := manager.Create(context.Background(), Session{
		Direction:    DirectionUpload,
		ProjectID:    "project",
		RelativePath: "file.txt",
		Size:         8,
		ChunkSize:    8,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := manager.WriteChunk(session.ID, 0, bytes.NewReader([]byte("tiny"))); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "file.txt")
	if _, err := manager.Complete(session.ID, destination); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("expected incomplete transfer, got %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination should not exist after incomplete transfer: %v", err)
	}
}

func TestExpiredTransferCleansSessionDirectory(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	session, err := manager.Create(context.Background(), Session{
		Direction:    DirectionUpload,
		ProjectID:    "project",
		RelativePath: "file.txt",
		Size:         4,
		ChunkSize:    4,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	manager.mu.Lock()
	manager.open[session.ID].session.ExpiresAt = time.Now().Add(-time.Minute)
	manager.mu.Unlock()
	if _, err := manager.Get(session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected expired session to be missing, got %v", err)
	}
	if _, err := os.Stat(manager.sessionDir(session.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory should be removed after expiry: %v", err)
	}
}

func TestNewManagerRemovesOrphanedSessionDirectories(t *testing.T) {
	tempDir := t.TempDir()
	orphan := filepath.Join(tempDir, transferDirectory, "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	if _, err := NewManager(tempDir); err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan directory should be removed: %v", err)
	}
}
