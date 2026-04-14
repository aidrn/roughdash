package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/google/uuid"

	"roughdash/internal/models"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	statements := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return nil, err
		}
	}

	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}

	return store, store.ensureSettingsRow(ctx)
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			totp_secret TEXT NOT NULL,
			role TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS login_challenges (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS helpers (
			id TEXT PRIMARY KEY,
			machine_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			platform TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			paired_at DATETIME NOT NULL,
			last_seen_at DATETIME,
			revoked_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			summary TEXT NOT NULL,
			activity TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			progress REAL NOT NULL DEFAULT 0,
			payload BLOB NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			started_at DATETIME,
			finished_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS job_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			level TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS media_records (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			source_url TEXT NOT NULL,
			title TEXT NOT NULL,
			uploader TEXT NOT NULL,
			upload_date TEXT NOT NULL,
			description TEXT NOT NULL,
			playlist_title TEXT NOT NULL,
			thumbnail_available INTEGER NOT NULL,
			subtitles_available INTEGER NOT NULL,
			subtitles_embedded INTEGER NOT NULL,
			target_path TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS audit_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			target TEXT NOT NULL,
			details TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			browser_enabled INTEGER NOT NULL DEFAULT 1,
			telegram_enabled INTEGER NOT NULL DEFAULT 0,
			telegram_bot_token TEXT NOT NULL DEFAULT '',
			telegram_chat_id TEXT NOT NULL DEFAULT ''
		);`,
	}

	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	if err := s.ensureJobActivityColumn(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureJobActivityColumn(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN activity TEXT NOT NULL DEFAULT '';`)
	if err != nil && !strings.Contains(err.Error(), "duplicate column name: activity") {
		return err
	}
	return nil
}

func (s *Store) ensureSettingsRow(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (id, browser_enabled, telegram_enabled, telegram_bot_token, telegram_chat_id)
		VALUES (1, 1, 0, '', '')
		ON CONFLICT(id) DO NOTHING;
	`)
	return err
}

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users;`).Scan(&count)
	return count > 0, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash, totpSecret, role string) (*models.User, error) {
	user := &models.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: passwordHash,
		TOTPSecret:   totpSecret,
		Role:         role,
		CreatedAt:    time.Now().UTC(),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, totp_secret, role, created_at)
		VALUES (?, ?, ?, ?, ?, ?);
	`, user.ID, user.Username, user.PasswordHash, user.TOTPSecret, user.Role, user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, totp_secret, role, created_at
		FROM users WHERE username = ?;
	`, username)
	var user models.User
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.TOTPSecret, &user.Role, &user.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, totp_secret, role, created_at
		FROM users WHERE id = ?;
	`, id)
	var user models.User
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.TOTPSecret, &user.Role, &user.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) CreateLoginChallenge(ctx context.Context, userID string, ttl time.Duration) (*models.LoginChallenge, error) {
	challenge := &models.LoginChallenge{
		ID:        uuid.NewString(),
		UserID:    userID,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO login_challenges (id, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?);
	`, challenge.ID, challenge.UserID, challenge.CreatedAt, challenge.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return challenge, nil
}

func (s *Store) GetLoginChallenge(ctx context.Context, id string) (*models.LoginChallenge, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, created_at, expires_at
		FROM login_challenges WHERE id = ?;
	`, id)
	var challenge models.LoginChallenge
	if err := row.Scan(&challenge.ID, &challenge.UserID, &challenge.CreatedAt, &challenge.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &challenge, nil
}

func (s *Store) DeleteLoginChallenge(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_challenges WHERE id = ?;`, id)
	return err
}

func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (*models.Session, error) {
	session := &models.Session{
		ID:        uuid.NewString(),
		UserID:    userID,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?);
	`, session.ID, session.UserID, session.CreatedAt, session.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (*models.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, created_at, expires_at
		FROM sessions WHERE id = ?;
	`, sessionID)
	var session models.Session
	if err := row.Scan(&session.ID, &session.UserID, &session.CreatedAt, &session.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &session, nil
}

func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?;`, sessionID)
	return err
}

func (s *Store) CreateHelper(ctx context.Context, machineID, name, platform, tokenHash string) (*models.Helper, error) {
	helper := &models.Helper{
		ID:        uuid.NewString(),
		MachineID: machineID,
		Name:      name,
		Platform:  platform,
		TokenHash: tokenHash,
		PairedAt:  time.Now().UTC(),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO helpers (id, machine_id, name, platform, token_hash, paired_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(machine_id) DO UPDATE SET
			name = excluded.name,
			platform = excluded.platform,
			token_hash = excluded.token_hash,
			paired_at = excluded.paired_at,
			revoked_at = NULL;
	`, helper.ID, helper.MachineID, helper.Name, helper.Platform, helper.TokenHash, helper.PairedAt)
	if err != nil {
		return nil, err
	}
	return s.GetHelperByMachineID(ctx, machineID)
}

