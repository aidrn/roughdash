package models

import "time"

const (
	UserRoleAdmin = "admin"

	JobTypeIngest   = "ingest"
	JobTypeDownload = "download"

	JobStatusQueued      = "queued"
	JobStatusRunning     = "running"
	JobStatusPaused      = "paused"
	JobStatusFailed      = "failed"
	JobStatusCompleted   = "completed"
	JobStatusCancelled   = "cancelled"
	JobStatusInterrupted = "interrupted"

	TargetModeCamera  = "camera"
	TargetModeProject = "project"

	SourceTypeLocal  = "local"
	SourceTypeHelper = "helper"

	SyncItemKindFile      = "file"
	SyncItemKindDirectory = "directory"

	SyncPinModeKeepDownloaded = "keep_downloaded"

	SyncConflictStatusOpen     = "open"
	SyncConflictStatusResolved = "resolved"
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	TOTPSecret   string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type LoginChallenge struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type NotificationSettings struct {
	BrowserEnabled   bool   `json:"browserEnabled"`
	TelegramEnabled  bool   `json:"telegramEnabled"`
	TelegramBotToken string `json:"telegramBotToken"`
	TelegramChatID   string `json:"telegramChatId"`
}

type Helper struct {
	ID         string     `json:"id"`
	MachineID  string     `json:"machineId"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	TokenHash  string     `json:"-"`
	PairedAt   time.Time  `json:"pairedAt"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
	Online     bool       `json:"online"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type PendingHelper struct {
	MachineID  string    `json:"machineId"`
	Name       string    `json:"name"`
	Platform   string    `json:"platform"`
	Code       string    `json:"code"`
	Token      string    `json:"-"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	ApprovedAt time.Time `json:"approvedAt"`
}

type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type JobTarget struct {
	Mode          string `json:"mode"`
	BasePath      string `json:"basePath"`
	ProjectFolder string `json:"projectFolder,omitempty"`
}

type Job struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary"`
	Activity   string     `json:"activity,omitempty"`
	Error      string     `json:"error,omitempty"`
	Progress   float64    `json:"progress"`
	Payload    []byte     `json:"payload,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type JobEvent struct {
	ID        int64     `json:"id"`
	JobID     string    `json:"jobId"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type IngestRequest struct {
	SourceType string    `json:"sourceType"`
	HelperID   string    `json:"helperId,omitempty"`
	Paths      []string  `json:"paths"`
	MoveFiles  bool      `json:"moveFiles"`
	VerifyHash bool      `json:"verifyHash"`
	Target     JobTarget `json:"target"`
}

type IngestPreview struct {
	TargetPath    string       `json:"targetPath"`
	Files         []FileAction `json:"files"`
	Skipped       []FileSkip   `json:"skipped"`
	EstimatedSize int64        `json:"estimatedSize"`
}

type FileAction struct {
	SourcePath      string `json:"sourcePath"`
	DestinationPath string `json:"destinationPath"`
	Kind            string `json:"kind"`
	Size            int64  `json:"size"`
}

type FileSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type DownloadRequest struct {
	Groups          []DownloadGroup `json:"groups"`
	ReplaceExisting bool            `json:"replaceExisting,omitempty"`
	ReplaceJobIDs   []string        `json:"replaceJobIds,omitempty"`
}

type DownloadGroup struct {
	Name           string   `json:"name"`
	BasePath       string   `json:"basePath"`
	NewFolder      string   `json:"newFolder,omitempty"`
	Links          []string `json:"links"`
	Transcode      bool     `json:"transcode"`
	FetchSubtitles bool     `json:"fetchSubtitles"`
}

type DownloadPreview struct {
	Groups     []DownloadGroupPreview `json:"groups"`
	Duplicates []DownloadDuplicate    `json:"duplicates,omitempty"`
	Warnings   []DownloadWarning      `json:"warnings,omitempty"`
}

type DownloadGroupPreview struct {
	Name       string                 `json:"name"`
	TargetPath string                 `json:"targetPath"`
	Videos     []DownloadVideoPreview `json:"videos"`
}

type DownloadVideoPreview struct {
	Link          string `json:"link"`
	VideoID       string `json:"videoId"`
	Title         string `json:"title"`
	Uploader      string `json:"uploader"`
	PlaylistTitle string `json:"playlistTitle,omitempty"`
	QualityLabel  string `json:"qualityLabel"`
	FinalPath     string `json:"finalPath"`
}

type DownloadDuplicate struct {
	VideoID        string `json:"videoId"`
	Title          string `json:"title"`
	Uploader       string `json:"uploader"`
	MatchingJobID  string `json:"matchingJobId"`
	MatchingStatus string `json:"matchingStatus"`
	TargetPath     string `json:"targetPath"`
	Active         bool   `json:"active"`
}

type DownloadWarning struct {
	Link    string `json:"link"`
	Message string `json:"message"`
}

type MediaRecord struct {
	ID                 string    `json:"id"`
	JobID              string    `json:"jobId"`
	SourceURL          string    `json:"sourceUrl"`
	Title              string    `json:"title"`
	Uploader           string    `json:"uploader"`
	UploadDate         string    `json:"uploadDate"`
	Description        string    `json:"description"`
	PlaylistTitle      string    `json:"playlistTitle"`
	ThumbnailAvailable bool      `json:"thumbnailAvailable"`
	SubtitlesAvailable bool      `json:"subtitlesAvailable"`
	SubtitlesEmbedded  bool      `json:"subtitlesEmbedded"`
	TargetPath         string    `json:"targetPath"`
	CreatedAt          time.Time `json:"createdAt"`
}

type AuditEntry struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Actor     string    `json:"actor"`
	Target    string    `json:"target"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"createdAt"`
}

