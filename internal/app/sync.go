package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

var (
	errSyncForbidden    = errors.New("sync actor is not allowed to use this device")
	errSyncLeaseMissing = errors.New("active sync lease is required")
)

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
	if !syncActorIsUser(r) {
		writeError(w, http.StatusForbidden, errors.New("user session required"))
		return
	}
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
	if !filepath.IsAbs(rootPath) {
		writeError(w, http.StatusBadRequest, errors.New("rootPath must be absolute"))
		return
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("rootPath must be a directory"))
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
	if !syncActorIsUser(r) {
		writeError(w, http.StatusForbidden, errors.New("user session required"))
		return
	}
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
	if !validSyncItemKind(item.Kind) {
		writeError(w, http.StatusBadRequest, errors.New("invalid item kind"))
		return
	}
	if item.Kind == models.SyncItemKindDirectory {
		item.Size = 0
	} else if item.Size < 0 {
		writeError(w, http.StatusBadRequest, errors.New("size must be non-negative"))
		return
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
	if actor, ok := currentSyncActor(r); ok && actor.Kind == "helper" {
		filtered := devices[:0]
		for _, device := range devices {
			if device.HelperID == actor.ID {
				filtered = append(filtered, device)
			}
		}
		devices = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleSyncDevicesCreate(w http.ResponseWriter, r *http.Request) {
	var request models.SyncDevice
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.MachineID = strings.TrimSpace(request.MachineID)
	request.Name = strings.TrimSpace(request.Name)
	request.Platform = strings.TrimSpace(request.Platform)
	request.SSDVolumeUUID = strings.TrimSpace(request.SSDVolumeUUID)
	request.LastConnectionMode = strings.TrimSpace(request.LastConnectionMode)
	if request.MachineID == "" || request.Name == "" || request.SSDVolumeUUID == "" {
		writeError(w, http.StatusBadRequest, errors.New("machineId, name, and ssdVolumeUuid are required"))
		return
	}
	if request.Platform == "" {
		request.Platform = "darwin"
	}
	if request.Platform != "darwin" {
		writeError(w, http.StatusBadRequest, errors.New("sync devices currently support darwin only"))
		return
	}
	if request.LastConnectionMode != "" && !validConnectionMode(request.LastConnectionMode) {
		writeError(w, http.StatusBadRequest, errors.New("invalid connection mode"))
		return
	}
	if actor, ok := currentSyncActor(r); ok && actor.Kind == "helper" {
		if request.HelperID != "" && request.HelperID != actor.ID {
			writeError(w, http.StatusForbidden, errSyncForbidden)
			return
		}
		request.HelperID = actor.ID
	} else if request.HelperID != "" {
		if _, err := s.store.GetHelperByID(r.Context(), request.HelperID); err != nil {
			writeError(w, statusForStoreError(err), err)
			return
		}
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
	if err := s.ensureActorCanUseDevice(r, device); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	volumeUUID := strings.TrimSpace(request.SSDVolumeUUID)
	if volumeUUID == "" {
		volumeUUID = device.SSDVolumeUUID
	}
	if volumeUUID != device.SSDVolumeUUID {
		writeError(w, http.StatusBadRequest, errors.New("ssdVolumeUuid must match the registered device volume"))
		return
	}
	ttl := 5 * time.Minute
	if request.TTLSeconds > 0 {
		if request.TTLSeconds < 30 || request.TTLSeconds > 1800 {
			writeError(w, http.StatusBadRequest, errors.New("ttlSeconds must be between 30 and 1800"))
			return
		}
		ttl = time.Duration(request.TTLSeconds) * time.Second
	} else if request.TTLSeconds < 0 {
		writeError(w, http.StatusBadRequest, errors.New("ttlSeconds must be non-negative"))
		return
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
	if !validSyncRevisionOperation(revision.Operation) {
		writeError(w, http.StatusBadRequest, errors.New("invalid revision operation"))
		return
	}
	item, err := s.store.GetSyncItem(r.Context(), revision.ItemID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if item.ProjectID != revision.ProjectID {
		writeError(w, http.StatusBadRequest, errors.New("item does not belong to project"))
		return
	}
	if actor, ok := currentSyncActor(r); ok && actor.Kind == "helper" && revision.DeviceID == "" {
		writeError(w, http.StatusBadRequest, errors.New("deviceId is required for helper revisions"))
		return
	}
	if revision.DeviceID != "" {
		device, err := s.store.GetSyncDevice(r.Context(), revision.DeviceID)
		if err != nil {
			writeError(w, statusForStoreError(err), err)
			return
		}
		if err := s.ensureActorCanUseDevice(r, device); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		if _, err := s.requireActiveSyncLease(r.Context(), device); err != nil {
			writeError(w, statusForSyncLeaseError(err), err)
			return
		}
	}
	if revision.BaseRevision != item.Revision {
		conflict, err := s.createSyncConflict(r.Context(), item, revision.BaseRevision, item.Revision, revision.BaseRevision+1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": "sync base revision does not match current NAS revision", "conflict": conflict})
		return
	}
	if revision.Revision <= revision.BaseRevision {
		revision.Revision = revision.BaseRevision + 1
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
	if conflict.Status != "" && conflict.Status != models.SyncConflictStatusOpen {
		writeError(w, http.StatusBadRequest, errors.New("new conflicts must be open"))
		return
	}
	item, err := s.store.GetSyncItem(r.Context(), conflict.ItemID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if item.ProjectID != conflict.ProjectID {
		writeError(w, http.StatusBadRequest, errors.New("item does not belong to project"))
		return
	}
	if conflict.BaseRevision < 0 || conflict.NASRevision < 0 || conflict.SSDRevision < 0 {
		writeError(w, http.StatusBadRequest, errors.New("conflict revisions must be non-negative"))
		return
	}
	if conflict.Fields == "" {
		conflict.Fields = "content"
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
	device, err := s.store.GetSyncDevice(r.Context(), deviceID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if err := s.ensureActorCanUseDevice(r, device); err != nil {
		writeError(w, http.StatusForbidden, err)
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
	if pin.Mode != "" && pin.Mode != models.SyncPinModeKeepDownloaded {
		writeError(w, http.StatusBadRequest, errors.New("invalid pin mode"))
		return
	}
	item, err := s.store.GetSyncItem(r.Context(), pin.ItemID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if item.ProjectID != pin.ProjectID {
		writeError(w, http.StatusBadRequest, errors.New("item does not belong to project"))
		return
	}
	device, err := s.store.GetSyncDevice(r.Context(), pin.DeviceID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if err := s.ensureActorCanUseDevice(r, device); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if _, err := s.requireActiveSyncLease(r.Context(), device); err != nil {
		writeError(w, statusForSyncLeaseError(err), err)
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
	pin, err := s.store.GetSyncPin(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	device, err := s.store.GetSyncDevice(r.Context(), pin.DeviceID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if err := s.ensureActorCanUseDevice(r, device); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if _, err := s.requireActiveSyncLease(r.Context(), device); err != nil {
		writeError(w, statusForSyncLeaseError(err), err)
		return
	}
	if err := s.store.DeleteSyncPin(r.Context(), pin.ID); err != nil {
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
	if request.ProjectID == "" || request.DeviceID == "" || request.RelativePath == "" || request.Size < 0 {
		writeError(w, http.StatusBadRequest, errors.New("projectId, deviceId, relativePath, and non-negative size are required"))
		return
	}
	project, err := s.store.GetSyncProject(r.Context(), request.ProjectID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if !project.Enabled {
		writeError(w, http.StatusBadRequest, errors.New("sync project is disabled"))
		return
	}
	device, err := s.store.GetSyncDevice(r.Context(), request.DeviceID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if err := s.ensureActorCanUseDevice(r, device); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if _, err := s.requireActiveSyncLease(r.Context(), device); err != nil {
		writeError(w, statusForSyncLeaseError(err), err)
		return
	}
	if request.BaseRevision < 0 {
		writeError(w, http.StatusBadRequest, errors.New("baseRevision must be non-negative"))
		return
	}
	if request.SHA256 != "" && !validSHA256(request.SHA256) {
		writeError(w, http.StatusBadRequest, errors.New("sha256 must be a lowercase hex SHA-256 digest"))
		return
	}
	relativePath, err := normalizeSyncRelativePath(request.RelativePath)
	if err != nil || relativePath == "" {
		writeError(w, http.StatusBadRequest, errors.New("valid relativePath is required"))
		return
	}
	request.RelativePath = relativePath
	if existing, err := s.store.GetSyncItemByProjectPath(r.Context(), request.ProjectID, relativePath); err == nil {
		if request.ItemID != "" && request.ItemID != existing.ID {
			writeError(w, http.StatusBadRequest, errors.New("itemId does not match relativePath"))
			return
		}
		if request.BaseRevision != existing.Revision {
			conflict, err := s.createSyncConflict(r.Context(), existing, request.BaseRevision, existing.Revision, request.BaseRevision+1)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": "sync base revision does not match current NAS revision", "conflict": conflict})
			return
		}
		request.ItemID = existing.ID
	} else if !errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err)
		return
	} else if request.BaseRevision != 0 {
		writeError(w, http.StatusConflict, errors.New("sync base revision does not match current NAS revision"))
		return
	}
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
	session, err := s.transfers.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, statusForTransferError(err), err)
		return
	}
	device, err := s.store.GetSyncDevice(r.Context(), session.DeviceID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if err := s.ensureActorCanUseDevice(r, device); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if _, err := s.requireActiveSyncLease(r.Context(), device); err != nil {
		writeError(w, statusForSyncLeaseError(err), err)
		return
	}
	receipt, err := s.transfers.WriteChunk(session.ID, index, http.MaxBytesReader(w, r.Body, transfers.MaxChunkSize+1))
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
	device, err := s.store.GetSyncDevice(r.Context(), session.DeviceID)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	if err := s.ensureActorCanUseDevice(r, device); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if _, err := s.requireActiveSyncLease(r.Context(), device); err != nil {
		writeError(w, statusForSyncLeaseError(err), err)
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
	existing, err := s.store.GetSyncItemByProjectPath(r.Context(), session.ProjectID, relativePath)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing != nil {
		if session.ItemID != "" && session.ItemID != existing.ID {
			writeError(w, http.StatusBadRequest, errors.New("itemId does not match relativePath"))
			return
		}
		if session.BaseRevision != existing.Revision {
			conflict, err := s.createSyncConflict(r.Context(), existing, session.BaseRevision, existing.Revision, session.BaseRevision+1)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": "sync base revision does not match current NAS revision", "conflict": conflict})
			return
		}
	} else if session.BaseRevision != 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "sync base revision does not match current NAS revision"})
		return
	}
	completed, err := s.transfers.Complete(session.ID, destination)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	revisionNumber := session.BaseRevision + 1
	parentID := "root"
	if parentPath := path.Dir(relativePath); parentPath != "." {
		if parent, err := s.store.GetSyncItemByProjectPath(r.Context(), session.ProjectID, parentPath); err == nil {
			parentID = parent.ID
		}
	}
	itemID := session.ItemID
	if existing != nil {
		itemID = existing.ID
	}
	metadataHash := syncMetadataHash(completed.Size, completed.SHA256)
	item, err := s.store.UpsertSyncItem(r.Context(), models.SyncItem{
		ID:           itemID,
		ProjectID:    session.ProjectID,
		ParentID:     parentID,
		RelativePath: relativePath,
		Name:         path.Base(relativePath),
		Kind:         models.SyncItemKindFile,
		Size:         completed.Size,
		ModTime:      ptrTime(time.Now().UTC()),
		ContentHash:  completed.SHA256,
		MetadataHash: metadataHash,
		Revision:     revisionNumber,
		Tombstoned:   false,
		Dirty:        false,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	revision, err := s.store.AppendSyncRevision(r.Context(), models.SyncRevision{
		ProjectID:    item.ProjectID,
		ItemID:       item.ID,
		DeviceID:     session.DeviceID,
		BaseRevision: session.BaseRevision,
		Revision:     revisionNumber,
		Operation:    models.SyncRevisionOperationUpload,
		ContentHash:  completed.SHA256,
		MetadataHash: metadataHash,
		Details:      syncRevisionDetails(relativePath, completed.Size),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.RecordAudit(r.Context(), "sync.transfer.completed", syncActorName(r), item.ID, map[string]any{"projectId": item.ProjectID, "relativePath": item.RelativePath})
	writeJSON(w, http.StatusOK, map[string]any{"transfer": completed, "path": destination, "item": item, "revision": revision})
}

func normalizeSyncRelativePath(value string) (string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if trimmed == "" || trimmed == "." {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", errors.New("path must be relative")
	}
	if len(trimmed) >= 2 && trimmed[1] == ':' && ((trimmed[0] >= 'A' && trimmed[0] <= 'Z') || (trimmed[0] >= 'a' && trimmed[0] <= 'z')) {
		return "", errors.New("path must be relative")
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", errors.New("path escapes project root")
		}
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

func currentSyncActor(r *http.Request) (syncActor, bool) {
	actor, ok := r.Context().Value(syncActorKey{}).(syncActor)
	return actor, ok
}

func syncActorIsUser(r *http.Request) bool {
	actor, ok := currentSyncActor(r)
	return ok && actor.Kind == "user"
}

func (s *Server) ensureActorCanUseDevice(r *http.Request, device *models.SyncDevice) error {
	actor, ok := currentSyncActor(r)
	if !ok {
		return errSyncForbidden
	}
	if actor.Kind == "helper" && device.HelperID != actor.ID {
		return errSyncForbidden
	}
	return nil
}

func (s *Server) requireActiveSyncLease(ctx context.Context, device *models.SyncDevice) (*models.SyncLease, error) {
	lease, err := s.store.GetActiveSyncLease(ctx, device.ID, device.SSDVolumeUUID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, errSyncLeaseMissing
		}
		return nil, err
	}
	return lease, nil
}

func statusForSyncLeaseError(err error) int {
	if errors.Is(err, errSyncLeaseMissing) {
		return http.StatusConflict
	}
	if errors.Is(err, db.ErrNotFound) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func validSyncItemKind(kind string) bool {
	return kind == models.SyncItemKindFile || kind == models.SyncItemKindDirectory
}

func validConnectionMode(mode string) bool {
	switch mode {
	case models.SyncConnectionModeLANDirect,
		models.SyncConnectionModeTailscaleDirect,
		models.SyncConnectionModePeerRelay,
		models.SyncConnectionModeDERPRelay:
		return true
	default:
		return false
	}
}

func validSyncRevisionOperation(operation string) bool {
	switch operation {
	case models.SyncRevisionOperationUpload,
		models.SyncRevisionOperationDelete,
		models.SyncRevisionOperationMetadata:
		return true
	default:
		return false
	}
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (s *Server) createSyncConflict(ctx context.Context, item *models.SyncItem, baseRevision, nasRevision, ssdRevision int64) (*models.SyncConflict, error) {
	return s.store.CreateSyncConflict(ctx, models.SyncConflict{
		ProjectID:    item.ProjectID,
		ItemID:       item.ID,
		BaseRevision: baseRevision,
		NASRevision:  nasRevision,
		SSDRevision:  ssdRevision,
		Fields:       "content",
		Status:       models.SyncConflictStatusOpen,
	})
}

func syncMetadataHash(size int64, contentHash string) string {
	return fmt.Sprintf("size:%d;sha256:%s", size, contentHash)
}

func syncRevisionDetails(relativePath string, size int64) string {
	body, err := json.Marshal(map[string]any{"relativePath": relativePath, "size": size})
	if err != nil {
		return ""
	}
	return string(body)
}

func ptrTime(value time.Time) *time.Time {
	return &value
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
