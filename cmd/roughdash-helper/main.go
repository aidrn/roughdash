package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/google/uuid"
)

type protocolEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type helperConfig struct {
	ServerURL string `json:"serverUrl"`
	HelperID  string `json:"helperId"`
	MachineID string `json:"machineId"`
	Token     string `json:"token"`
	Name      string `json:"name"`
}

type browseRequest struct {
	Path string `json:"path"`
}

type browseResponse struct {
	Entries []fileEntry `json:"entries"`
	Error   string      `json:"error,omitempty"`
}

type fileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

func main() {
	serverURL := flag.String("server", "http://localhost:8420", "roughdash server URL")
	name := flag.String("name", hostname(), "helper display name")
	configPath := flag.String("config", defaultConfigPath(), "helper config path")
	approve := flag.Bool("approve", false, "confirm local approval for first-time pairing")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.MachineID == "" {
		cfg.MachineID = uuid.NewString()
	}
	if cfg.Name == "" {
		cfg.Name = *name
	}
	if cfg.ServerURL == "" {
		cfg.ServerURL = *serverURL
	}

	if cfg.Token == "" && !*approve {
		log.Fatal("first-time pairing requires --approve to confirm local access approval")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	for {
		err := run(ctx, cfg, *configPath, *approve)
		if err == nil || ctx.Err() != nil {
			return
		}
		log.Printf("helper disconnected: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func run(ctx context.Context, cfg helperConfig, configPath string, approve bool) error {
	wsURL, err := toWebsocketURL(cfg.ServerURL)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return err
	}
	defer conn.Close()

	if cfg.Token == "" {
		code := randomDigits(6)
		body, _ := json.Marshal(map[string]any{
			"machineId": cfg.MachineID,
			"name":      cfg.Name,
			"platform":  runtime.GOOS,
			"code":      code,
		})
		if err := conn.WriteJSON(protocolEnvelope{Type: "pair.request", Payload: body}); err != nil {
			return err
		}
		fmt.Printf("roughdash helper pairing code: %s\n", code)
		fmt.Println("Enter this code in the roughdash Helpers page to approve the machine.")
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			var env protocolEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				continue
			}
			if env.Type != "pair.confirmed" {
				continue
			}
			var response struct {
				HelperID string `json:"helperId"`
				Token    string `json:"token"`
			}
			if err := json.Unmarshal(env.Payload, &response); err != nil {
				return err
			}
			cfg.HelperID = response.HelperID
			cfg.Token = response.Token
			cfg.ServerURL = strings.TrimRight(cfg.ServerURL, "/")
			if err := saveConfig(configPath, cfg); err != nil {
				return err
			}
			fmt.Printf("paired helper %s (%s)\n", cfg.Name, cfg.HelperID)
			return run(ctx, cfg, configPath, false)
		}
	}

	hello, _ := json.Marshal(map[string]any{
		"machineId": cfg.MachineID,
		"helperId":  cfg.HelperID,
		"token":     cfg.Token,
	})
	if err := conn.WriteJSON(protocolEnvelope{Type: "hello", Payload: hello}); err != nil {
		return err
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = conn.WriteJSON(protocolEnvelope{Type: "heartbeat"})
			}
		}
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var env protocolEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		switch env.Type {
		case "browse.request":
			var request browseRequest
			if err := json.Unmarshal(env.Payload, &request); err != nil {
				continue
			}
			response := browseResponse{}
			entries, err := listEntries(request.Path)
			if err != nil {
				response.Error = err.Error()
			} else {
				response.Entries = entries
			}
			body, _ := json.Marshal(response)
			_ = conn.WriteJSON(protocolEnvelope{Type: "browse.response", RequestID: env.RequestID, Payload: body})
		}
	}
}

func listEntries(path string) ([]fileEntry, error) {
	if path == "" {
		path = "/"
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	entries := make([]fileEntry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntry{
			Name:    item.Name(),
			Path:    filepath.Join(path, item.Name()),
			IsDir:   item.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return entries, nil
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ".roughdash-helper.json"
	}
	return filepath.Join(dir, "roughdash", "helper.json")
}

func loadConfig(path string) (helperConfig, error) {
	var cfg helperConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

func saveConfig(path string, cfg helperConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

func toWebsocketURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported server URL scheme: %s", parsed.Scheme)
	}
	parsed.Path = "/ws/helper"
	return parsed.String(), nil
}

func randomDigits(count int) string {
	buf := make([]byte, count)
	_, _ = rand.Read(buf)
	for i := range buf {
		buf[i] = '0' + (buf[i] % 10)
	}
	return string(buf)
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "helper"
	}
	return name
}
