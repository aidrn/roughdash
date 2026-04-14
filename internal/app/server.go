package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"roughdash/internal/auth"
	"roughdash/internal/config"
	"roughdash/internal/db"
	"roughdash/internal/downloads"
	"roughdash/internal/helpers"
	"roughdash/internal/jobs"
	"roughdash/internal/models"
	"roughdash/internal/system"
)

type ctxKey string

const userKey ctxKey = "user"

type eventHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[chan []byte]struct{})}
}

func (h *eventHub) Subscribe() chan []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, 32)
	h.subs[ch] = struct{}{}
	return ch
}

func (h *eventHub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
	close(ch)
}

func (h *eventHub) publish(payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- body:
		default:
		}
	}
}

type Server struct {
	cfg       config.Config
	logger    *slog.Logger
	store     *db.Store
	downloads *downloads.Service
	helpers   *helpers.Manager
	jobs      *jobs.Engine
	events    *eventHub
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Server, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.TempDir, 0o755); err != nil {
		return nil, err
	}
	store, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, err
	}

	events := newEventHub()
	server := &Server{
		cfg:       cfg,
		logger:    logger,
		store:     store,
		downloads: downloads.NewService(cfg),
		helpers:   helpers.NewManager(cfg, store),
		events:    events,
	}
	server.jobs = jobs.NewEngine(store, server)
	server.jobs.Register(models.JobTypeIngest, server.handleIngestJob)
	server.jobs.Register(models.JobTypeDownload, server.handleDownloadJob)
	return server, nil
}

func (s *Server) Close() error {
	return s.store.Close()
}

func (s *Server) NotifyJob(job models.Job) {
	s.events.publish(map[string]any{"type": "job", "job": job})
}

func (s *Server) NotifyJobEvent(event models.JobEvent) {
	s.events.publish(map[string]any{"type": "job_event", "event": event})
}

