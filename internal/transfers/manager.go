package transfers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DirectionUpload   = "upload"
	DefaultChunkSize  = int64(8 * 1024 * 1024)
	MaxChunkSize      = int64(128 * 1024 * 1024)
	sessionTTL        = 6 * time.Hour
	transferDirectory = "sync-transfers"
)

var (
	ErrNotFound        = errors.New("transfer session not found")
	ErrInvalidChunk    = errors.New("invalid transfer chunk")
	ErrIncomplete      = errors.New("transfer is missing chunks")
	ErrHashMismatch    = errors.New("transfer checksum mismatch")
	ErrUnsupportedMode = errors.New("unsupported transfer direction")
)

type Session struct {
	ID           string    `json:"id"`
	Direction    string    `json:"direction"`
	ProjectID    string    `json:"projectId"`
	ItemID       string    `json:"itemId"`
	RelativePath string    `json:"relativePath"`
	Size         int64     `json:"size"`
	ChunkSize    int64     `json:"chunkSize"`
	SHA256       string    `json:"sha256,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type ChunkReceipt struct {
	Index  int64  `json:"index"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type sessionState struct {
	session Session
	chunks  map[int64]string
}

type Manager struct {
	root string
	mu   sync.Mutex
	open map[string]*sessionState
}

func NewManager(tempDir string) (*Manager, error) {
	root := filepath.Join(tempDir, transferDirectory)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Manager{root: root, open: make(map[string]*sessionState)}, nil
}

func (m *Manager) Create(_ context.Context, session Session) (Session, error) {
	if session.Direction == "" {
		session.Direction = DirectionUpload
	}
	if session.Direction != DirectionUpload {
		return Session{}, ErrUnsupportedMode
	}
	if session.ChunkSize <= 0 {
		session.ChunkSize = DefaultChunkSize
	}
	if session.ChunkSize > MaxChunkSize {
		session.ChunkSize = MaxChunkSize
	}
	now := time.Now().UTC()
	session.ID = uuid.NewString()
	session.CreatedAt = now
	session.ExpiresAt = now.Add(sessionTTL)

	sessionDir := m.sessionDir(session.ID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return Session{}, err
	}

	m.mu.Lock()
	m.open[session.ID] = &sessionState{
		session: session,
		chunks:  make(map[int64]string),
	}
	m.mu.Unlock()
	return session, nil
}

func (m *Manager) Get(id string) (Session, error) {
	state, err := m.state(id)
	if err != nil {
		return Session{}, err
	}
	return state.session, nil
}

func (m *Manager) WriteChunk(id string, index int64, body io.Reader) (ChunkReceipt, error) {
	state, err := m.state(id)
	if err != nil {
		return ChunkReceipt{}, err
	}
	if index < 0 {
		return ChunkReceipt{}, ErrInvalidChunk
	}
	if state.session.Size >= 0 {
		maxIndex := (state.session.Size + state.session.ChunkSize - 1) / state.session.ChunkSize
		if index >= maxIndex {
			return ChunkReceipt{}, ErrInvalidChunk
		}
	}

	chunkPath := m.chunkPath(id, index)
	tempPath := chunkPath + ".part"
	if err := os.MkdirAll(filepath.Dir(chunkPath), 0o755); err != nil {
		return ChunkReceipt{}, err
	}
	output, err := os.Create(tempPath)
	if err != nil {
		return ChunkReceipt{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(output, hash), body)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return ChunkReceipt{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return ChunkReceipt{}, closeErr
	}
	if size > state.session.ChunkSize {
		_ = os.Remove(tempPath)
		return ChunkReceipt{}, ErrInvalidChunk
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	if err := os.Rename(tempPath, chunkPath); err != nil {
		return ChunkReceipt{}, err
	}

	m.mu.Lock()
	if current := m.open[id]; current != nil {
		current.chunks[index] = sum
	}
	m.mu.Unlock()

	return ChunkReceipt{Index: index, Size: size, SHA256: sum}, nil
}

func (m *Manager) Complete(id, destinationPath string) (Session, error) {
	state, err := m.state(id)
	if err != nil {
		return Session{}, err
	}
	if state.session.Direction != DirectionUpload {
		return Session{}, ErrUnsupportedMode
	}
	if state.session.Size < 0 {
		return Session{}, ErrInvalidChunk
	}
	chunkCount := (state.session.Size + state.session.ChunkSize - 1) / state.session.ChunkSize

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return Session{}, err
	}
	tempPath := destinationPath + ".part"
	output, err := os.Create(tempPath)
	if err != nil {
		return Session{}, err
	}
	hash := sha256.New()
	for index := int64(0); index < chunkCount; index++ {
		chunk, err := os.Open(m.chunkPath(id, index))
		if err != nil {
			output.Close()
			_ = os.Remove(tempPath)
			return Session{}, ErrIncomplete
		}
		_, copyErr := io.Copy(io.MultiWriter(output, hash), chunk)
		closeErr := chunk.Close()
		if copyErr != nil {
			output.Close()
			_ = os.Remove(tempPath)
			return Session{}, copyErr
		}
		if closeErr != nil {
			output.Close()
			_ = os.Remove(tempPath)
			return Session{}, closeErr
		}
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(tempPath)
		return Session{}, err
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	if state.session.SHA256 != "" && state.session.SHA256 != sum {
		_ = os.Remove(tempPath)
		return Session{}, fmt.Errorf("%w: got %s want %s", ErrHashMismatch, sum, state.session.SHA256)
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		return Session{}, err
	}

	m.mu.Lock()
	delete(m.open, id)
	m.mu.Unlock()
	_ = os.RemoveAll(m.sessionDir(id))
	return state.session, nil
}

func (m *Manager) state(id string) (*sessionState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.open[id]
	if state == nil || state.session.ExpiresAt.Before(time.Now().UTC()) {
		return nil, ErrNotFound
	}
	return state, nil
}

func (m *Manager) sessionDir(id string) string {
	return filepath.Join(m.root, id)
}

func (m *Manager) chunkPath(id string, index int64) string {
	return filepath.Join(m.sessionDir(id), fmt.Sprintf("%012d.chunk", index))
}
