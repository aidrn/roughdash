package downloads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"roughdash/internal/config"
	"roughdash/internal/models"
	"roughdash/internal/system"
)

type Service struct {
	cfg config.Config
}

type ytMetadata struct {
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	Channel           string         `json:"channel"`
	Uploader          string         `json:"uploader"`
	UploadDate        string         `json:"upload_date"`
	Description       string         `json:"description"`
	WebpageURL        string         `json:"webpage_url"`
	PlaylistTitle     string         `json:"playlist_title"`
	Height            int            `json:"height"`
	FPS               float64        `json:"fps"`
	Thumbnail         string         `json:"thumbnail"`
	Subtitles         map[string]any `json:"subtitles"`
	AutomaticCaptions map[string]any `json:"automatic_captions"`
	Entries           []ytMetadata   `json:"entries"`
}

type ResolvedGroup struct {
	Name       string          `json:"name"`
	TargetPath string          `json:"targetPath"`
	Videos     []ResolvedVideo `json:"videos"`
	Transcode  bool            `json:"transcode"`
}

type ResolvedVideo struct {
	Link               string `json:"link"`
	VideoID            string `json:"videoId"`
	Title              string `json:"title"`
	Uploader           string `json:"uploader"`
	UploadDate         string `json:"uploadDate"`
	Description        string `json:"description"`
	PlaylistTitle      string `json:"playlistTitle"`
	QualityLabel       string `json:"qualityLabel"`
	FinalPath          string `json:"finalPath"`
	ThumbnailAvailable bool   `json:"thumbnailAvailable"`
	SubtitlesAvailable bool   `json:"subtitlesAvailable"`
}

func NewService(cfg config.Config) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) Preview(ctx context.Context, request models.DownloadRequest) (models.DownloadPreview, []ResolvedGroup, error) {
	preview := models.DownloadPreview{}
	var groups []ResolvedGroup

	for _, group := range request.Groups {
		targetPath, err := resolveTargetPath(s.cfg.NASRoot, group.BasePath, group.NewFolder)
		if err != nil {
			return preview, nil, err
		}
		if err := system.EnsureWithinRoot(s.cfg.NASRoot, targetPath); err != nil {
			return preview, nil, err
		}

		resolvedGroup := ResolvedGroup{
			Name:       group.Name,
			TargetPath: targetPath,
			Transcode:  group.Transcode,
		}
		groupPreview := models.DownloadGroupPreview{
			Name:       group.Name,
			TargetPath: targetPath,
		}

		for _, link := range group.Links {
			items, err := s.resolveLink(ctx, link, targetPath)
			if err != nil {
				return preview, nil, err
			}
			resolvedGroup.Videos = append(resolvedGroup.Videos, items...)
			for _, item := range items {
				groupPreview.Videos = append(groupPreview.Videos, models.DownloadVideoPreview{
					Link:          item.Link,
					VideoID:       item.VideoID,
					Title:         item.Title,
					Uploader:      item.Uploader,
					PlaylistTitle: item.PlaylistTitle,
					QualityLabel:  item.QualityLabel,
					FinalPath:     item.FinalPath,
				})
			}
		}

		groups = append(groups, resolvedGroup)
		preview.Groups = append(preview.Groups, groupPreview)
	}

	return preview, groups, nil
}

func (s *Service) Download(ctx context.Context, group ResolvedGroup, video ResolvedVideo, tempDir string) (string, string, error) {
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return "", "", err
	}

	outputTemplate := filepath.Join(tempDir, "%(id)s.%(ext)s")
	args := []string{
		"--no-playlist",
		"--print", "after_move:filepath",
		"--paths", tempDir,
		"--output", outputTemplate,
		"--format", "bv*+ba/b",
		"--write-subs",
		"--write-auto-subs",
		"--sub-langs", "all,-live_chat",
		"--convert-subs", "srt",
		video.Link,
	}
	cmd := ytDLPCommand(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("yt-dlp failed: %w: %s", err, stderr.String())
	}

	downloadedPath := strings.TrimSpace(lastNonEmptyLine(stdout.String()))
	if downloadedPath == "" {
		matches, _ := filepath.Glob(filepath.Join(tempDir, video.VideoID+".*"))
		for _, match := range matches {
			if filepath.Ext(match) != ".srt" {
				downloadedPath = match
				break
			}
		}
	}
	if downloadedPath == "" {
		return "", "", errors.New("yt-dlp did not produce a video file")
	}

	subtitlePath := ""
	matches, _ := filepath.Glob(filepath.Join(tempDir, video.VideoID+"*.srt"))
	if len(matches) > 0 {
		subtitlePath = matches[0]
	}
	return downloadedPath, subtitlePath, nil
}