func (s *Server) StartJobs(ctx context.Context) {
	go s.jobs.Start(ctx)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.authRequired(s.handleLogout))
	mux.HandleFunc("GET /api/events", s.authRequired(s.handleEvents))

	mux.HandleFunc("GET /api/dashboard", s.authRequired(s.handleDashboard))
	mux.HandleFunc("GET /api/helpers", s.authRequired(s.handleHelpersList))
	mux.HandleFunc("POST /api/helpers/pair", s.authRequired(s.handleHelpersPair))
	mux.HandleFunc("POST /api/helpers/{id}/revoke", s.authRequired(s.handleHelpersRevoke))
	mux.HandleFunc("POST /api/helpers/{id}/browse", s.authRequired(s.handleHelpersBrowse))
	mux.HandleFunc("POST /api/paths/create-folder", s.authRequired(s.handleCreateFolder))
	mux.HandleFunc("POST /api/ingest/preview", s.authRequired(s.handleIngestPreview))
	mux.HandleFunc("POST /api/ingest/jobs", s.authRequired(s.handleIngestJobCreate))
	mux.HandleFunc("POST /api/downloads/preview", s.authRequired(s.handleDownloadsPreview))
	mux.HandleFunc("POST /api/downloads/jobs", s.authRequired(s.handleDownloadsCreate))
	mux.HandleFunc("GET /api/jobs", s.authRequired(s.handleJobsList))
	mux.HandleFunc("GET /api/jobs/{id}", s.authRequired(s.handleJobsGet))
	mux.HandleFunc("DELETE /api/jobs/{id}", s.authRequired(s.handleJobDelete))
	mux.HandleFunc("POST /api/jobs/{id}/pause", s.authRequired(s.handleJobPause))
	mux.HandleFunc("POST /api/jobs/{id}/resume", s.authRequired(s.handleJobResume))
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.authRequired(s.handleJobCancel))
	mux.HandleFunc("GET /api/settings", s.authRequired(s.handleSettingsGet))
	mux.HandleFunc("PUT /api/settings", s.authRequired(s.handleSettingsPut))
	mux.HandleFunc("GET /api/audit", s.authRequired(s.handleAudit))
	mux.HandleFunc("POST /api/export", s.authRequired(s.handleExport))
	mux.HandleFunc("GET /ws/helper", s.helpers.ServeWS)

	fileServer := http.FileServer(http.Dir(s.cfg.StaticDir))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(s.cfg.StaticDir, "index.html")); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"message": "roughdash backend is running; build the frontend in web/dist to serve the dashboard from this binary",
			})
			return
		}
		if r.URL.Path != "/" {
			if _, err := os.Stat(filepath.Join(s.cfg.StaticDir, strings.TrimPrefix(r.URL.Path, "/"))); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFile(w, r, filepath.Join(s.cfg.StaticDir, "index.html"))
	}))

	return withCORS(mux)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hasUsers, err := s.store.HasUsers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	response := map[string]any{
		"setupRequired": !hasUsers,
	}
	user, err := s.currentUser(r)
	if err == nil {
		response["user"] = user
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hasUsers, err := s.store.HasUsers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if hasUsers {
		writeError(w, http.StatusBadRequest, errors.New("setup already completed"))
		return
	}

	var request struct {
		BootstrapSecret string `json:"bootstrapSecret"`
		Username        string `json:"username"`
		Password        string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.BootstrapSecret != s.cfg.BootstrapSecret {
		writeError(w, http.StatusUnauthorized, errors.New("invalid bootstrap secret"))
		return
	}
	if len(strings.TrimSpace(request.Password)) < 12 {
		writeError(w, http.StatusBadRequest, errors.New("password must be at least 12 characters"))
		return
	}

	hash, err := auth.HashPassword(request.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	user, err := s.store.CreateUser(ctx, strings.TrimSpace(request.Username), hash, "", models.UserRoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	session, err := s.store.CreateSession(ctx, user.ID, s.cfg.SessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.RecordAudit(ctx, "setup.completed", user.Username, user.ID, map[string]any{"username": user.Username})
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    session.ID,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Expires:  session.ExpiresAt,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, err := s.store.GetUserByUsername(ctx, strings.TrimSpace(request.Username))
	if err != nil || auth.CheckPassword(user.PasswordHash, request.Password) != nil {
		writeError(w, http.StatusUnauthorized, errors.New("invalid credentials"))
		return
	}
	session, err := s.store.CreateSession(ctx, user.ID, s.cfg.SessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.RecordAudit(ctx, "auth.login", user.Username, user.ID, nil)

	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    session.ID,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Expires:  session.ExpiresAt,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, _ := s.currentUser(r)
	if cookie, err := r.Cookie(s.cfg.SessionCookieName); err == nil {
		_ = s.store.DeleteSession(ctx, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		MaxAge:   -1,
	})
	if user != nil {
		_ = s.store.RecordAudit(ctx, "auth.logout", user.Username, user.ID, nil)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming not supported"))
		return
	}
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case body := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
			flusher.Flush()
		}
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobsList, err := s.store.ListJobs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	helpersList, err := s.helpers.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	activeJobs := 0
	for _, job := range jobsList {
		if job.Status == models.JobStatusRunning || job.Status == models.JobStatusQueued || job.Status == models.JobStatusPaused {
			activeJobs++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"activeJobs": activeJobs,
		"totalJobs":  len(jobsList),
		"helpers":    helpersList,
		"nasRoot":    s.cfg.NASRoot,
	})
}

func (s *Server) handleHelpersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := s.helpers.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now().UTC()
	items = append([]models.Helper{{
		ID:         "local",
		MachineID:  "nas-local",
		Name:       "TrueNAS Host",
		Platform:   runtime.GOOS,
		PairedAt:   now,
		LastSeenAt: &now,
		Online:     true,
	}}, items...)
	writeJSON(w, http.StatusOK, map[string]any{"helpers": items})
}

func (s *Server) handleHelpersPair(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var request struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	helper, _, err := s.helpers.ApprovePending(ctx, strings.TrimSpace(request.Code))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, _ := s.currentUser(r)
	_ = s.store.RecordAudit(ctx, "helper.paired", username(user), helper.ID, map[string]any{"machineId": helper.MachineID})
	writeJSON(w, http.StatusOK, map[string]any{"helper": helper})
}

func (s *Server) handleHelpersRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	helperID := r.PathValue("id")
	if helperID == "local" {
		writeError(w, http.StatusBadRequest, errors.New("cannot revoke local helper"))
		return
	}
	if err := s.store.RevokeHelper(ctx, helperID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	user, _ := s.currentUser(r)
	_ = s.store.RecordAudit(ctx, "helper.revoked", username(user), helperID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) handleHelpersBrowse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var request struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Path == "" {
		request.Path = "/"
	}
	helperID := r.PathValue("id")
	if helperID == "local" {
		if request.Mode == "target" {
			if err := system.EnsureWithinRoot(s.cfg.NASRoot, request.Path); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		entries, err := system.ListEntries(request.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.HelperRequestTimeout)
	defer cancel()
	entries, err := s.helpers.Browse(ctx, helperID, request.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var request struct {
		ParentPath string `json:"parentPath"`
		Name       string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	target := filepath.Join(request.ParentPath, request.Name)
	if err := system.EnsureWithinRoot(s.cfg.NASRoot, target); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	user, _ := s.currentUser(r)
	_ = s.store.RecordAudit(ctx, "path.created", username(user), target, nil)
	writeJSON(w, http.StatusCreated, map[string]string{"path": target})
}

func (s *Server) handleIngestPreview(w http.ResponseWriter, r *http.Request) {
	var request models.IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	preview, err := s.previewIngest(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleIngestJobCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var request models.IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	preview, err := s.previewIngest(ctx, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload := map[string]any{
		"request": request,
		"preview": preview,
	}
	job, err := s.store.CreateJob(ctx, models.JobTypeIngest, fmt.Sprintf("Ingest %d files", len(preview.Files)), payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.jobs.AddEvent(ctx, job.ID, "info", "Job queued")
	user, _ := s.currentUser(r)
	_ = s.store.RecordAudit(ctx, "job.ingest.created", username(user), job.ID, map[string]any{"files": len(preview.Files)})
	s.jobs.Wake()
	writeJSON(w, http.StatusCreated, map[string]any{"job": job, "preview": preview})
}

func (s *Server) handleDownloadsPreview(w http.ResponseWriter, r *http.Request) {
	var request models.DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	preview, groups, err := s.downloads.Preview(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	duplicates, err := s.findDownloadDuplicates(r.Context(), groups)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	preview.Duplicates = duplicates
	writeJSON(w, http.StatusOK, map[string]any{"preview": preview, "resolvedGroups": groups})
}

func (s *Server) handleDownloadsCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var request models.DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	preview, plannedGroups, linkCount, err := s.downloads.Plan(ctx, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.ReplaceExisting && len(request.ReplaceJobIDs) > 0 {
		if err := s.cancelReplacementDownloadJobs(ctx, request.ReplaceJobIDs); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	payload := downloadJobPayload{
		PlannedGroups: plannedGroups,
	}
	job, err := s.store.CreateJob(ctx, models.JobTypeDownload, fmt.Sprintf("Download %d link(s)", linkCount), payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.jobs.AddEvent(ctx, job.ID, "info", "Job queued")
	_ = s.jobs.AddEvent(ctx, job.ID, "info", "Download metadata will be resolved by the worker")
	user, _ := s.currentUser(r)
	_ = s.store.RecordAudit(ctx, "job.download.created", username(user), job.ID, map[string]any{
		"links":        linkCount,
		"replacedJobs": request.ReplaceJobIDs,
	})
	s.jobs.Wake()
	writeJSON(w, http.StatusCreated, map[string]any{"job": job, "preview": preview})
}

func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	jobsList, err := s.store.ListJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobsList})
}

func (s *Server) handleJobsGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := r.PathValue("id")
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	events, err := s.store.ListJobEvents(ctx, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "events": events})
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := r.PathValue("id")
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if job.Status == models.JobStatusQueued || job.Status == models.JobStatusRunning {
		writeError(w, http.StatusBadRequest, errors.New("queued or running jobs cannot be deleted"))
		return
	}
	if err := s.store.DeleteJob(ctx, jobID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	user, _ := s.currentUser(r)
	_ = s.store.RecordAudit(ctx, "job.deleted", username(user), jobID, map[string]any{
		"type":   job.Type,
		"status": job.Status,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleJobPause(w http.ResponseWriter, r *http.Request) {
	if err := s.jobs.Pause(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (s *Server) handleJobResume(w http.ResponseWriter, r *http.Request) {
	if err := s.jobs.Resume(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.jobs.Cancel(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var settings models.NotificationSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveSettings(ctx, settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	user, _ := s.currentUser(r)
	_ = s.store.RecordAudit(ctx, "settings.updated", username(user), "settings", nil)
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListAudit(r.Context(), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.store.ExportSnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="roughdash-export-%s.json"`, time.Now().UTC().Format("20060102-150405")))
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) previewIngest(ctx context.Context, request models.IngestRequest) (models.IngestPreview, error) {
	if request.SourceType != models.SourceTypeLocal {
		return models.IngestPreview{}, errors.New("remote helper ingest transfer is not implemented yet")
	}
	files, skipped, err := system.ExpandMediaPaths(request.Paths)
	if err != nil {
		return models.IngestPreview{}, err
	}

	targetRoot, err := s.resolveIngestTarget(request)
	if err != nil {
		return models.IngestPreview{}, err
	}
	preview := models.IngestPreview{TargetPath: targetRoot, Skipped: skipped}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			preview.Skipped = append(preview.Skipped, models.FileSkip{Path: path, Reason: err.Error()})
			continue
		}
		destinationDir := targetRoot
		if request.Target.Mode == models.TargetModeCamera {
			destinationDir = system.BuildCameraDestination(s.cfg.NASRoot, path, info.ModTime())
		}
		destination := filepath.Join(destinationDir, filepath.Base(path))
		preview.Files = append(preview.Files, models.FileAction{
			SourcePath:      path,
			DestinationPath: destination,
			Kind:            mediaKind(path),
			Size:            info.Size(),
		})
		preview.EstimatedSize += info.Size()
	}
	return preview, nil
}

func (s *Server) resolveIngestTarget(request models.IngestRequest) (string, error) {
	switch request.Target.Mode {
	case models.TargetModeCamera:
		return s.cfg.NASRoot, nil
	case models.TargetModeProject:
		base := request.Target.BasePath
		if base == "" {
			base = s.cfg.NASRoot
		}
		target := filepath.Join(base, request.Target.ProjectFolder)
		if err := system.EnsureWithinRoot(s.cfg.NASRoot, target); err != nil {
			return "", err
		}
		return target, nil
	default:
		return "", errors.New("unknown target mode")
	}
}

func (s *Server) handleIngestJob(ctx context.Context, job models.Job) error {
	var payload struct {
		Request models.IngestRequest `json:"request"`
		Preview models.IngestPreview `json:"preview"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}

	total := len(payload.Preview.Files)
	if total == 0 {
		return errors.New("no ingestable files in job")
	}
	for index, item := range payload.Preview.Files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := os.MkdirAll(filepath.Dir(item.DestinationPath), 0o755); err != nil {
			return err
		}
		if err := s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Copying %s", filepath.Base(item.SourcePath))); err != nil {
			return err
		}
		if err := system.CopyFile(item.SourcePath, item.DestinationPath); err != nil {
			return err
		}
		if payload.Request.VerifyHash {
			sourceHash, err := system.ComputeSHA256(item.SourcePath)
			if err != nil {
				return err
			}
			destinationHash, err := system.ComputeSHA256(item.DestinationPath)
			if err != nil {
				return err
			}
			if sourceHash != destinationHash {
				return errors.New("checksum verification failed")
			}
			_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Verified %s", filepath.Base(item.SourcePath)))
		}
		if payload.Request.MoveFiles {
			if err := os.Remove(item.SourcePath); err != nil {
				return err
			}
		}
		progress := float64(index+1) / float64(total)
		_ = s.jobs.UpdateProgress(ctx, job.ID, progress)
	}
	return nil
}

func (s *Server) handleDownloadJob(ctx context.Context, job models.Job) error {
	var payload downloadJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	groups := payload.Groups
	if len(groups) == 0 && len(payload.PlannedGroups) > 0 {
		resolvedGroups, err := s.resolveDownloadJobMetadata(ctx, job, payload.PlannedGroups)
		if err != nil {
			return err
		}
		groups = resolvedGroups
		payload.Groups = groups
		payload.PlannedGroups = nil
		if err := s.store.UpdateJobPayload(ctx, job.ID, payload); err != nil {
			return err
		}
	}

	total := 0
	for _, group := range groups {
		total += len(group.Videos)
	}
	if total == 0 {
		return errors.New("download job contains no videos")
	}

	completed := 0
	for _, group := range groups {
		if err := os.MkdirAll(group.TargetPath, 0o755); err != nil {
			return err
		}
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Processing group %s -> %s", group.Name, group.TargetPath))
		for _, video := range group.Videos {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			videoProgressBase := float64(completed) / float64(total)
			videoProgressSpan := 1 / float64(total)
			if err := s.processDownloadVideo(ctx, job, group, video, videoProgressBase, videoProgressSpan); err != nil {
				return err
			}
			completed++
			_ = s.jobs.UpdateProgress(ctx, job.ID, float64(completed)/float64(total))
		}
	}
	return nil
}

type downloadJobPayload struct {
	Groups        []downloads.ResolvedGroup `json:"groups,omitempty"`
	PlannedGroups []downloads.PlannedGroup  `json:"plannedGroups,omitempty"`
}

func (s *Server) resolveDownloadJobMetadata(ctx context.Context, job models.Job, plannedGroups []downloads.PlannedGroup) ([]downloads.ResolvedGroup, error) {
	linkCount := 0
	for _, group := range plannedGroups {
		linkCount += len(group.Links)
	}
	_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Resolving metadata for %d link(s)", linkCount))
	_ = s.jobs.UpdateActivity(ctx, job.ID, "Resolving download metadata")

	resolvedGroups := make([]downloads.ResolvedGroup, 0, len(plannedGroups))
	warnings := []models.DownloadWarning{}
	for _, group := range plannedGroups {
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Resolving %s", group.Name))
		resolvedGroup, _, groupWarnings := s.downloads.ResolvePlannedGroupLenient(ctx, group)
		for _, warning := range groupWarnings {
			_ = s.jobs.AddEvent(ctx, job.ID, "warning", fmt.Sprintf("Skipped %s: %s", warning.Link, warning.Message))
		}
		warnings = append(warnings, groupWarnings...)
		resolvedGroups = append(resolvedGroups, resolvedGroup)
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Resolved %d video(s) for %s", len(resolvedGroup.Videos), group.Name))
	}
	if countDownloadVideos(resolvedGroups) == 0 {
		if len(warnings) > 0 {
			return nil, fmt.Errorf("no videos could be resolved; first error: %s", warnings[0].Message)
		}
		return nil, errors.New("no videos could be resolved")
	}
	_ = s.jobs.UpdateActivity(ctx, job.ID, "Metadata resolved")
	return resolvedGroups, nil
}

func countDownloadVideos(groups []downloads.ResolvedGroup) int {
	count := 0
	for _, group := range groups {
		count += len(group.Videos)
	}
	return count
}

func (s *Server) findDownloadDuplicates(ctx context.Context, groups []downloads.ResolvedGroup) ([]models.DownloadDuplicate, error) {
	jobsList, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}

	type payloadGroup struct {
		Groups []downloads.ResolvedGroup `json:"groups"`
	}

	seen := make(map[string]struct{})
	duplicates := make([]models.DownloadDuplicate, 0)
	for _, existingJob := range jobsList {
		if existingJob.Type != models.JobTypeDownload || existingJob.Status == models.JobStatusCancelled {
			continue
		}

		var payload payloadGroup
		if err := json.Unmarshal(existingJob.Payload, &payload); err != nil {
			continue
		}

		existingVideos := make(map[string]downloads.ResolvedVideo)
		for _, group := range payload.Groups {
			for _, video := range group.Videos {
				if video.VideoID == "" {
					continue
				}
				existingVideos[video.VideoID] = video
			}
		}

		for _, group := range groups {
			for _, video := range group.Videos {
				match, ok := existingVideos[video.VideoID]
				if !ok || video.VideoID == "" {
					continue
				}
				key := existingJob.ID + ":" + video.VideoID
				if _, found := seen[key]; found {
					continue
				}
				seen[key] = struct{}{}
				duplicates = append(duplicates, models.DownloadDuplicate{
					VideoID:        video.VideoID,
					Title:          video.Title,
					Uploader:       video.Uploader,
					MatchingJobID:  existingJob.ID,
					MatchingStatus: existingJob.Status,
					TargetPath:     match.FinalPath,
					Active:         isActiveJobStatus(existingJob.Status),
				})
			}
		}
	}

	return duplicates, nil
}

func duplicateJobIDs(duplicates []models.DownloadDuplicate) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, duplicate := range duplicates {
		if _, exists := seen[duplicate.MatchingJobID]; exists {
			continue
		}
		seen[duplicate.MatchingJobID] = struct{}{}
		result = append(result, duplicate.MatchingJobID)
	}
	return result
}

func (s *Server) cancelReplacementDownloadJobs(ctx context.Context, jobIDs []string) error {
	seen := make(map[string]struct{})
	for _, jobID := range jobIDs {
		jobID = strings.TrimSpace(jobID)
		if jobID == "" {
			continue
		}
		if _, exists := seen[jobID]; exists {
			continue
		}
		seen[jobID] = struct{}{}

		job, err := s.store.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Type != models.JobTypeDownload {
			return fmt.Errorf("replacement job %s is not a download job", jobID)
		}
		if !isActiveJobStatus(job.Status) {
			continue
		}
		if err := s.jobs.Cancel(ctx, jobID); err != nil {
			return err
		}
	}
	return nil
}

func activeDuplicateJobIDs(duplicates []models.DownloadDuplicate) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, duplicate := range duplicates {
		if !duplicate.Active {
			continue
		}
		if _, exists := seen[duplicate.MatchingJobID]; exists {
			continue
		}
		seen[duplicate.MatchingJobID] = struct{}{}
		result = append(result, duplicate.MatchingJobID)
	}
	return result
}

func isActiveJobStatus(status string) bool {
	switch status {
	case models.JobStatusQueued, models.JobStatusRunning, models.JobStatusPaused, models.JobStatusInterrupted:
		return true
	default:
		return false
	}
}

func (s *Server) processDownloadVideo(
	ctx context.Context,
	job models.Job,
	group downloads.ResolvedGroup,
	video downloads.ResolvedVideo,
	baseProgress float64,
	progressSpan float64,
) error {
	updateStageProgress := func(fraction float64) {
		value := baseProgress + (progressSpan * fraction)
		if value > 1 {
			value = 1
		}
		_ = s.jobs.UpdateProgress(ctx, job.ID, value)
	}

	tempDir := filepath.Join(s.cfg.TempDir, job.ID, uuid.NewString())
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	updateStageProgress(0.03)
	_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Downloading %s", video.Title))
	_ = s.jobs.UpdateActivity(ctx, job.ID, fmt.Sprintf("Downloading %s", video.Title))
	downloadProgress := newToolProgressReporter(ctx, s.jobs, job.ID, "Download", 6*time.Second, func(fraction float64) {
		updateStageProgress(0.05 + (0.45 * fraction))
	})
	sourcePath, err := s.downloads.DownloadVideo(ctx, video, tempDir, downloadProgress)
	if err != nil {
		return err
	}
	updateStageProgress(0.50)
	_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Downloaded source file %s", filepath.Base(sourcePath)))

	subtitlePath := ""
	subtitlesEmbedded := false
	if group.FetchSubtitles && video.SubtitlesAvailable {
		updateStageProgress(0.55)
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Fetching subtitles for %s", video.Title))
		_ = s.jobs.UpdateActivity(ctx, job.ID, fmt.Sprintf("Fetching subtitles for %s", video.Title))
		subtitleProgress := newToolProgressReporter(ctx, s.jobs, job.ID, "Subtitles", 8*time.Second, func(fraction float64) {
			updateStageProgress(0.55 + (0.08 * fraction))
		})
		fetchedSubtitlePath, warning, subtitleErr := s.downloads.DownloadSubtitles(ctx, video, tempDir, subtitleProgress)
		if subtitleErr != nil {
			_ = s.jobs.AddEvent(ctx, job.ID, "warning", fmt.Sprintf("Subtitles skipped for %s: %s", video.Title, subtitleErr.Error()))
		} else {
			subtitlePath = fetchedSubtitlePath
			if warning != "" {
				_ = s.jobs.AddEvent(ctx, job.ID, "warning", warning)
			}
			if subtitlePath != "" {
				_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Prepared subtitle track %s", filepath.Base(subtitlePath)))
			} else {
				_ = s.jobs.AddEvent(ctx, job.ID, "warning", fmt.Sprintf("No subtitles were captured for %s", video.Title))
			}
		}
	} else if !group.FetchSubtitles && video.SubtitlesAvailable {
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Subtitle fetch disabled for %s", video.Title))
	}

	finalPath := video.FinalPath
	updateStageProgress(0.65)
	if group.Transcode {
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Transcoding %s", filepath.Base(sourcePath)))
		_ = s.jobs.UpdateActivity(ctx, job.ID, fmt.Sprintf("Transcoding %s", filepath.Base(sourcePath)))
		transcodeProgress := newToolProgressReporter(ctx, s.jobs, job.ID, "Transcode", 8*time.Second, func(fraction float64) {
			updateStageProgress(0.65 + (0.30 * fraction))
		})
		subtitlesEmbedded, err = s.downloads.Transcode(ctx, sourcePath, subtitlePath, finalPath, transcodeProgress)
		if err != nil {
			return err
		}
		updateStageProgress(0.95)
		_ = os.Remove(sourcePath)
		if subtitlePath != "" {
			_ = os.Remove(subtitlePath)
		}
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Transcode finished for %s", filepath.Base(finalPath)))
		if subtitlePath != "" && !subtitlesEmbedded {
			_ = s.jobs.AddEvent(ctx, job.ID, "warning", fmt.Sprintf("Subtitle track for %s was not embedded", video.Title))
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(video.FinalPath), 0o755); err != nil {
			return err
		}
		finalPath = strings.TrimSuffix(video.FinalPath, filepath.Ext(video.FinalPath)) + filepath.Ext(sourcePath)
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Finalizing %s", filepath.Base(finalPath)))
		_ = s.jobs.UpdateActivity(ctx, job.ID, fmt.Sprintf("Finalizing %s", filepath.Base(finalPath)))
		if err := system.CopyFile(sourcePath, finalPath); err != nil {
			return err
		}
		_ = os.Remove(sourcePath)
		if subtitlePath != "" {
			_ = os.Remove(subtitlePath)
			_ = s.jobs.AddEvent(ctx, job.ID, "warning", fmt.Sprintf("Discarded subtitle sidecar for %s because transcode is disabled", video.Title))
		}
		updateStageProgress(0.95)
		_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Moved %s into the target folder", filepath.Base(finalPath)))
	}

	record := models.MediaRecord{
		JobID:              job.ID,
		SourceURL:          video.Link,
		Title:              video.Title,
		Uploader:           video.Uploader,
		UploadDate:         video.UploadDate,
		Description:        video.Description,
		PlaylistTitle:      video.PlaylistTitle,
		ThumbnailAvailable: video.ThumbnailAvailable,
		SubtitlesAvailable: video.SubtitlesAvailable,
		SubtitlesEmbedded:  subtitlesEmbedded,
		TargetPath:         finalPath,
	}
	if err := s.store.CreateMediaRecord(ctx, record); err != nil {
		return err
	}
	updateStageProgress(1)
	_ = s.jobs.AddEvent(ctx, job.ID, "info", fmt.Sprintf("Completed %s", filepath.Base(finalPath)))
	return nil
}

func newToolProgressReporter(
	ctx context.Context,
	engine *jobs.Engine,
	jobID string,
	prefix string,
	_ time.Duration,
	onProgress func(float64),
) func(downloads.ProgressUpdate) {
	return func(update downloads.ProgressUpdate) {
		fraction := update.Fraction
		if fraction < 0 {
			fraction = 0
		}
		if fraction > 1 {
			fraction = 1
		}
		onProgress(fraction)
		if strings.TrimSpace(update.Message) == "" {
			return
		}
		_ = engine.UpdateActivity(ctx, jobID, fmt.Sprintf("%s: %s", prefix, update.Message))
	}
}

func (s *Server) authRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.currentUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) currentUser(r *http.Request) (*models.User, error) {
	if user, ok := r.Context().Value(userKey).(*models.User); ok {
		return user, nil
	}
	cookie, err := r.Cookie(s.cfg.SessionCookieName)
	if err != nil {
		return nil, err
	}
	session, err := s.store.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return nil, err
	}
	if session.ExpiresAt.Before(time.Now().UTC()) {
		_ = s.store.DeleteSession(r.Context(), session.ID)
		return nil, errors.New("session expired")
	}
	return s.store.GetUserByID(r.Context(), session.UserID)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mediaKind(path string) string {
	switch {
	case system.IsVideo(path):
		return "video"
	case system.IsPhoto(path):
		return "photo"
	default:
		return "sidecar"
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func username(user *models.User) string {
	if user == nil {
		return "anonymous"
	}
	return user.Username
}

func ReadBody[T any](body io.Reader, value *T) error {
	return json.NewDecoder(body).Decode(value)
}
