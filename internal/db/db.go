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

var (
	ErrNotFound  = errors.New("not found")
	ErrLeaseHeld = errors.New("sync lease is held by another device")
)

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
		`CREATE TABLE IF NOT EXISTS sync_projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root_path TEXT NOT NULL UNIQUE,
			enabled INTEGER NOT NULL,
			ignore_policy TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sync_devices (
			id TEXT PRIMARY KEY,
			helper_id TEXT NOT NULL DEFAULT '',
			machine_id TEXT NOT NULL,
			name TEXT NOT NULL,
			platform TEXT NOT NULL,
			ssd_volume_uuid TEXT NOT NULL,
			last_connection_mode TEXT NOT NULL DEFAULT '',
			paired_at DATETIME NOT NULL,
			last_seen_at DATETIME,
			UNIQUE(machine_id, ssd_volume_uuid)
		);`,
		`CREATE TABLE IF NOT EXISTS sync_device_leases (
			ssd_volume_uuid TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			token TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(device_id) REFERENCES sync_devices(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS sync_items (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			parent_id TEXT NOT NULL,
			relative_path TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			size INTEGER NOT NULL,
			mod_time DATETIME,
			content_hash TEXT NOT NULL,
			metadata_hash TEXT NOT NULL,
			revision INTEGER NOT NULL,
			tombstoned INTEGER NOT NULL,
			dirty INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(project_id, relative_path),
			FOREIGN KEY(project_id) REFERENCES sync_projects(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS sync_revisions (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			item_id TEXT NOT NULL,
			device_id TEXT NOT NULL DEFAULT '',
			base_revision INTEGER NOT NULL,
			revision INTEGER NOT NULL,
			operation TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			metadata_hash TEXT NOT NULL,
			details TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(project_id) REFERENCES sync_projects(id) ON DELETE CASCADE,
			FOREIGN KEY(item_id) REFERENCES sync_items(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS sync_conflicts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			item_id TEXT NOT NULL,
			base_revision INTEGER NOT NULL,
			nas_revision INTEGER NOT NULL,
			ssd_revision INTEGER NOT NULL,
			fields TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			resolved_at DATETIME,
			FOREIGN KEY(project_id) REFERENCES sync_projects(id) ON DELETE CASCADE,
			FOREIGN KEY(item_id) REFERENCES sync_items(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS sync_pins (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			item_id TEXT NOT NULL,
			device_id TEXT NOT NULL,
			mode TEXT NOT NULL,
			recursive INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE(project_id, item_id, device_id),
			FOREIGN KEY(project_id) REFERENCES sync_projects(id) ON DELETE CASCADE,
			FOREIGN KEY(item_id) REFERENCES sync_items(id) ON DELETE CASCADE,
			FOREIGN KEY(device_id) REFERENCES sync_devices(id) ON DELETE CASCADE
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

func (s *Store) CreateSyncProject(ctx context.Context, project models.SyncProject) (*models.SyncProject, error) {
	now := time.Now().UTC()
	if project.ID == "" {
		project.ID = uuid.NewString()
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	project.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_projects (id, name, root_path, enabled, ignore_policy, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(root_path) DO UPDATE SET
			name = excluded.name,
			enabled = excluded.enabled,
			ignore_policy = excluded.ignore_policy,
			updated_at = excluded.updated_at;
	`, project.ID, project.Name, project.RootPath, boolToInt(project.Enabled), project.IgnorePolicy, project.CreatedAt, project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return s.GetSyncProjectByRootPath(ctx, project.RootPath)
}

func (s *Store) GetSyncProject(ctx context.Context, id string) (*models.SyncProject, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, enabled, ignore_policy, created_at, updated_at
		FROM sync_projects WHERE id = ?;
	`, id)
	return scanSyncProject(row.Scan)
}

func (s *Store) GetSyncProjectByRootPath(ctx context.Context, rootPath string) (*models.SyncProject, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, enabled, ignore_policy, created_at, updated_at
		FROM sync_projects WHERE root_path = ?;
	`, rootPath)
	return scanSyncProject(row.Scan)
}