type SyncProject struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RootPath     string    `json:"rootPath"`
	Enabled      bool      `json:"enabled"`
	IgnorePolicy string    `json:"ignorePolicy"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type SyncDevice struct {
	ID                 string     `json:"id"`
	HelperID           string     `json:"helperId,omitempty"`
	MachineID          string     `json:"machineId"`
	Name               string     `json:"name"`
	Platform           string     `json:"platform"`
	SSDVolumeUUID      string     `json:"ssdVolumeUuid"`
	LastConnectionMode string     `json:"lastConnectionMode,omitempty"`
	PairedAt           time.Time  `json:"pairedAt"`
	LastSeenAt         *time.Time `json:"lastSeenAt,omitempty"`
}

type SyncLease struct {
	SSDVolumeUUID string    `json:"ssdVolumeUuid"`
	DeviceID      string    `json:"deviceId"`
	Token         string    `json:"token"`
	ExpiresAt     time.Time `json:"expiresAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type SyncItem struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"projectId"`
	ParentID     string     `json:"parentId"`
	RelativePath string     `json:"relativePath"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	Size         int64      `json:"size"`
	ModTime      *time.Time `json:"modTime,omitempty"`
	ContentHash  string     `json:"contentHash"`
	MetadataHash string     `json:"metadataHash"`
	Revision     int64      `json:"revision"`
	Tombstoned   bool       `json:"tombstoned"`
	Dirty        bool       `json:"dirty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type SyncRevision struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	ItemID       string    `json:"itemId"`
	DeviceID     string    `json:"deviceId,omitempty"`
	BaseRevision int64     `json:"baseRevision"`
	Revision     int64     `json:"revision"`
	Operation    string    `json:"operation"`
	ContentHash  string    `json:"contentHash"`
	MetadataHash string    `json:"metadataHash"`
	Details      string    `json:"details"`
	CreatedAt    time.Time `json:"createdAt"`
}

type SyncConflict struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"projectId"`
	ItemID       string     `json:"itemId"`
	BaseRevision int64      `json:"baseRevision"`
	NASRevision  int64      `json:"nasRevision"`
	SSDRevision  int64      `json:"ssdRevision"`
	Fields       string     `json:"fields"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	ResolvedAt   *time.Time `json:"resolvedAt,omitempty"`
}

type SyncPin struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	ItemID    string    `json:"itemId"`
	DeviceID  string    `json:"deviceId"`
	Mode      string    `json:"mode"`
	Recursive bool      `json:"recursive"`
	CreatedAt time.Time `json:"createdAt"`
}