func (s *Service) Transcode(ctx context.Context, sourcePath, subtitlePath, outputPath string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return false, err
	}

	args := []string{"-y", "-i", sourcePath}
	subtitlesEmbedded := false
	if subtitlePath != "" {
		args = append(args, "-i", subtitlePath)
	}
	args = append(args, "-map", "0:v:0", "-map", "0:a?")
	if subtitlePath != "" {
		args = append(args, "-map", "1:0", "-c:s", "mov_text")
		subtitlesEmbedded = true
	}
	args = append(args,
		"-c:v", "hevc_nvenc",
		"-preset", "p5",
		"-cq", "26",
		"-c:a", "aac",
		"-movflags", "+faststart",
		outputPath,
	)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		args = []string{"-y", "-i", sourcePath}
		if subtitlePath != "" {
			args = append(args, "-i", subtitlePath)
		}
		args = append(args, "-map", "0:v:0", "-map", "0:a?")
		if subtitlePath != "" {
			args = append(args, "-map", "1:0", "-c:s", "mov_text")
		}
		args = append(args,
			"-c:v", "libx265",
			"-preset", "medium",
			"-crf", "24",
			"-c:a", "aac",
			"-movflags", "+faststart",
			outputPath,
		)
		cmd = exec.CommandContext(ctx, "ffmpeg", args...)
		stderr.Reset()
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("ffmpeg failed: %w: %s", err, stderr.String())
		}
	}
	return subtitlesEmbedded, nil
}

func (s *Service) resolveLink(ctx context.Context, link, targetPath string) ([]ResolvedVideo, error) {
	meta, err := loadMetadata(ctx, link, false)
	if err == nil && len(meta.Entries) > 0 {
		var results []ResolvedVideo
		for _, entry := range meta.Entries {
			entryLink := entry.WebpageURL
			if entryLink == "" && entry.ID != "" {
				entryLink = "https://www.youtube.com/watch?v=" + entry.ID
			}
			if entryLink == "" {
				continue
			}
			full, err := loadMetadata(ctx, entryLink, true)
			if err != nil {
				return nil, err
			}
			results = append(results, buildResolvedVideo(full, entryLink, targetPath))
		}
		return results, nil
	}

	full, err := loadMetadata(ctx, link, true)
	if err != nil {
		return nil, err
	}
	return []ResolvedVideo{buildResolvedVideo(full, link, targetPath)}, nil
}

func buildResolvedVideo(meta ytMetadata, link, targetPath string) ResolvedVideo {
	uploader := meta.Uploader
	if uploader == "" {
		uploader = meta.Channel
	}
	if uploader == "" {
		uploader = "Unknown"
	}
	title := meta.Title
	if title == "" {
		title = meta.ID
	}
	quality := qualityLabel(meta.Height, meta.FPS)
	filename := fmt.Sprintf("%s_%s_%s.mp4",
		sanitizeFilename(uploader),
		sanitizeFilename(title),
		sanitizeFilename(quality),
	)
	return ResolvedVideo{
		Link:               link,
		VideoID:            meta.ID,
		Title:              title,
		Uploader:           uploader,
		UploadDate:         meta.UploadDate,
		Description:        meta.Description,
		PlaylistTitle:      meta.PlaylistTitle,
		QualityLabel:       quality,
		FinalPath:          filepath.Join(targetPath, filename),
		ThumbnailAvailable: meta.Thumbnail != "",
		SubtitlesAvailable: len(meta.Subtitles) > 0 || len(meta.AutomaticCaptions) > 0,
	}
}

func loadMetadata(ctx context.Context, link string, noPlaylist bool) (ytMetadata, error) {
	args := []string{"--skip-download", "--dump-single-json", link}
	if noPlaylist {
		args = append([]string{"--no-playlist"}, args...)
	}
	cmd := ytDLPCommand(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ytMetadata{}, fmt.Errorf("yt-dlp preflight failed: %w: %s", err, stderr.String())
	}
	var meta ytMetadata
	if err := json.Unmarshal(stdout.Bytes(), &meta); err != nil {
		return ytMetadata{}, err
	}
	return meta, nil
}

func resolveTargetPath(nasRoot, basePath, newFolder string) (string, error) {
	if basePath == "" {
		return "", errors.New("base path is required")
	}
	target := basePath
	if newFolder != "" {
		target = filepath.Join(basePath, newFolder)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(nasRoot, target)
	}
	return filepath.Clean(target), nil
}

func qualityLabel(height int, fps float64) string {
	if height == 0 {
		return "BEST"
	}
	if fps >= 1 {
		return fmt.Sprintf("%dp%.0f", height, fps)
	}
	return fmt.Sprintf("%dp", height)
}

var invalidFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	value = invalidFilename.ReplaceAllString(value, "_")
	value = strings.Trim(value, "._")
	if value == "" {
		return "untitled"
	}
	return value
}

func lastNonEmptyLine(value string) string {
	lines := strings.Split(value, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ytDLPCommand(ctx context.Context, args ...string) *exec.Cmd {
	if runtime.GOOS == "linux" {
		return exec.CommandContext(ctx, "python3", append([]string{"-m", "yt_dlp"}, args...)...)
	}
	return exec.CommandContext(ctx, "yt-dlp", args...)
}