func (s *Store) ListSyncProjects(ctx context.Context) ([]models.SyncProject, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, root_path, enabled, ignore_policy, created_at, updated_at
		FROM sync_projects ORDER BY name;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []models.SyncProject
	for rows.Next() {
		project, err := scanSyncProject(rows.Scan)
		if err != nil {
			return nil, err
		}
		projects = append(projects, *project)
	}
	return projects, rows.Err()
}

func scanSyncProject(scan func(dest ...any) error) (*models.SyncProject, error) {
	var project models.SyncProject
	var enabled int
	if err := scan(&project.ID, &project.Name, &project.RootPath, &enabled, &project.IgnorePolicy, &project.CreatedAt, &project.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	project.Enabled = intToBool(enabled)
	return &project, nil
}

func (s *Store) CreateSyncDevice(ctx context.Context, device models.SyncDevice) (*models.SyncDevice, error) {
	now := time.Now().UTC()
	if device.ID == "" {
		device.ID = uuid.NewString()
	}
	if device.PairedAt.IsZero() {
		device.PairedAt = now
	}
	device.LastSeenAt = &now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_devices (
			id, helper_id, machine_id, name, platform, ssd_volume_uuid, last_connection_mode, paired_at, last_seen_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(machine_id, ssd_volume_uuid) DO UPDATE SET
			helper_id = excluded.helper_id,
			name = excluded.name,
			platform = excluded.platform,
			last_connection_mode = excluded.last_connection_mode,
			last_seen_at = excluded.last_seen_at;
	`, device.ID, device.HelperID, device.MachineID, device.Name, device.Platform, device.SSDVolumeUUID, device.LastConnectionMode, device.PairedAt, now)
	if err != nil {
		return nil, err
	}
	return s.GetSyncDeviceByMachineAndVolume(ctx, device.MachineID, device.SSDVolumeUUID)
}

func (s *Store) GetSyncDevice(ctx context.Context, id string) (*models.SyncDevice, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, helper_id, machine_id, name, platform, ssd_volume_uuid, last_connection_mode, paired_at, last_seen_at
		FROM sync_devices WHERE id = ?;
	`, id)
	return scanSyncDevice(row.Scan)
}

func (s *Store) GetSyncDeviceByMachineAndVolume(ctx context.Context, machineID, volumeUUID string) (*models.SyncDevice, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, helper_id, machine_id, name, platform, ssd_volume_uuid, last_connection_mode, paired_at, last_seen_at
		FROM sync_devices WHERE machine_id = ? AND ssd_volume_uuid = ?;
	`, machineID, volumeUUID)
	return scanSyncDevice(row.Scan)
}

func (s *Store) ListSyncDevices(ctx context.Context) ([]models.SyncDevice, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, helper_id, machine_id, name, platform, ssd_volume_uuid, last_connection_mode, paired_at, last_seen_at
		FROM sync_devices ORDER BY name;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []models.SyncDevice
	for rows.Next() {
		device, err := scanSyncDevice(rows.Scan)
		if err != nil {
			return nil, err
		}
		devices = append(devices, *device)
	}
	return devices, rows.Err()
}

func scanSyncDevice(scan func(dest ...any) error) (*models.SyncDevice, error) {
	var device models.SyncDevice
	var lastSeen sql.NullTime
	if err := scan(
		&device.ID,
		&device.HelperID,
		&device.MachineID,
		&device.Name,
		&device.Platform,
		&device.SSDVolumeUUID,
		&device.LastConnectionMode,
		&device.PairedAt,
		&lastSeen,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	device.LastSeenAt = nullTimePtr(lastSeen)
	return &device, nil
}

func (s *Store) AcquireSyncLease(ctx context.Context, deviceID, volumeUUID string, ttl time.Duration, force bool) (*models.SyncLease, error) {
	now := time.Now().UTC()
	var current models.SyncLease
	err := s.db.QueryRowContext(ctx, `
		SELECT ssd_volume_uuid, device_id, token, expires_at, updated_at
		FROM sync_device_leases WHERE ssd_volume_uuid = ?;
	`, volumeUUID).Scan(&current.SSDVolumeUUID, &current.DeviceID, &current.Token, &current.ExpiresAt, &current.UpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && current.DeviceID != deviceID && current.ExpiresAt.After(now) && !force {
		return &current, ErrLeaseHeld
	}

	lease := models.SyncLease{
		SSDVolumeUUID: volumeUUID,
		DeviceID:      deviceID,
		Token:         uuid.NewString(),
		ExpiresAt:     now.Add(ttl),
		UpdatedAt:     now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sync_device_leases (ssd_volume_uuid, device_id, token, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(ssd_volume_uuid) DO UPDATE SET
			device_id = excluded.device_id,
			token = excluded.token,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at;
	`, lease.SSDVolumeUUID, lease.DeviceID, lease.Token, lease.ExpiresAt, lease.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

func (s *Store) UpsertSyncItem(ctx context.Context, item models.SyncItem) (*models.SyncItem, error) {
	now := time.Now().UTC()
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if item.ParentID == "" {
		item.ParentID = "root"
	}
	if item.Kind == "" {
		item.Kind = models.SyncItemKindFile
	}
	if item.Revision <= 0 {
		item.Revision = 1
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	var modTime any
	if item.ModTime != nil {
		modTime = *item.ModTime
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_items (
			id, project_id, parent_id, relative_path, name, kind, size, mod_time, content_hash, metadata_hash,
			revision, tombstoned, dirty, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, relative_path) DO UPDATE SET
			parent_id = excluded.parent_id,
			name = excluded.name,
			kind = excluded.kind,
			size = excluded.size,
			mod_time = excluded.mod_time,
			content_hash = excluded.content_hash,
			metadata_hash = excluded.metadata_hash,
			revision = excluded.revision,
			tombstoned = excluded.tombstoned,
			dirty = excluded.dirty,
			updated_at = excluded.updated_at;
	`, item.ID, item.ProjectID, item.ParentID, item.RelativePath, item.Name, item.Kind, item.Size, modTime, item.ContentHash, item.MetadataHash,
		item.Revision, boolToInt(item.Tombstoned), boolToInt(item.Dirty), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return s.GetSyncItemByProjectPath(ctx, item.ProjectID, item.RelativePath)
}

func (s *Store) GetSyncItem(ctx context.Context, id string) (*models.SyncItem, error) {
	row := s.db.QueryRowContext(ctx, syncItemSelect()+` WHERE id = ?;`, id)
	return scanSyncItem(row.Scan)
}

func (s *Store) GetSyncItemByProjectPath(ctx context.Context, projectID, relativePath string) (*models.SyncItem, error) {
	row := s.db.QueryRowContext(ctx, syncItemSelect()+` WHERE project_id = ? AND relative_path = ?;`, projectID, relativePath)
	return scanSyncItem(row.Scan)
}

func (s *Store) ListSyncItems(ctx context.Context, projectID, parentID string) ([]models.SyncItem, error) {
	if parentID == "" {
		parentID = "root"
	}
	rows, err := s.db.QueryContext(ctx, syncItemSelect()+`
		WHERE project_id = ? AND parent_id = ?
		ORDER BY kind = 'file', lower(name);
	`, projectID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.SyncItem
	for rows.Next() {
		item, err := scanSyncItem(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func syncItemSelect() string {
	return `SELECT id, project_id, parent_id, relative_path, name, kind, size, mod_time, content_hash, metadata_hash,
		revision, tombstoned, dirty, created_at, updated_at FROM sync_items`
}

func scanSyncItem(scan func(dest ...any) error) (*models.SyncItem, error) {
	var item models.SyncItem
	var modTime sql.NullTime
	var tombstoned, dirty int
	if err := scan(
		&item.ID,
		&item.ProjectID,
		&item.ParentID,
		&item.RelativePath,
		&item.Name,
		&item.Kind,
		&item.Size,
		&modTime,
		&item.ContentHash,
		&item.MetadataHash,
		&item.Revision,
		&tombstoned,
		&dirty,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	item.ModTime = nullTimePtr(modTime)
	item.Tombstoned = intToBool(tombstoned)
	item.Dirty = intToBool(dirty)
	return &item, nil
}

func (s *Store) AppendSyncRevision(ctx context.Context, revision models.SyncRevision) (*models.SyncRevision, error) {
	if revision.ID == "" {
		revision.ID = uuid.NewString()
	}
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_revisions (
			id, project_id, item_id, device_id, base_revision, revision, operation, content_hash, metadata_hash, details, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`, revision.ID, revision.ProjectID, revision.ItemID, revision.DeviceID, revision.BaseRevision, revision.Revision,
		revision.Operation, revision.ContentHash, revision.MetadataHash, revision.Details, revision.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

func (s *Store) CreateSyncConflict(ctx context.Context, conflict models.SyncConflict) (*models.SyncConflict, error) {
	if conflict.ID == "" {
		conflict.ID = uuid.NewString()
	}
	if conflict.Status == "" {
		conflict.Status = models.SyncConflictStatusOpen
	}
	if conflict.CreatedAt.IsZero() {
		conflict.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_conflicts (
			id, project_id, item_id, base_revision, nas_revision, ssd_revision, fields, status, created_at, resolved_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`, conflict.ID, conflict.ProjectID, conflict.ItemID, conflict.BaseRevision, conflict.NASRevision, conflict.SSDRevision,
		conflict.Fields, conflict.Status, conflict.CreatedAt, conflict.ResolvedAt)
	if err != nil {
		return nil, err
	}
	return &conflict, nil
}

func (s *Store) ListSyncConflicts(ctx context.Context, status string) ([]models.SyncConflict, error) {
	query := `
		SELECT id, project_id, item_id, base_revision, nas_revision, ssd_revision, fields, status, created_at, resolved_at
		FROM sync_conflicts`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC;`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conflicts []models.SyncConflict
	for rows.Next() {
		var conflict models.SyncConflict
		var resolved sql.NullTime
		if err := rows.Scan(&conflict.ID, &conflict.ProjectID, &conflict.ItemID, &conflict.BaseRevision, &conflict.NASRevision,
			&conflict.SSDRevision, &conflict.Fields, &conflict.Status, &conflict.CreatedAt, &resolved); err != nil {
			return nil, err
		}
		conflict.ResolvedAt = nullTimePtr(resolved)
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

func (s *Store) ResolveSyncConflict(ctx context.Context, conflictID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sync_conflicts
		SET status = ?, resolved_at = ?
		WHERE id = ?;
	`, models.SyncConflictStatusResolved, time.Now().UTC(), conflictID)
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

func (s *Store) UpsertSyncPin(ctx context.Context, pin models.SyncPin) (*models.SyncPin, error) {
	if pin.ID == "" {
		pin.ID = uuid.NewString()
	}
	if pin.Mode == "" {
		pin.Mode = models.SyncPinModeKeepDownloaded
	}
	if pin.CreatedAt.IsZero() {
		pin.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_pins (id, project_id, item_id, device_id, mode, recursive, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, item_id, device_id) DO UPDATE SET
			mode = excluded.mode,
			recursive = excluded.recursive;
	`, pin.ID, pin.ProjectID, pin.ItemID, pin.DeviceID, pin.Mode, boolToInt(pin.Recursive), pin.CreatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := s.ListSyncPins(ctx, pin.ProjectID, pin.DeviceID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ItemID == pin.ItemID {
			return &row, nil
		}
	}
	return &pin, nil
}

func (s *Store) ListSyncPins(ctx context.Context, projectID, deviceID string) ([]models.SyncPin, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, item_id, device_id, mode, recursive, created_at
		FROM sync_pins
		WHERE project_id = ? AND device_id = ?
		ORDER BY created_at DESC;
	`, projectID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []models.SyncPin
	for rows.Next() {
		var pin models.SyncPin
		var recursive int
		if err := rows.Scan(&pin.ID, &pin.ProjectID, &pin.ItemID, &pin.DeviceID, &pin.Mode, &recursive, &pin.CreatedAt); err != nil {
			return nil, err
		}
		pin.Recursive = intToBool(recursive)
		pins = append(pins, pin)
	}
	return pins, rows.Err()
}

func (s *Store) DeleteSyncPin(ctx context.Context, pinID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sync_pins WHERE id = ?;`, pinID)
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

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func intToBool(v int) bool {
	return v != 0
}
