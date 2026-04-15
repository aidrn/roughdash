package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"roughdash/internal/auth"
	"roughdash/internal/config"
	"roughdash/internal/models"
)

func TestSyncHelperDeviceOwnershipAndLeaseGatedPins(t *testing.T) {
	server := newSyncTestServer(t)
	handler := server.Handler()
	ctx := context.Background()

	helperOne, helperOneToken := createSyncTestHelper(t, server, "helper-one")
	helperTwo, _ := createSyncTestHelper(t, server, "helper-two")
	project, item := createSyncTestProjectAndItem(t, server)

	response := doSyncJSON(t, handler, http.MethodPost, "/api/sync/devices", map[string]any{
		"helperId":      helperTwo.ID,
		"machineId":     "mac-one",
		"name":          "Mac One",
		"platform":      "darwin",
		"ssdVolumeUuid": "volume-one",
	}, helperOne.ID, helperOneToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected helper mismatch to be forbidden, got %d: %s", response.Code, response.Body.String())
	}

	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/devices", map[string]any{
		"machineId":          "mac-one",
		"name":               "Mac One",
		"platform":           "darwin",
		"ssdVolumeUuid":      "volume-one",
		"lastConnectionMode": models.SyncConnectionModeLANDirect,
	}, helperOne.ID, helperOneToken)
	if response.Code != http.StatusCreated {
		t.Fatalf("create device: got %d: %s", response.Code, response.Body.String())
	}
	var created struct {
		Device models.SyncDevice `json:"device"`
	}
	decodeSyncResponse(t, response, &created)
	if created.Device.HelperID != helperOne.ID {
		t.Fatalf("device was not bound to helper: %#v", created.Device)
	}

	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/pins", models.SyncPin{
		ProjectID: project.ID,
		ItemID:    item.ID,
		DeviceID:  created.Device.ID,
		Mode:      models.SyncPinModeKeepDownloaded,
	}, helperOne.ID, helperOneToken)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected pin without lease to conflict, got %d: %s", response.Code, response.Body.String())
	}

	if _, err := server.store.AcquireSyncLease(ctx, created.Device.ID, created.Device.SSDVolumeUUID, time.Minute, false); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/pins", models.SyncPin{
		ProjectID: project.ID,
		ItemID:    item.ID,
		DeviceID:  created.Device.ID,
		Mode:      models.SyncPinModeKeepDownloaded,
	}, helperOne.ID, helperOneToken)
	if response.Code != http.StatusOK {
		t.Fatalf("expected leased pin to succeed, got %d: %s", response.Code, response.Body.String())
	}
}

