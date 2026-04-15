package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"roughdash/internal/models"
)

func TestSyncProjectItemPinAndConflictLifecycle(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	project, err := store.CreateSyncProject(ctx, models.SyncProject{
		Name:         "Projects",
		RootPath:     filepath.Join(t.TempDir(), "Projects"),
		Enabled:      true,
		IgnorePolicy: "default-system-junk",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if project.ID == "" || !project.Enabled {
		t.Fatalf("unexpected project: %#v", project)
	}

	now := time.Now().UTC()
	item, err := store.UpsertSyncItem(ctx, models.SyncItem{
		ProjectID:    project.ID,
		ParentID:     "root",
		RelativePath: "Client/file.mov",
		Name:         "file.mov",
		Kind:         models.SyncItemKindFile,
		Size:         128,
		ModTime:      &now,
		ContentHash:  "abc",
		MetadataHash: "meta",
		Revision:     1,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	items, err := store.ListSyncItems(ctx, project.ID, "root")
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("unexpected items: %#v", items)
	}

	device, err := store.CreateSyncDevice(ctx, models.SyncDevice{
		MachineID:          "mac-1",
		Name:               "Studio Mac",
		Platform:           "darwin",
		SSDVolumeUUID:      "volume-1",
		LastConnectionMode: "LAN direct",
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	pin, err := store.UpsertSyncPin(ctx, models.SyncPin{
		ProjectID: project.ID,
		ItemID:    item.ID,
		DeviceID:  device.ID,
		Recursive: false,
	})
	if err != nil {
		t.Fatalf("upsert pin: %v", err)
	}
	if pin.Mode != models.SyncPinModeKeepDownloaded {
		t.Fatalf("unexpected pin mode: %#v", pin)
	}

	conflict, err := store.CreateSyncConflict(ctx, models.SyncConflict{
		ProjectID:    project.ID,
		ItemID:       item.ID,
		BaseRevision: 1,
		NASRevision:  2,
		SSDRevision:  2,
		Fields:       "content",
	})
	if err != nil {
		t.Fatalf("create conflict: %v", err)
	}
	openConflicts, err := store.ListSyncConflicts(ctx, models.SyncConflictStatusOpen)
	if err != nil {
		t.Fatalf("list conflicts: %v", err)
	}
	if len(openConflicts) != 1 || openConflicts[0].ID != conflict.ID {
		t.Fatalf("unexpected conflicts: %#v", openConflicts)
	}
	if err := store.ResolveSyncConflict(ctx, conflict.ID); err != nil {
		t.Fatalf("resolve conflict: %v", err)
	}
	openConflicts, _ = store.ListSyncConflicts(ctx, models.SyncConflictStatusOpen)
	if len(openConflicts) != 0 {
		t.Fatalf("expected no open conflicts: %#v", openConflicts)
	}
}

func TestSyncLeaseRequiresForceForActiveOtherDevice(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	first, err := store.CreateSyncDevice(ctx, models.SyncDevice{
		MachineID:     "mac-1",
		Name:          "Mac One",
		Platform:      "darwin",
		SSDVolumeUUID: "volume-1",
	})
	if err != nil {
		t.Fatalf("create first device: %v", err)
	}
	second, err := store.CreateSyncDevice(ctx, models.SyncDevice{
		MachineID:     "mac-2",
		Name:          "Mac Two",
		Platform:      "darwin",
		SSDVolumeUUID: "volume-1",
	})
	if err != nil {
		t.Fatalf("create second device: %v", err)
	}

	if _, err := store.AcquireSyncLease(ctx, first.ID, "volume-1", time.Hour, false); err != nil {
		t.Fatalf("first lease: %v", err)
	}
	held, err := store.AcquireSyncLease(ctx, second.ID, "volume-1", time.Hour, false)
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("expected lease held error, got lease=%#v err=%v", held, err)
	}
	taken, err := store.AcquireSyncLease(ctx, second.ID, "volume-1", time.Hour, true)
	if err != nil {
		t.Fatalf("forced takeover: %v", err)
	}
	if taken.DeviceID != second.ID {
		t.Fatalf("unexpected lease owner: %#v", taken)
	}
}

func TestGetActiveSyncLeaseRequiresMatchingDeviceAndUnexpiredLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	device, err := store.CreateSyncDevice(ctx, models.SyncDevice{
		MachineID:     "mac-1",
		Name:          "Mac One",
		Platform:      "darwin",
		SSDVolumeUUID: "volume-1",
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if _, err := store.GetActiveSyncLease(ctx, device.ID, device.SSDVolumeUUID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing lease before acquire, got %v", err)
	}
	if _, err := store.AcquireSyncLease(ctx, device.ID, device.SSDVolumeUUID, time.Minute, false); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if _, err := store.GetActiveSyncLease(ctx, device.ID, device.SSDVolumeUUID); err != nil {
		t.Fatalf("expected active lease: %v", err)
	}
	if _, err := store.AcquireSyncLease(ctx, device.ID, device.SSDVolumeUUID, -time.Minute, true); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if _, err := store.GetActiveSyncLease(ctx, device.ID, device.SSDVolumeUUID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected expired lease to be missing, got %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "roughdash.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