func (s *Store) GetHelperByMachineID(ctx context.Context, machineID string) (*models.Helper, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, machine_id, name, platform, token_hash, paired_at, last_seen_at, revoked_at
		FROM helpers WHERE machine_id = ?;
	`, machineID)
	var helper models.Helper
	var lastSeen, revoked sql.NullTime
	if err := row.Scan(&helper.ID, &helper.MachineID, &helper.Name, &helper.Platform, &helper.TokenHash, &helper.PairedAt, &lastSeen, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if lastSeen.Valid {
		helper.LastSeenAt = &lastSeen.Time
	}
	if revoked.Valid {
		helper.RevokedAt = &revoked.Time
	}
	return &helper, nil
}

func (s *Store) GetHelperByID(ctx context.Context, id string) (*models.Helper, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, machine_id, name, platform, token_hash, paired_at, last_seen_at, revoked_at
		FROM helpers WHERE id = ?;
	`, id)
	var helper models.Helper
	var lastSeen, revoked sql.NullTime
	if err := row.Scan(&helper.ID, &helper.MachineID, &helper.Name, &helper.Platform, &helper.TokenHash, &helper.PairedAt, &lastSeen, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if lastSeen.Valid {
		helper.LastSeenAt = &lastSeen.Time
	}
	if revoked.Valid {
		helper.RevokedAt = &revoked.Time
	}
	return &helper, nil
}

func (s *Store) ListHelpers(ctx context.Context) ([]models.Helper, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, machine_id, name, platform, token_hash, paired_at, last_seen_at, revoked_at
		FROM helpers ORDER BY name;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	helpers := []models.Helper{}
	for rows.Next() {
		var helper models.Helper
		var lastSeen, revoked sql.NullTime
		if err := rows.Scan(&helper.ID, &helper.MachineID, &helper.Name, &helper.Platform, &helper.TokenHash, &helper.PairedAt, &lastSeen, &revoked); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			helper.LastSeenAt = &lastSeen.Time
		}
		if revoked.Valid {
			helper.RevokedAt = &revoked.Time
		}
		helpers = append(helpers, helper)
	}
	return helpers, rows.Err()
}

func (s *Store) UpdateHelperHeartbeat(ctx context.Context, helperID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE helpers SET last_seen_at = ?, revoked_at = revoked_at WHERE id = ?;
	`, time.Now().UTC(), helperID)
	return err
}

func (s *Store) RevokeHelper(ctx context.Context, helperID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE helpers SET revoked_at = ? WHERE id = ?;`, time.Now().UTC(), helperID)
	return err
}

func (s *Store) CreateJob(ctx context.Context, jobType, summary string, payload any) (*models.Job, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	job := &models.Job{
		ID:        uuid.NewString(),
		Type:      jobType,
		Status:    models.JobStatusQueued,
		Summary:   summary,
		Progress:  0,
		Payload:   body,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, type, status, summary, error, progress, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, ?, ?, ?);
	`, job.ID, job.Type, job.Status, job.Summary, job.Progress, job.Payload, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Store) UpdateJobState(ctx context.Context, jobID, status, message string, progress float64) error {
	now := time.Now().UTC()
	startedAt := "started_at"
	finishedAt := "finished_at"
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE jobs
		SET status = ?, error = ?, progress = ?, updated_at = ?,
			started_at = CASE WHEN %s IS NULL AND ? = ? THEN ? ELSE %s END,
			finished_at = CASE WHEN ? IN (?, ?, ?, ?) THEN ? ELSE %s END
		WHERE id = ?;
	`, startedAt, startedAt, finishedAt),
		status, message, progress, now,
		status, models.JobStatusRunning, now,
		status, models.JobStatusCompleted, models.JobStatusFailed, models.JobStatusCancelled, models.JobStatusPaused, now,
		jobID,
	)
	return err
}

