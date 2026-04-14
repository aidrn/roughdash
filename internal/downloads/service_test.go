package downloads

import (
	"context"
	"errors"
	"testing"

	"roughdash/internal/config"
	"roughdash/internal/models"
)

func TestSanitizeFilename(t *testing.T) {
	got := sanitizeFilename("Channel / Name: Episode 1")
	if got != "Channel Name Episode 1" {
		t.Fatalf("unexpected sanitize result: %s", got)
	}
}

func TestQualityLabel(t *testing.T) {
	if got := qualityLabel(1080, 60); got != "1080p60" {
		t.Fatalf("unexpected quality label: %s", got)
	}
	if got := qualityLabel(0, 0); got != "BEST" {
		t.Fatalf("unexpected fallback label: %s", got)
	}
}

func TestPlanDoesNotResolveMetadata(t *testing.T) {
	service := NewService(config.Config{NASRoot: "/mnt/media"})
	preview, groups, linkCount, err := service.Plan(context.Background(), models.DownloadRequest{
		Groups: []models.DownloadGroup{
			{
				Name:      "Folder",
				BasePath:  "/mnt/media",
				NewFolder: "Folder",
				Links:     []string{" https://example.com/watch?v=abc123 ", "", "https://example.com/watch?v=abc123"},
				Transcode: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("unexpected link count: %d", linkCount)
	}
	if len(groups) != 1 {
		t.Fatalf("unexpected group count: %d", len(groups))
	}
	if groups[0].TargetPath != "/mnt/media/Folder" {
		t.Fatalf("unexpected target path: %s", groups[0].TargetPath)
	}
	if len(groups[0].Links) != 1 || groups[0].Links[0] != "https://example.com/watch?v=abc123" {
		t.Fatalf("unexpected planned links: %#v", groups[0].Links)
	}
	if len(preview.Groups) != 1 || len(preview.Groups[0].Videos) != 0 {
		t.Fatalf("preview should contain only lightweight group data: %#v", preview.Groups)
	}
}

func TestResolveLinkReusesSuccessfulSingleVideoMetadata(t *testing.T) {
	calls := 0
	items, err := resolveLinkWithLoader(context.Background(), "https://example.com/watch?v=abc123", "/mnt/media", func(_ context.Context, link string, noPlaylist bool) (ytMetadata, error) {
		calls++
		if noPlaylist {
			t.Fatalf("single-video metadata should be reused instead of probing %s again with --no-playlist", link)
		}
		return ytMetadata{
			ID:         "abc123",
			Title:      "Single video",
			WebpageURL: "https://example.com/watch?v=abc123",
			Uploader:   "Uploader",
			Height:     1080,
			FPS:        25,
		}, nil
	})
	if err != nil {
		t.Fatalf("resolve link failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one metadata call, got %d", calls)
	}
	if len(items) != 1 || items[0].VideoID != "abc123" {
		t.Fatalf("unexpected resolved items: %#v", items)
	}
}

func TestResolveLinkFallsBackToNoPlaylistWhenDiscoveryFails(t *testing.T) {
	var noPlaylistCalls int
	items, err := resolveLinkWithLoader(context.Background(), "https://example.com/watch?v=abc123", "/mnt/media", func(_ context.Context, _ string, noPlaylist bool) (ytMetadata, error) {
		if !noPlaylist {
			return ytMetadata{}, errors.New("discovery failed")
		}
		noPlaylistCalls++
		return ytMetadata{
			ID:       "abc123",
			Title:    "Single video",
			Uploader: "Uploader",
		}, nil
	})
	if err != nil {
		t.Fatalf("resolve link failed: %v", err)
	}
	if noPlaylistCalls != 1 {
		t.Fatalf("expected one fallback metadata call, got %d", noPlaylistCalls)
	}
	if len(items) != 1 || items[0].VideoID != "abc123" {
		t.Fatalf("unexpected resolved items: %#v", items)
	}
}
