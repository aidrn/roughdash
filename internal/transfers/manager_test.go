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
