package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"roughdash/internal/auth"
	"roughdash/internal/db"
	"roughdash/internal/models"
	"roughdash/internal/system"
	"roughdash/internal/transfers"
)

type syncActorKey struct{}

type syncActor struct {
	Kind string
	ID   string
	Name string
}

func (s *Server) syncAccessRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if user, err := s.currentUser(r); err == nil {
			ctx := context.WithValue(r.Context(), userKey, user)
			ctx = context.WithValue(ctx, syncActorKey{}, syncActor{Kind: "user", ID: user.ID, Name: user.Username})
			next(w, r.WithContext(ctx))
			return
		}

		helperID := strings.TrimSpace(r.Header.Get("X-Roughdash-Helper-ID"))
		token := bearerToken(r.Header.Get("Authorization"))
		if helperID == "" || token == "" {
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		helper, err := s.store.GetHelperByID(r.Context(), helperID)
		if err != nil || helper.RevokedAt != nil {
			writeError(w, http.StatusUnauthorized, errors.New("invalid helper credentials"))
			return
		}
		if subtle.ConstantTimeCompare([]byte(helper.TokenHash), []byte(auth.HashToken(token))) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("invalid helper credentials"))
			return
		}
		_ = s.store.UpdateHelperHeartbeat(r.Context(), helper.ID)
		ctx := context.WithValue(r.Context(), syncActorKey{}, syncActor{Kind: "helper", ID: helper.ID, Name: helper.Name})
		next(w, r.WithContext(ctx))
	}
}

func bearerToken(value string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}

