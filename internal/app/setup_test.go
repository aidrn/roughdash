package app

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupReportsValidationFailures(t *testing.T) {
	server := newSyncTestServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	status, body := postHTTPJSONStatus(t, client, http.MethodPost, httpServer.URL+"/api/setup", nil, map[string]any{
		"bootstrapSecret": "bootstrap",
		"username":        "admin",
		"password":        "short",
	}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("got status %d want %d: %s", status, http.StatusBadRequest, string(body))
	}
	assertAPIError(t, body, "password must be at least 12 characters")

	status, body = postHTTPJSONStatus(t, client, http.MethodPost, httpServer.URL+"/api/setup", nil, map[string]any{
		"bootstrapSecret": " ",
		"username":        "admin",
		"password":        "correct horse battery staple",
	}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("got status %d want %d: %s", status, http.StatusBadRequest, string(body))
	}
	assertAPIError(t, body, "bootstrap secret is required")

	var response struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	postHTTPJSON(t, client, http.MethodPost, httpServer.URL+"/api/setup", nil, map[string]any{
		"bootstrapSecret": " bootstrap ",
		"username":        " admin ",
		"password":        "correct horse battery staple",
	}, http.StatusCreated, &response)
	if response.User.Username != "admin" {
		t.Fatalf("expected trimmed username, got %q", response.User.Username)
	}
}

func assertAPIError(t *testing.T, body []byte, want string) {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode error %q: %v", body, err)
	}
	if !strings.Contains(payload.Error, want) {
		t.Fatalf("error %q does not contain %q", payload.Error, want)
	}
}
