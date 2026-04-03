package helpers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"roughdash/internal/auth"
	"roughdash/internal/config"
	"roughdash/internal/db"
	"roughdash/internal/models"
)

type protocolEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type pairRequest struct {
	MachineID string `json:"machineId"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	Code      string `json:"code"`
}

type helperHello struct {
	MachineID string `json:"machineId"`
	HelperID  string `json:"helperId"`
	Token     string `json:"token"`
}

type browseRequest struct {
	Path string `json:"path"`
}

type browseResponse struct {
	Entries []models.FileEntry `json:"entries"`
	Error   string             `json:"error,omitempty"`
}

type pendingConn struct {
	conn      *websocket.Conn
	request   pairRequest
	token     string
	createdAt time.Time
}

type liveHelper struct {
	helper models.Helper
	conn   *websocket.Conn
	writeMu sync.Mutex
	waiters map[string]chan browseResponse
}

type Manager struct {
	cfg      config.Config
	store    *db.Store
	upgrader websocket.Upgrader

	mu          sync.Mutex
	pendingByCode map[string]*pendingConn
	liveByID      map[string]*liveHelper
}

func NewManager(cfg config.Config, store *db.Store) *Manager {
	return &Manager{
		cfg: cfg,
		store: store,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
		pendingByCode: make(map[string]*pendingConn),
		liveByID:      make(map[string]*liveHelper),
	}
}

func (m *Manager) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return
	}

	var env protocolEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		_ = conn.Close()
		return
	}

	switch env.Type {
	case "pair.request":
		m.handlePairRequest(conn, env.Payload)
	case "hello":
		m.handleHello(conn, env.Payload)
	default:
		_ = conn.Close()
	}
}

func (m *Manager) handlePairRequest(conn *websocket.Conn, payload json.RawMessage) {
	var request pairRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		_ = conn.Close()
		return
	}
	if request.Code == "" || request.MachineID == "" {
		_ = conn.Close()
		return
	}

	token := randomToken(32)
	pending := &pendingConn{
		conn:      conn,
		request:   request,
		token:     token,
		createdAt: time.Now().UTC(),
	}

	m.mu.Lock()
	m.pendingByCode[request.Code] = pending
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pendingByCode, request.Code)
		m.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (m *Manager) handleHello(conn *websocket.Conn, payload json.RawMessage) {
	var hello helperHello
	if err := json.Unmarshal(payload, &hello); err != nil {
		_ = conn.Close()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	helper, err := m.store.GetHelperByID(ctx, hello.HelperID)
	if err != nil || helper.RevokedAt != nil || helper.TokenHash != auth.HashToken(hello.Token) {
		_ = conn.Close()
		return
	}

	live := &liveHelper{
		helper:  *helper,
		conn:    conn,
		waiters: make(map[string]chan browseResponse),
	}

	m.mu.Lock()
	m.liveByID[helper.ID] = live
	m.mu.Unlock()
	_ = m.store.UpdateHelperHeartbeat(context.Background(), helper.ID)

	defer func() {
		m.mu.Lock()
		delete(m.liveByID, helper.ID)
		m.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env protocolEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		switch env.Type {
		case "heartbeat":
			_ = m.store.UpdateHelperHeartbeat(context.Background(), helper.ID)
		case "browse.response":
			var response browseResponse
			if err := json.Unmarshal(env.Payload, &response); err != nil {
				continue
			}
			live.writeMu.Lock()
			ch := live.waiters[env.RequestID]
			delete(live.waiters, env.RequestID)
			live.writeMu.Unlock()
			if ch != nil {
				ch <- response
			}
		}
	}
}

func (m *Manager) ApprovePending(ctx context.Context, code string) (*models.Helper, string, error) {
	m.mu.Lock()
	pending := m.pendingByCode[code]
	m.mu.Unlock()
	if pending == nil {
		return nil, "", errors.New("pairing code not found")
	}
	if time.Since(pending.createdAt) > m.cfg.HelperPairingTTL {
		return nil, "", errors.New("pairing code expired")
	}

	tokenHash := auth.HashToken(pending.token)
	helper, err := m.store.CreateHelper(ctx, pending.request.MachineID, pending.request.Name, pending.request.Platform, tokenHash)
	if err != nil {
		return nil, "", err
	}

	body, _ := json.Marshal(map[string]any{
		"helperId": helper.ID,
		"token":    pending.token,
	})
	_ = pending.conn.WriteJSON(protocolEnvelope{Type: "pair.confirmed", Payload: body})
	return helper, pending.token, nil
}

func (m *Manager) List(ctx context.Context) ([]models.Helper, error) {
	helpers, err := m.store.ListHelpers(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range helpers {
		_, online := m.liveByID[helpers[i].ID]
		helpers[i].Online = online
	}
	return helpers, nil
}

func (m *Manager) Browse(ctx context.Context, helperID, path string) ([]models.FileEntry, error) {
	m.mu.Lock()
	live := m.liveByID[helperID]
	m.mu.Unlock()
	if live == nil {
		return nil, errors.New("helper is offline")
	}

	requestID := randomToken(12)
	respCh := make(chan browseResponse, 1)

	live.writeMu.Lock()
	live.waiters[requestID] = respCh
	body, _ := json.Marshal(browseRequest{Path: path})
	err := live.conn.WriteJSON(protocolEnvelope{Type: "browse.request", RequestID: requestID, Payload: body})
	live.writeMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-respCh:
		if response.Error != "" {
			return nil, errors.New(response.Error)
		}
		return response.Entries, nil
	}
}

func randomToken(bytesLen int) string {
	buf := make([]byte, bytesLen)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
