package config

import (
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Addr                string
	DataDir             string
	DBPath              string
	TempDir             string
	StaticDir           string
	NASRoot             string
	SessionCookieName   string
	SessionTTL          time.Duration
	LoginChallengeTTL   time.Duration
	BootstrapSecret     string
	HelperPairingTTL    time.Duration
	HelperRequestTimeout time.Duration
}

func Load() Config {
	dataDir := getenv("ROUGHDASH_DATA_DIR", filepath.Join(".", "data"))
	return Config{
		Addr:                 getenv("ROUGHDASH_ADDR", ":8420"),
		DataDir:              dataDir,
		DBPath:               getenv("ROUGHDASH_DB_PATH", filepath.Join(dataDir, "roughdash.sqlite")),
		TempDir:              getenv("ROUGHDASH_TEMP_DIR", filepath.Join(dataDir, "tmp")),
		StaticDir:            getenv("ROUGHDASH_STATIC_DIR", filepath.Join(".", "web", "dist")),
		NASRoot:              getenv("ROUGHDASH_NAS_ROOT", "/mnt/Main/AIDEN"),
		SessionCookieName:    getenv("ROUGHDASH_SESSION_COOKIE", "roughdash_session"),
		SessionTTL:           14 * 24 * time.Hour,
		LoginChallengeTTL:    10 * time.Minute,
		BootstrapSecret:      getenv("ROUGHDASH_BOOTSTRAP_SECRET", "roughdash-bootstrap"),
		HelperPairingTTL:     10 * time.Minute,
		HelperRequestTimeout: 30 * time.Second,
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