func TestSyncTransferRequiresLeaseAndCatalogsCompletedUpload(t *testing.T) {
	server := newSyncTestServer(t)
	handler := server.Handler()
	ctx := context.Background()

	helper, token := createSyncTestHelper(t, server, "transfer-helper")
	project, _ := createSyncTestProjectAndItem(t, server)
	device, err := server.store.CreateSyncDevice(ctx, models.SyncDevice{
		HelperID:      helper.ID,
		MachineID:     "transfer-mac",
		Name:          "Transfer Mac",
		Platform:      "darwin",
		SSDVolumeUUID: "transfer-volume",
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	payload := []byte("roughdash sync upload")
	sum := sha256.Sum256(payload)
	transferRequest := map[string]any{
		"direction":    "upload",
		"projectId":    project.ID,
		"deviceId":     device.ID,
		"relativePath": "Folder/new.txt",
		"baseRevision": float64(0),
		"size":         float64(len(payload)),
		"chunkSize":    float64(len(payload)),
		"sha256":       hex.EncodeToString(sum[:]),
	}
	response := doSyncJSON(t, handler, http.MethodPost, "/api/sync/transfers", transferRequest, helper.ID, token)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected transfer without lease to conflict, got %d: %s", response.Code, response.Body.String())
	}

	if _, err := server.store.AcquireSyncLease(ctx, device.ID, device.SSDVolumeUUID, time.Minute, false); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/transfers", map[string]any{
		"direction":    "upload",
		"projectId":    project.ID,
		"deviceId":     device.ID,
		"relativePath": "../escape.txt",
		"baseRevision": float64(0),
		"size":         float64(len(payload)),
		"chunkSize":    float64(len(payload)),
		"sha256":       hex.EncodeToString(sum[:]),
	}, helper.ID, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid path to be rejected, got %d: %s", response.Code, response.Body.String())
	}

	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/transfers", transferRequest, helper.ID, token)
	if response.Code != http.StatusCreated {
		t.Fatalf("create transfer: got %d: %s", response.Code, response.Body.String())
	}
	var created struct {
		Transfer struct {
			ID string `json:"id"`
		} `json:"transfer"`
	}
	decodeSyncResponse(t, response, &created)

	chunkReq := httptest.NewRequest(http.MethodPut, "/api/sync/transfers/"+created.Transfer.ID+"/chunks/0", bytes.NewReader(payload))
	chunkReq.Header.Set("Authorization", "Bearer "+token)
	chunkReq.Header.Set("X-Roughdash-Helper-ID", helper.ID)
	chunkResp := httptest.NewRecorder()
	handler.ServeHTTP(chunkResp, chunkReq)
	if chunkResp.Code != http.StatusOK {
		t.Fatalf("write chunk: got %d: %s", chunkResp.Code, chunkResp.Body.String())
	}

	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/transfers/"+created.Transfer.ID+"/complete", nil, helper.ID, token)
	if response.Code != http.StatusOK {
		t.Fatalf("complete transfer: got %d: %s", response.Code, response.Body.String())
	}
	var completed struct {
		Item     models.SyncItem     `json:"item"`
		Revision models.SyncRevision `json:"revision"`
	}
	decodeSyncResponse(t, response, &completed)
	if completed.Item.RelativePath != "Folder/new.txt" || completed.Item.Revision != 1 || completed.Revision.Operation != models.SyncRevisionOperationUpload {
		t.Fatalf("unexpected catalog response: %#v %#v", completed.Item, completed.Revision)
	}
	written, err := os.ReadFile(filepath.Join(project.RootPath, "Folder", "new.txt"))
	if err != nil {
		t.Fatalf("read completed upload: %v", err)
	}
	if !bytes.Equal(written, payload) {
		t.Fatalf("unexpected upload payload %q", written)
	}

	outsideDir := filepath.Join(server.cfg.NASRoot, "outside-upload")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside upload dir: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(project.RootPath, "LinkedOutside")); err != nil {
		t.Fatalf("create outside symlink: %v", err)
	}
	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/transfers", map[string]any{
		"direction":    "upload",
		"projectId":    project.ID,
		"deviceId":     device.ID,
		"relativePath": "LinkedOutside/escape.txt",
		"baseRevision": float64(0),
		"size":         float64(len(payload)),
		"chunkSize":    float64(len(payload)),
		"sha256":       hex.EncodeToString(sum[:]),
	}, helper.ID, token)
	if response.Code != http.StatusCreated {
		t.Fatalf("create symlink transfer: got %d: %s", response.Code, response.Body.String())
	}
	var symlinkTransfer struct {
		Transfer struct {
			ID string `json:"id"`
		} `json:"transfer"`
	}
	decodeSyncResponse(t, response, &symlinkTransfer)
	chunkReq = httptest.NewRequest(http.MethodPut, "/api/sync/transfers/"+symlinkTransfer.Transfer.ID+"/chunks/0", bytes.NewReader(payload))
	chunkReq.Header.Set("Authorization", "Bearer "+token)
	chunkReq.Header.Set("X-Roughdash-Helper-ID", helper.ID)
	chunkResp = httptest.NewRecorder()
	handler.ServeHTTP(chunkResp, chunkReq)
	if chunkResp.Code != http.StatusOK {
		t.Fatalf("write symlink chunk: got %d: %s", chunkResp.Code, chunkResp.Body.String())
	}
	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/transfers/"+symlinkTransfer.Transfer.ID+"/complete", nil, helper.ID, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected symlink parent upload to be rejected, got %d: %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink parent upload wrote outside project root or stat failed unexpectedly: %v", err)
	}
}

