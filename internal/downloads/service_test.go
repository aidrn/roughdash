package downloads

import "testing"

func TestSanitizeFilename(t *testing.T) {
	got := sanitizeFilename("Channel / Name: Episode 1")
	if got != "Channel_Name_Episode_1" {
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