func (s *Store) UpdateJobActivity(ctx context.Context, jobID, activity string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE jobs
		SET activity = ?, updated_at = ?
		WHERE id = ?;
	`, activity, time.Now().UTC(), jobID)
	return err
}

func (s *Store) UpdateJobPayload(ctx context.Context, jobID string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE jobs
		SET payload = ?, updated_at = ?
		WHERE id = ?;
	`, body, time.Now().UTC(), jobID)
	return err
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*models.Job, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, status, summary, activity, error, progress, payload, created_at, updated_at,
		       started_at, finished_at
		FROM jobs WHERE id = ?;
	`, jobID)
	var job models.Job
	if err := scanJob(row.Scan, &job); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &job, nil
}

func (s *Store) ListJobs(ctx context.Context) ([]models.Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, status, summary, activity, error, progress, payload, created_at, updated_at,
		       started_at, finished_at
		FROM jobs ORDER BY created_at DESC;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []models.Job{}
	for rows.Next() {
		var job models.Job
		if err := scanJob(rows.Scan, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) DeleteJob(ctx context.Context, jobID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?;`, jobID)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListRunnableJobs(ctx context.Context) ([]models.Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, status, summary, activity, error, progress, payload, created_at, updated_at,
		       started_at, finished_at
		FROM jobs
		WHERE status = ? OR status = ?
		ORDER BY created_at ASC;
	`, models.JobStatusQueued, models.JobStatusInterrupted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []models.Job{}
	for rows.Next() {
		var job models.Job
		if err := scanJob(rows.Scan, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func scanJob(scan func(dest ...any) error, job *models.Job) error {
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	if err := scan(
		&job.ID,
		&job.Type,
		&job.Status,
		&job.Summary,
		&job.Activity,
		&job.Error,
		&job.Progress,
		&job.Payload,
		&job.CreatedAt,
		&job.UpdatedAt,
		&startedAt,
		&finishedAt,
	); err != nil {
		return err
	}
	job.StartedAt = nullTimePtr(startedAt)
	job.FinishedAt = nullTimePtr(finishedAt)
	return nil
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	timeValue := value.Time
	return &timeValue
}

func (s *Store) AddJobEvent(ctx context.Context, jobID, level, message string) (*models.JobEvent, error) {
	event := &models.JobEvent{
		JobID:     jobID,
		Level:     level,
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO job_events (job_id, level, message, created_at)
		VALUES (?, ?, ?, ?);
	`, event.JobID, event.Level, event.Message, event.CreatedAt)
	if err != nil {
		return nil, err
	}
	event.ID, _ = result.LastInsertId()
	return event, nil
}

func (s *Store) ListJobEvents(ctx context.Context, jobID string) ([]models.JobEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, level, message, created_at
		FROM job_events WHERE job_id = ? ORDER BY id ASC;
	`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []models.JobEvent{}
	for rows.Next() {
		var event models.JobEvent
		if err := rows.Scan(&event.ID, &event.JobID, &event.Level, &event.Message, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) CreateMediaRecord(ctx context.Context, record models.MediaRecord) error {
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_records (
			id, job_id, source_url, title, uploader, upload_date, description, playlist_title,
			thumbnail_available, subtitles_available, subtitles_embedded, target_path, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`, record.ID, record.JobID, record.SourceURL, record.Title, record.Uploader, record.UploadDate, record.Description, record.PlaylistTitle,
		boolToInt(record.ThumbnailAvailable), boolToInt(record.SubtitlesAvailable), boolToInt(record.SubtitlesEmbedded), record.TargetPath, record.CreatedAt)
	return err
}

func (s *Store) RecordAudit(ctx context.Context, action, actor, target string, details any) error {
	body := ""
	if details != nil {
		buf, err := json.Marshal(details)
		if err != nil {
			return err
		}
		body = string(buf)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_entries (action, actor, target, details, created_at)
		VALUES (?, ?, ?, ?, ?);
	`, action, actor, target, body, time.Now().UTC())
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]models.AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, action, actor, target, details, created_at
		FROM audit_entries
		ORDER BY id DESC
		LIMIT ?;
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []models.AuditEntry{}
	for rows.Next() {
		var entry models.AuditEntry
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.Actor, &entry.Target, &entry.Details, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) GetSettings(ctx context.Context) (models.NotificationSettings, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT browser_enabled, telegram_enabled, telegram_bot_token, telegram_chat_id
		FROM settings WHERE id = 1;
	`)
	var settings models.NotificationSettings
	var browserEnabled, telegramEnabled int
	if err := row.Scan(&browserEnabled, &telegramEnabled, &settings.TelegramBotToken, &settings.TelegramChatID); err != nil {
		return settings, err
	}
	settings.BrowserEnabled = browserEnabled == 1
	settings.TelegramEnabled = telegramEnabled == 1
	return settings, nil
}

func (s *Store) SaveSettings(ctx context.Context, settings models.NotificationSettings) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET browser_enabled = ?, telegram_enabled = ?, telegram_bot_token = ?, telegram_chat_id = ?
		WHERE id = 1;
	`, boolToInt(settings.BrowserEnabled), boolToInt(settings.TelegramEnabled), settings.TelegramBotToken, settings.TelegramChatID)
	return err
}

func (s *Store) ExportSnapshot(ctx context.Context) (map[string]any, error) {
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	audit, err := s.ListAudit(ctx, 500)
	if err != nil {
		return nil, err
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	helpers, err := s.ListHelpers(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"jobs":       jobs,
		"audit":      audit,
		"settings":   settings,
		"helpers":    helpers,
		"exportedAt": time.Now().UTC(),
	}, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