func TestSyncTransferCreatesConflictForStaleBaseRevision(t *testing.T) {
	server := newSyncTestServer(t)
	handler := server.Handler()
	ctx := context.Background()

	helper, token := createSyncTestHelper(t, server, "conflict-helper")
	project, item := createSyncTestProjectAndItem(t, server)
	device, err := server.store.CreateSyncDevice(ctx, models.SyncDevice{
		HelperID:      helper.ID,
		MachineID:     "conflict-mac",
		Name:          "Conflict Mac",
		Platform:      "darwin",
		SSDVolumeUUID: "conflict-volume",
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if _, err := server.store.AcquireSyncLease(ctx, device.ID, device.SSDVolumeUUID, time.Minute, false); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	response := doSyncJSON(t, handler, http.MethodPost, "/api/sync/transfers", map[string]any{
		"direction":    "upload",
		"projectId":    project.ID,
		"deviceId":     device.ID,
		"itemId":       item.ID,
		"relativePath": item.RelativePath,
		"baseRevision": float64(item.Revision - 1),
		"size":         float64(4),
		"chunkSize":    float64(4),
		"sha256":       "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7",
	}, helper.ID, token)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected stale base conflict, got %d: %s", response.Code, response.Body.String())
	}
	conflicts, err := server.store.ListSyncConflicts(ctx, models.SyncConflictStatusOpen)
	if err != nil {
		t.Fatalf("list conflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].ItemID != item.ID {
		t.Fatalf("unexpected conflicts: %#v", conflicts)
	}
}

func TestSyncItemContentDownloadStreamsProjectFile(t *testing.T) {
	server := newSyncTestServer(t)
	handler := server.Handler()
	ctx := context.Background()

	helper, token := createSyncTestHelper(t, server, "download-helper")
	projectRoot := filepath.Join(server.cfg.NASRoot, "DownloadProject")
	if err := os.MkdirAll(filepath.Join(projectRoot, "Folder"), 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	payload := []byte("roughdash hydrate payload\n")
	if err := os.WriteFile(filepath.Join(projectRoot, "Folder", "readme.txt"), payload, 0o644); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	sum := sha256.Sum256(payload)
	project, err := server.store.CreateSyncProject(ctx, models.SyncProject{
		Name:         "DownloadProject",
		RootPath:     projectRoot,
		Enabled:      true,
		IgnorePolicy: "default-system-junk",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	item, err := server.store.UpsertSyncItem(ctx, models.SyncItem{
		ProjectID:    project.ID,
		ParentID:     "root",
		RelativePath: "Folder/readme.txt",
		Name:         "readme.txt",
		Kind:         models.SyncItemKindFile,
		Size:         int64(len(payload)),
		ContentHash:  hex.EncodeToString(sum[:]),
		MetadataHash: "metadata",
		Revision:     7,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/sync/projects/"+project.ID+"/items/"+item.ID+"/content", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Roughdash-Helper-ID", helper.ID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("download content: got %d: %s", response.Code, response.Body.String())
	}
	if !bytes.Equal(response.Body.Bytes(), payload) {
		t.Fatalf("download payload mismatch: %q", response.Body.String())
	}
	if got := response.Header().Get("X-Roughdash-Content-Hash"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected hash header: %q", got)
	}
	if got := response.Header().Get("X-Roughdash-Revision"); got != "7" {
		t.Fatalf("unexpected revision header: %q", got)
	}
}

func TestSyncProjectScanCatalogsDirectNASFiles(t *testing.T) {
	server := newSyncTestServer(t)
	handler := server.Handler()
	ctx := context.Background()

	helper, token := createSyncTestHelper(t, server, "scan-helper")
	projectRoot := filepath.Join(server.cfg.NASRoot, "ScanProject")
	if err := os.MkdirAll(filepath.Join(projectRoot, "Folder"), 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "Folder", "nas-drop.txt"), []byte("direct nas file\n"), 0o644); err != nil {
		t.Fatalf("write nas file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}
	project, err := server.store.CreateSyncProject(ctx, models.SyncProject{
		Name:         "ScanProject",
		RootPath:     projectRoot,
		Enabled:      true,
		IgnorePolicy: "default-system-junk",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	response := doSyncJSON(t, handler, http.MethodPost, "/api/sync/projects/"+project.ID+"/scan", nil, helper.ID, token)
	if response.Code != http.StatusOK {
		t.Fatalf("scan project: got %d: %s", response.Code, response.Body.String())
	}
	var scan struct {
		Items   []models.SyncItem `json:"items"`
		Created int               `json:"created"`
		Deleted int               `json:"deleted"`
		Skipped int               `json:"skipped"`
	}
	decodeSyncResponse(t, response, &scan)
	if scan.Created != 2 || scan.Skipped != 1 {
		t.Fatalf("unexpected scan counts: %#v", scan)
	}
	var folder, file *models.SyncItem
	for i := range scan.Items {
		switch scan.Items[i].RelativePath {
		case "Folder":
			folder = &scan.Items[i]
		case "Folder/nas-drop.txt":
			file = &scan.Items[i]
		case ".DS_Store":
			t.Fatalf("ignored file was cataloged: %#v", scan.Items[i])
		}
	}
	if folder == nil || file == nil {
		t.Fatalf("expected folder and file in scan: %#v", scan.Items)
	}
	if file.ParentID != folder.ID || file.Revision != 1 {
		t.Fatalf("unexpected scanned file metadata: folder=%#v file=%#v", folder, file)
	}

	if err := os.WriteFile(filepath.Join(projectRoot, "Folder", "nas-drop.txt"), []byte("direct nas file edited\n"), 0o644); err != nil {
		t.Fatalf("edit nas file: %v", err)
	}
	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/projects/"+project.ID+"/scan", nil, helper.ID, token)
	if response.Code != http.StatusOK {
		t.Fatalf("rescan project: got %d: %s", response.Code, response.Body.String())
	}
	decodeSyncResponse(t, response, &scan)
	for i := range scan.Items {
		if scan.Items[i].RelativePath == "Folder/nas-drop.txt" {
			file = &scan.Items[i]
		}
	}
	if file == nil || file.Revision != 2 {
		t.Fatalf("expected edited file revision 2, got %#v", file)
	}

	if err := os.Remove(filepath.Join(projectRoot, "Folder", "nas-drop.txt")); err != nil {
		t.Fatalf("delete nas file: %v", err)
	}
	response = doSyncJSON(t, handler, http.MethodPost, "/api/sync/projects/"+project.ID+"/scan", nil, helper.ID, token)
	if response.Code != http.StatusOK {
		t.Fatalf("scan deleted project file: got %d: %s", response.Code, response.Body.String())
	}
	decodeSyncResponse(t, response, &scan)
	if scan.Deleted != 1 {
		t.Fatalf("expected one deleted item, got %#v", scan)
	}
	var deletedFile *models.SyncItem
	for i := range scan.Items {
		if scan.Items[i].RelativePath == "Folder/nas-drop.txt" {
			deletedFile = &scan.Items[i]
		}
	}
	if deletedFile == nil || !deletedFile.Tombstoned || deletedFile.Revision != 3 {
		t.Fatalf("expected tombstoned revision 3 item, got %#v", deletedFile)
	}

	var listed struct {
		Items []models.SyncItem `json:"items"`
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/sync/projects/"+project.ID+"/items?recursive=true", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listReq.Header.Set("X-Roughdash-Helper-ID", helper.ID)
	listResp := httptest.NewRecorder()
	handler.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list recursive: got %d: %s", listResp.Code, listResp.Body.String())
	}
	decodeSyncResponse(t, listResp, &listed)
	if len(listed.Items) != len(scan.Items) {
		t.Fatalf("recursive list did not return scanned items: got %#v want %#v", listed.Items, scan.Items)
	}
}

func TestSyncItemContentDownloadRejectsInvalidCatalogItems(t *testing.T) {
	server := newSyncTestServer(t)
	handler := server.Handler()
	ctx := context.Background()

	helper, token := createSyncTestHelper(t, server, "invalid-download-helper")
	projectRoot := filepath.Join(server.cfg.NASRoot, "InvalidDownloadProject")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	project, err := server.store.CreateSyncProject(ctx, models.SyncProject{
		Name:         "InvalidDownloadProject",
		RootPath:     projectRoot,
		Enabled:      true,
		IgnorePolicy: "default-system-junk",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	directoryItem, err := server.store.UpsertSyncItem(ctx, models.SyncItem{
		ProjectID:    project.ID,
		ParentID:     "root",
		RelativePath: "Folder",
		Name:         "Folder",
		Kind:         models.SyncItemKindDirectory,
		ContentHash:  "dir",
		MetadataHash: "dir",
		Revision:     1,
	})
	if err != nil {
		t.Fatalf("create directory item: %v", err)
	}
	tombstonedItem, err := server.store.UpsertSyncItem(ctx, models.SyncItem{
		ProjectID:    project.ID,
		ParentID:     "root",
		RelativePath: "deleted.txt",
		Name:         "deleted.txt",
		Kind:         models.SyncItemKindFile,
		ContentHash:  "deleted",
		MetadataHash: "deleted",
		Revision:     2,
		Tombstoned:   true,
	})
	if err != nil {
		t.Fatalf("create tombstoned item: %v", err)
	}
	escapingItem, err := server.store.UpsertSyncItem(ctx, models.SyncItem{
		ProjectID:    project.ID,
		ParentID:     "root",
		RelativePath: "../escape.txt",
		Name:         "escape.txt",
		Kind:         models.SyncItemKindFile,
		ContentHash:  "escape",
		MetadataHash: "escape",
		Revision:     3,
	})
	if err != nil {
		t.Fatalf("create escaping item: %v", err)
	}
	outsidePath := filepath.Join(server.cfg.NASRoot, "outside-download.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(projectRoot, "linked-outside.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	symlinkItem, err := server.store.UpsertSyncItem(ctx, models.SyncItem{
		ProjectID:    project.ID,
		ParentID:     "root",
		RelativePath: "linked-outside.txt",
		Name:         "linked-outside.txt",
		Kind:         models.SyncItemKindFile,
		ContentHash:  "linked",
		MetadataHash: "linked",
		Revision:     4,
	})
	if err != nil {
		t.Fatalf("create symlink item: %v", err)
	}

	assertDownloadStatus := func(itemID string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/sync/projects/"+project.ID+"/items/"+itemID+"/content", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Roughdash-Helper-ID", helper.ID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("download %s: got %d want %d: %s", itemID, response.Code, want, response.Body.String())
		}
	}

	assertDownloadStatus(directoryItem.ID, http.StatusBadRequest)
	assertDownloadStatus(tombstonedItem.ID, http.StatusGone)
	assertDownloadStatus(escapingItem.ID, http.StatusBadRequest)
	assertDownloadStatus(symlinkItem.ID, http.StatusBadRequest)
}

func TestSyncDisposableNASPublicAPISmoke(t *testing.T) {
	server := newSyncTestServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	baseClient := httpServer.Client()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	userClient := &http.Client{Transport: baseClient.Transport, CheckRedirect: baseClient.CheckRedirect, Jar: jar}
	helperClient := &http.Client{Transport: baseClient.Transport, CheckRedirect: baseClient.CheckRedirect}

	postHTTPJSON(t, userClient, http.MethodPost, httpServer.URL+"/api/setup", nil, map[string]any{
		"bootstrapSecret": "bootstrap",
		"username":        "admin",
		"password":        "correct horse battery staple",
	}, http.StatusCreated, nil)

	projectRoot := filepath.Join(server.cfg.NASRoot, "DisposableProject")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	var createdProject struct {
		Project models.SyncProject `json:"project"`
	}
	postHTTPJSON(t, userClient, http.MethodPost, httpServer.URL+"/api/sync/projects", nil, map[string]any{
		"name":     "DisposableProject",
		"rootPath": projectRoot,
		"enabled":  true,
	}, http.StatusCreated, &createdProject)

	helperID, helperToken := pairSmokeHelper(t, userClient, httpServer.URL)
	helperHeaders := map[string]string{
		"Authorization":         "Bearer " + helperToken,
		"X-Roughdash-Helper-ID": helperID,
	}

	var createdDevice struct {
		Device models.SyncDevice `json:"device"`
	}
	postHTTPJSON(t, helperClient, http.MethodPost, httpServer.URL+"/api/sync/devices", helperHeaders, map[string]any{
		"machineId":          "smoke-mac",
		"name":               "Smoke Mac",
		"platform":           "darwin",
		"ssdVolumeUuid":      "smoke-volume",
		"lastConnectionMode": models.SyncConnectionModeLANDirect,
	}, http.StatusCreated, &createdDevice)
	if createdDevice.Device.HelperID != helperID {
		t.Fatalf("device helper binding mismatch: %#v", createdDevice.Device)
	}

	postHTTPJSON(t, helperClient, http.MethodPost, httpServer.URL+"/api/sync/devices/"+createdDevice.Device.ID+"/lease", helperHeaders, map[string]any{
		"ssdVolumeUuid": "smoke-volume",
		"ttlSeconds":    60,
	}, http.StatusOK, nil)

	payload := []byte("roughdash disposable smoke upload\n")
	sum := sha256.Sum256(payload)
	uploadRequest := map[string]any{
		"direction":    "upload",
		"projectId":    createdProject.Project.ID,
		"deviceId":     createdDevice.Device.ID,
		"relativePath": "Safe/file.txt",
		"baseRevision": 0,
		"size":         len(payload),
		"chunkSize":    len(payload),
		"sha256":       hex.EncodeToString(sum[:]),
	}

	postHTTPJSON(t, helperClient, http.MethodPost, httpServer.URL+"/api/sync/transfers", helperHeaders, map[string]any{
		"direction":    "upload",
		"projectId":    createdProject.Project.ID,
		"deviceId":     createdDevice.Device.ID,
		"relativePath": "../escape.txt",
		"baseRevision": 0,
		"size":         len(payload),
		"chunkSize":    len(payload),
		"sha256":       hex.EncodeToString(sum[:]),
	}, http.StatusBadRequest, nil)
	if _, err := os.Stat(filepath.Join(projectRoot, "..", "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("escape upload wrote outside project root or stat failed unexpectedly: %v", err)
	}

	var createdTransfer struct {
		Transfer struct {
			ID string `json:"id"`
		} `json:"transfer"`
	}
	postHTTPJSON(t, helperClient, http.MethodPost, httpServer.URL+"/api/sync/transfers", helperHeaders, uploadRequest, http.StatusCreated, &createdTransfer)

	putRaw(t, helperClient, httpServer.URL+"/api/sync/transfers/"+createdTransfer.Transfer.ID+"/chunks/0", helperHeaders, payload, http.StatusOK)

	var completed struct {
		Path     string              `json:"path"`
		Item     models.SyncItem     `json:"item"`
		Revision models.SyncRevision `json:"revision"`
	}
	postHTTPJSON(t, helperClient, http.MethodPost, httpServer.URL+"/api/sync/transfers/"+createdTransfer.Transfer.ID+"/complete", helperHeaders, nil, http.StatusOK, &completed)

	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	expectedPath := filepath.Join(resolvedProjectRoot, "Safe", "file.txt")
	if completed.Path != expectedPath {
		t.Fatalf("unexpected completed path: got %q want %q", completed.Path, expectedPath)
	}
	if completed.Item.RelativePath != "Safe/file.txt" || completed.Item.Revision != 1 {
		t.Fatalf("unexpected completed item metadata: %#v", completed.Item)
	}
	if completed.Revision.Operation != models.SyncRevisionOperationUpload || completed.Revision.DeviceID != createdDevice.Device.ID {
		t.Fatalf("unexpected completed revision: %#v", completed.Revision)
	}
	written, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if !bytes.Equal(written, payload) {
		t.Fatalf("uploaded payload mismatch: %q", written)
	}
	assertNoTransferPartFiles(t, server.cfg.TempDir)
	assertNoTransferPartFiles(t, projectRoot)

	postHTTPJSON(t, helperClient, http.MethodPost, httpServer.URL+"/api/sync/transfers", helperHeaders, uploadRequest, http.StatusConflict, nil)
	var listedConflicts struct {
		Conflicts []models.SyncConflict `json:"conflicts"`
	}
	getHTTPJSON(t, helperClient, httpServer.URL+"/api/sync/conflicts?status=open", helperHeaders, http.StatusOK, &listedConflicts)
	if len(listedConflicts.Conflicts) != 1 || listedConflicts.Conflicts[0].ItemID != completed.Item.ID {
		t.Fatalf("expected one conflict for uploaded item, got %#v", listedConflicts.Conflicts)
	}
}

func newSyncTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	nasRoot := filepath.Join(root, "nas")
	if err := os.MkdirAll(nasRoot, 0o755); err != nil {
		t.Fatalf("mkdir nas root: %v", err)
	}
	server, err := New(context.Background(), config.Config{
		DataDir:              dataDir,
		DBPath:               filepath.Join(dataDir, "roughdash.sqlite"),
		TempDir:              filepath.Join(dataDir, "tmp"),
		StaticDir:            filepath.Join(root, "static"),
		NASRoot:              nasRoot,
		SessionCookieName:    "roughdash_test_session",
		SessionTTL:           time.Hour,
		LoginChallengeTTL:    time.Minute,
		BootstrapSecret:      "bootstrap",
		HelperPairingTTL:     time.Minute,
		HelperRequestTimeout: time.Second,
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Close()
	})
	return server
}

func createSyncTestHelper(t *testing.T, server *Server, machineID string) (*models.Helper, string) {
	t.Helper()
	token := machineID + "-token"
	helper, err := server.store.CreateHelper(context.Background(), machineID, machineID, "darwin", auth.HashToken(token))
	if err != nil {
		t.Fatalf("create helper: %v", err)
	}
	return helper, token
}

func createSyncTestProjectAndItem(t *testing.T, server *Server) (*models.SyncProject, *models.SyncItem) {
	t.Helper()
	ctx := context.Background()
	projectRoot := filepath.Join(server.cfg.NASRoot, "Projects")
	if err := os.MkdirAll(filepath.Join(projectRoot, "Folder"), 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	project, err := server.store.CreateSyncProject(ctx, models.SyncProject{
		Name:         "Projects",
		RootPath:     projectRoot,
		Enabled:      true,
		IgnorePolicy: "default-system-junk",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	item, err := server.store.UpsertSyncItem(ctx, models.SyncItem{
		ProjectID:    project.ID,
		ParentID:     "root",
		RelativePath: "existing.txt",
		Name:         "existing.txt",
		Kind:         models.SyncItemKindFile,
		Size:         8,
		ContentHash:  "old",
		MetadataHash: "old-meta",
		Revision:     2,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	return project, item
}

func doSyncJSON(t *testing.T, handler http.Handler, method, target string, body any, helperID, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Roughdash-Helper-ID", helperID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeSyncResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

type smokeProtocolEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func pairSmokeHelper(t *testing.T, client *http.Client, baseURL string) (string, string) {
	t.Helper()
	wsURL := strings.Replace(baseURL, "http", "ws", 1) + "/ws/helper"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial helper websocket: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	code := "123456"
	payload, err := json.Marshal(map[string]any{
		"machineId": "smoke-helper",
		"name":      "Smoke Helper",
		"platform":  "darwin",
		"code":      code,
	})
	if err != nil {
		t.Fatalf("marshal pair payload: %v", err)
	}
	if err := conn.WriteJSON(smokeProtocolEnvelope{Type: "pair.request", Payload: payload}); err != nil {
		t.Fatalf("send pair request: %v", err)
	}

	var pairResponse struct {
		Helper models.Helper `json:"helper"`
	}
	approvePendingPair(t, client, baseURL, code, &pairResponse)

	var envelope smokeProtocolEnvelope
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatalf("read pair confirmation: %v", err)
	}
	if envelope.Type != "pair.confirmed" {
		t.Fatalf("unexpected helper envelope: %#v", envelope)
	}
	var confirmation struct {
		HelperID string `json:"helperId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(envelope.Payload, &confirmation); err != nil {
		t.Fatalf("decode pair confirmation: %v", err)
	}
	if confirmation.HelperID == "" || confirmation.Token == "" || confirmation.HelperID != pairResponse.Helper.ID {
		t.Fatalf("invalid pair confirmation: confirmation=%#v response=%#v", confirmation, pairResponse.Helper)
	}
	return confirmation.HelperID, confirmation.Token
}

func approvePendingPair(t *testing.T, client *http.Client, baseURL, code string, target any) {
	t.Helper()
	var lastStatus int
	var lastBody string
	for attempt := 0; attempt < 50; attempt++ {
		status, body := postHTTPJSONStatus(t, client, http.MethodPost, baseURL+"/api/helpers/pair", nil, map[string]any{"code": code}, target)
		if status == http.StatusOK {
			return
		}
		lastStatus = status
		lastBody = string(body)
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("approve helper pair failed after retries: status=%d body=%s", lastStatus, lastBody)
}

func postHTTPJSON(t *testing.T, client *http.Client, method, targetURL string, headers map[string]string, body any, wantStatus int, target any) {
	t.Helper()
	status, responseBody := postHTTPJSONStatus(t, client, method, targetURL, headers, body, target)
	if status != wantStatus {
		t.Fatalf("%s %s: got status %d want %d: %s", method, targetURL, status, wantStatus, string(responseBody))
	}
}

func postHTTPJSONStatus(t *testing.T, client *http.Client, method, targetURL string, headers map[string]string, body any, target any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, targetURL, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, targetURL, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if target != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.Unmarshal(responseBody, target); err != nil {
			t.Fatalf("decode response %q: %v", responseBody, err)
		}
	}
	return response.StatusCode, responseBody
}

func getHTTPJSON(t *testing.T, client *http.Client, targetURL string, headers map[string]string, wantStatus int, target any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", targetURL, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("GET %s: got status %d want %d: %s", targetURL, response.StatusCode, wantStatus, string(body))
	}
	if target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			t.Fatalf("decode response %q: %v", body, err)
		}
	}
}

func putRaw(t *testing.T, client *http.Client, targetURL string, headers map[string]string, body []byte, wantStatus int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, targetURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("PUT %s: %v", targetURL, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("PUT %s: got status %d want %d: %s", targetURL, response.StatusCode, wantStatus, string(responseBody))
	}
}

func assertNoTransferPartFiles(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(entry.Name(), ".part") {
			t.Fatalf("leftover partial transfer file: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk temp dir: %v", err)
	}
}