func (s *Server) handleSyncProjectsList(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListSyncProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) handleSyncProjectsCreate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name         string `json:"name"`
		RootPath     string `json:"rootPath"`
		Enabled      *bool  `json:"enabled"`
		IgnorePolicy string `json:"ignorePolicy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rootPath := filepath.Clean(strings.TrimSpace(request.RootPath))
	if rootPath == "." || rootPath == "" {
		writeError(w, http.StatusBadRequest, errors.New("rootPath is required"))
		return
	}
	if err := system.EnsureWithinRoot(s.cfg.NASRoot, rootPath); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = filepath.Base(rootPath)
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	ignorePolicy := strings.TrimSpace(request.IgnorePolicy)
	if ignorePolicy == "" {
		ignorePolicy = "default-system-junk"
	}
	project, err := s.store.CreateSyncProject(r.Context(), models.SyncProject{
		Name:         name,
		RootPath:     rootPath,
		Enabled:      enabled,
		IgnorePolicy: ignorePolicy,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.RecordAudit(r.Context(), "sync.project.saved", syncActorName(r), project.ID, map[string]any{"rootPath": project.RootPath})
	writeJSON(w, http.StatusCreated, map[string]any{"project": project})
}

func (s *Server) handleSyncItemsList(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := s.store.GetSyncProject(r.Context(), projectID); err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	parentID := strings.TrimSpace(r.URL.Query().Get("parentId"))
	items, err := s.store.ListSyncItems(r.Context(), projectID, parentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleSyncItemUpsert(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := s.store.GetSyncProject(r.Context(), projectID); err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	var item models.SyncItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	relativePath, err := normalizeSyncRelativePath(item.RelativePath)
	if err != nil || relativePath == "" {
		writeError(w, http.StatusBadRequest, errors.New("valid relativePath is required"))
		return
	}
	item.ProjectID = projectID
	item.RelativePath = relativePath
	if item.Name == "" {
		item.Name = path.Base(relativePath)
	}
	if item.ParentID == "" {
		item.ParentID = "root"
	}
	if item.Kind == "" {
		item.Kind = models.SyncItemKindFile
	}
	saved, err := s.store.UpsertSyncItem(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": saved})
}

func (s *Server) handleSyncDevicesList(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListSyncDevices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleSyncDevicesCreate(w http.ResponseWriter, r *http.Request) {
	var request models.SyncDevice
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.MachineID == "" || request.Name == "" || request.SSDVolumeUUID == "" {
		writeError(w, http.StatusBadRequest, errors.New("machineId, name, and ssdVolumeUuid are required"))
		return
	}
	if request.Platform == "" {
		request.Platform = "darwin"
	}
	if actor, ok := r.Context().Value(syncActorKey{}).(syncActor); ok && actor.Kind == "helper" && request.HelperID == "" {
		request.HelperID = actor.ID
	}
	device, err := s.store.CreateSyncDevice(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device": device})
}

func (s *Server) handleSyncDeviceLease(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SSDVolumeUUID string `json:"ssdVolumeUuid"`
		TTLSeconds    int    `json:"ttlSeconds"`
		Force         bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	device, err := s.store.GetSyncDevice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	volumeUUID := strings.TrimSpace(request.SSDVolumeUUID)
	if volumeUUID == "" {
		volumeUUID = device.SSDVolumeUUID
	}
	ttl := 5 * time.Minute
	if request.TTLSeconds > 0 {
		ttl = time.Duration(request.TTLSeconds) * time.Second
	}
	lease, err := s.store.AcquireSyncLease(r.Context(), device.ID, volumeUUID, ttl, request.Force)
	if err != nil {
		if errors.Is(err, db.ErrLeaseHeld) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "lease": lease})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lease": lease})
}

func (s *Server) handleSyncRevisionCreate(w http.ResponseWriter, r *http.Request) {
	var revision models.SyncRevision
	if err := json.NewDecoder(r.Body).Decode(&revision); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if revision.ProjectID == "" || revision.ItemID == "" || revision.Operation == "" {
		writeError(w, http.StatusBadRequest, errors.New("projectId, itemId, and operation are required"))
		return
	}
	saved, err := s.store.AppendSyncRevision(r.Context(), revision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"revision": saved})
}

func (s *Server) handleSyncConflictsList(w http.ResponseWriter, r *http.Request) {
	conflicts, err := s.store.ListSyncConflicts(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts})
}

func (s *Server) handleSyncConflictsCreate(w http.ResponseWriter, r *http.Request) {
	var conflict models.SyncConflict
	if err := json.NewDecoder(r.Body).Decode(&conflict); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if conflict.ProjectID == "" || conflict.ItemID == "" {
		writeError(w, http.StatusBadRequest, errors.New("projectId and itemId are required"))
		return
	}
	saved, err := s.store.CreateSyncConflict(r.Context(), conflict)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conflict": saved})
}

func (s *Server) handleSyncConflictResolve(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ResolveSyncConflict(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func (s *Server) handleSyncPinsList(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("projectId"))
	deviceID := strings.TrimSpace(r.URL.Query().Get("deviceId"))
	if projectID == "" || deviceID == "" {
		writeError(w, http.StatusBadRequest, errors.New("projectId and deviceId are required"))
		return
	}
	pins, err := s.store.ListSyncPins(r.Context(), projectID, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pins": pins})
}

func (s *Server) handleSyncPinsUpsert(w http.ResponseWriter, r *http.Request) {
	var pin models.SyncPin
	if err := json.NewDecoder(r.Body).Decode(&pin); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if pin.ProjectID == "" || pin.ItemID == "" || pin.DeviceID == "" {
		writeError(w, http.StatusBadRequest, errors.New("projectId, itemId, and deviceId are required"))
		return
	}
	saved, err := s.store.UpsertSyncPin(r.Context(), pin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pin": saved})
}

func (s *Server) handleSyncPinDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSyncPin(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleSyncTransferCreate(w http.ResponseWriter, r *http.Request) {
	var request transfers.Session
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.ProjectID == "" || request.RelativePath == "" || request.Size < 0 {
		writeError(w, http.StatusBadRequest, errors.New("projectId, relativePath, and non-negative size are required"))
		return
	}
	relativePath, err := normalizeSyncRelativePath(request.RelativePath)
	if err != nil || relativePath == "" {
		writeError(w, http.StatusBadRequest, errors.New("valid relativePath is required"))
		return
	}
	request.RelativePath = relativePath
	session, err := s.transfers.Create(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"transfer": session})
}

func (s *Server) handleSyncTransferChunk(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.ParseInt(r.PathValue("index"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid chunk index"))
		return
	}
	receipt, err := s.transfers.WriteChunk(r.PathValue("id"), index, http.MaxBytesReader(w, r.Body, transfers.MaxChunkSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chunk": receipt})
}

func (s *Server) handleSyncTransferComplete(w http.ResponseWriter, r *http.Request) {
	session, err := s.transfers.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, statusForTransferError(err), err)
		return
	}
	project, err := s.store.GetSyncProject(r.Context(), session.ProjectID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	relativePath, err := normalizeSyncRelativePath(session.RelativePath)
	if err != nil || relativePath == "" {
		writeError(w, http.StatusBadRequest, errors.New("valid relativePath is required"))
		return
	}
	destination := filepath.Join(project.RootPath, filepath.FromSlash(relativePath))
	if err := system.EnsureWithinRoot(project.RootPath, destination); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	completed, err := s.transfers.Complete(session.ID, destination)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfer": completed, "path": destination})
}

func normalizeSyncRelativePath(value string) (string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if trimmed == "" || trimmed == "." {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", errors.New("path must be relative")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("path escapes project root")
	}
	return cleaned, nil
}

func statusForStoreError(err error) int {
	if errors.Is(err, db.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func statusForTransferError(err error) int {
	if errors.Is(err, transfers.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func syncActorName(r *http.Request) string {
	if actor, ok := r.Context().Value(syncActorKey{}).(syncActor); ok && actor.Name != "" {
		return fmt.Sprintf("%s:%s", actor.Kind, actor.Name)
	}
	return "sync"
}
