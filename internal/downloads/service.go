package downloads

import (
	"bufio"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"roughdash/internal/config"
	"roughdash/internal/models"
	"roughdash/internal/system"
)

type Service struct {
	cfg config.Config
}

type ProgressUpdate struct {
	Fraction float64
	Message  string
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
	Name           string          `json:"name"`
	TargetPath     string          `json:"targetPath"`
	Videos         []ResolvedVideo `json:"videos"`
	Transcode      bool            `json:"transcode"`
	FetchSubtitles bool            `json:"fetchSubtitles"`
}

type PlannedGroup struct {
	Name           string   `json:"name"`
	TargetPath     string   `json:"targetPath"`
	Links          []string `json:"links"`
	Transcode      bool     `json:"transcode"`
	FetchSubtitles bool     `json:"fetchSubtitles"`
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

func (s *Service) Plan(_ context.Context, request models.DownloadRequest) (models.DownloadPreview, []PlannedGroup, int, error) {
	preview := models.DownloadPreview{}
	plannedGroups := make([]PlannedGroup, 0, len(request.Groups))
	linkCount := 0

	if len(request.Groups) == 0 {
		return preview, nil, 0, errors.New("at least one download group is required")
	}

	for _, group := range request.Groups {
		links := make([]string, 0, len(group.Links))
		seenLinks := make(map[string]struct{})
		for _, link := range group.Links {
			link = strings.TrimSpace(link)
			if link == "" {
				continue
			}
			if _, exists := seenLinks[link]; exists {
				continue
			}
			seenLinks[link] = struct{}{}
			links = append(links, link)
		}
		if len(links) == 0 {
			return preview, nil, 0, errors.New("each download group needs at least one link")
		}

		targetPath, err := resolveTargetPath(s.cfg.NASRoot, group.BasePath, group.NewFolder)
		if err != nil {
			return preview, nil, 0, err
		}
		if err := system.EnsureWithinRoot(s.cfg.NASRoot, targetPath); err != nil {
			return preview, nil, 0, err
		}

		name := strings.TrimSpace(group.Name)
		if name == "" {
			name = strings.TrimSpace(group.NewFolder)
		}
		if name == "" {
			name = filepath.Base(targetPath)
		}

		plannedGroup := PlannedGroup{
			Name:           name,
			TargetPath:     targetPath,
			Links:          links,
			Transcode:      group.Transcode,
			FetchSubtitles: group.FetchSubtitles,
		}
		plannedGroups = append(plannedGroups, plannedGroup)
		preview.Groups = append(preview.Groups, models.DownloadGroupPreview{
			Name:       name,
			TargetPath: targetPath,
		})
		linkCount += len(links)
	}

	return preview, plannedGroups, linkCount, nil
}

func (s *Service) Preview(ctx context.Context, request models.DownloadRequest) (models.DownloadPreview, []ResolvedGroup, error) {
	_, plannedGroups, _, err := s.Plan(ctx, request)
	if err != nil {
		return models.DownloadPreview{}, nil, err
	}

	preview, groups, warnings, err := s.ResolvePlanLenient(ctx, plannedGroups)
	if err != nil {
		return models.DownloadPreview{}, nil, err
	}
	preview.Warnings = warnings
	return preview, groups, nil
}

func (s *Service) ResolvePlan(ctx context.Context, plannedGroups []PlannedGroup) (models.DownloadPreview, []ResolvedGroup, error) {
	preview := models.DownloadPreview{}
	groups := make([]ResolvedGroup, 0, len(plannedGroups))
	for _, group := range plannedGroups {
		resolvedGroup, groupPreview, err := s.ResolvePlannedGroup(ctx, group)
		if err != nil {
			return preview, nil, err
		}
		groups = append(groups, resolvedGroup)
		preview.Groups = append(preview.Groups, groupPreview)
	}
	return preview, groups, nil
}

func (s *Service) ResolvePlanLenient(ctx context.Context, plannedGroups []PlannedGroup) (models.DownloadPreview, []ResolvedGroup, []models.DownloadWarning, error) {
	preview := models.DownloadPreview{}
	groups := make([]ResolvedGroup, 0, len(plannedGroups))
	warnings := []models.DownloadWarning{}
	for _, group := range plannedGroups {
		resolvedGroup, groupPreview, groupWarnings := s.ResolvePlannedGroupLenient(ctx, group)
		groups = append(groups, resolvedGroup)
		preview.Groups = append(preview.Groups, groupPreview)
		warnings = append(warnings, groupWarnings...)
	}
	if countResolvedVideos(groups) == 0 {
		if len(warnings) > 0 {
			return preview, groups, warnings, fmt.Errorf("no videos could be resolved; first error: %s", warnings[0].Message)
		}
		return preview, groups, warnings, errors.New("no videos could be resolved")
	}
	return preview, groups, warnings, nil
}

func (s *Service) ResolvePlannedGroup(ctx context.Context, group PlannedGroup) (ResolvedGroup, models.DownloadGroupPreview, error) {
	resolvedGroup := ResolvedGroup{
		Name:           group.Name,
		TargetPath:     group.TargetPath,
		Transcode:      group.Transcode,
		FetchSubtitles: group.FetchSubtitles,
	}
	groupPreview := models.DownloadGroupPreview{
		Name:       group.Name,
		TargetPath: group.TargetPath,
	}

	for _, link := range group.Links {
		items, err := s.resolveLink(ctx, link, group.TargetPath)
		if err != nil {
			return resolvedGroup, groupPreview, err
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

	return resolvedGroup, groupPreview, nil
}

func (s *Service) ResolvePlannedGroupLenient(ctx context.Context, group PlannedGroup) (ResolvedGroup, models.DownloadGroupPreview, []models.DownloadWarning) {
	resolvedGroup := ResolvedGroup{
		Name:           group.Name,
		TargetPath:     group.TargetPath,
		Transcode:      group.Transcode,
		FetchSubtitles: group.FetchSubtitles,
	}
	groupPreview := models.DownloadGroupPreview{
		Name:       group.Name,
		TargetPath: group.TargetPath,
	}
	warnings := []models.DownloadWarning{}

	for _, link := range group.Links {
		items, err := s.resolveLink(ctx, link, group.TargetPath)
		if err != nil {
			warnings = append(warnings, models.DownloadWarning{
				Link:    link,
				Message: err.Error(),
			})
			continue
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

	return resolvedGroup, groupPreview, warnings
}

func countResolvedVideos(groups []ResolvedGroup) int {
	count := 0
	for _, group := range groups {
		count += len(group.Videos)
	}
	return count
}

func (s *Service) DownloadVideo(ctx context.Context, video ResolvedVideo, tempDir string, onProgress func(ProgressUpdate)) (string, error) {
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return "", err
	}

	outputTemplate := filepath.Join(tempDir, "%(id)s.%(ext)s")
	args := []string{
		"--no-playlist",
		"--newline",
		"--progress-template", "download:roughdash-progress:%(progress.downloaded_bytes)s:%(progress.total_bytes)s:%(progress.total_bytes_estimate)s",
		"--print", "after_move:roughdash-output:%(filepath)s",
		"--output", outputTemplate,
		"--format", "bv*+ba/b",
		video.Link,
	}
	stdout, stderr, afterMovePath, err := runYTDLPStreaming(ctx, onProgress, args...)
	if err != nil {
		return "", fmt.Errorf("yt-dlp failed: %w: %s", err, stderr)
	}

	downloadedPath := strings.TrimSpace(afterMovePath)
	if downloadedPath == "" {
		downloadedPath = strings.TrimSpace(lastNonEmptyLine(stdout))
	}
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
		return "", errors.New("yt-dlp did not produce a video file")
	}
	return downloadedPath, nil
}

func (s *Service) DownloadSubtitles(ctx context.Context, video ResolvedVideo, tempDir string, onProgress func(ProgressUpdate)) (string, string, error) {
	if !video.SubtitlesAvailable {
		return "", "", nil
	}

	outputTemplate := filepath.Join(tempDir, "%(id)s.%(ext)s")
	args := []string{
		"--no-playlist",
		"--skip-download",
		"--ignore-errors",
		"--output", outputTemplate,
		"--write-subs",
		"--write-auto-subs",
		"--sub-langs", "all,-live_chat",
		"--convert-subs", "srt",
		video.Link,
	}
	_, stderr, _, err := runYTDLPStreaming(ctx, onProgress, args...)
	if err != nil {
		return "", "", fmt.Errorf("subtitle fetch failed: %w: %s", err, stderr)
	}

	matches, _ := filepath.Glob(filepath.Join(tempDir, video.VideoID+"*.srt"))
	warning := summarizeWarnings(stderr)
	if len(matches) == 0 {
		return "", warning, nil
	}
	return matches[0], warning, nil
}

func (s *Service) Transcode(ctx context.Context, sourcePath, subtitlePath, outputPath string, onProgress func(ProgressUpdate)) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return false, err
	}

	durationSeconds, err := probeDuration(ctx, sourcePath)
	if err != nil {
		durationSeconds = 0
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
		"-hide_banner",
		"-nostats",
		"-progress", "pipe:1",
		"-c:v", "hevc_nvenc",
		"-profile:v", "main",
		"-pix_fmt", "yuv420p",
		"-tag:v", "hvc1",
		"-preset", "p5",
		"-cq", "26",
		"-c:a", "aac",
		"-movflags", "+faststart",
		outputPath,
	)
	if onProgress != nil {
		onProgress(ProgressUpdate{Fraction: 0, Message: "Starting GPU transcode"})
	}
	if err := runFFmpegWithProgress(ctx, args, durationSeconds, onProgress); err != nil {
		args = []string{"-y", "-i", sourcePath}
		if subtitlePath != "" {
			args = append(args, "-i", subtitlePath)
		}
		args = append(args, "-map", "0:v:0", "-map", "0:a?")
		if subtitlePath != "" {
			args = append(args, "-map", "1:0", "-c:s", "mov_text")
		}
		args = append(args,
			"-hide_banner",
			"-nostats",
			"-progress", "pipe:1",
			"-c:v", "libx265",
			"-profile:v", "main",
			"-pix_fmt", "yuv420p",
			"-tag:v", "hvc1",
			"-preset", "medium",
			"-crf", "24",
			"-c:a", "aac",
			"-movflags", "+faststart",
			outputPath,
		)
		if onProgress != nil {
			onProgress(ProgressUpdate{Fraction: 0, Message: "GPU transcode unavailable, falling back to CPU"})
		}
		if err := runFFmpegWithProgress(ctx, args, durationSeconds, onProgress); err != nil {
			return false, err
		}
	}
	if onProgress != nil {
		onProgress(ProgressUpdate{Fraction: 1, Message: "Transcode finished"})
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
	filename := fmt.Sprintf("%s - %s - %s.mp4",
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
	stdout, stderr, err := runYTDLP(ctx, args...)
	if err != nil {
		return ytMetadata{}, fmt.Errorf("yt-dlp preflight failed: %w: %s", err, stderr)
	}
	var meta ytMetadata
	if err := json.Unmarshal([]byte(stdout), &meta); err != nil {
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

var invalidFilename = regexp.MustCompile(`[^a-zA-Z0-9 ._()-]+`)
var repeatedWhitespace = regexp.MustCompile(`\s+`)

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	value = invalidFilename.ReplaceAllString(value, " ")
	value = repeatedWhitespace.ReplaceAllString(value, " ")
	value = strings.Trim(value, " ._-")
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

func runYTDLP(ctx context.Context, args ...string) (string, string, error) {
	cmd := ytDLPCommand(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), stderr.String(), err
	}
	return stdout.String(), stderr.String(), nil
}

func runYTDLPStreaming(ctx context.Context, onProgress func(ProgressUpdate), args ...string) (string, string, string, error) {
	cmd := ytDLPCommand(ctx, args...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", "", err
	}
	if err := cmd.Start(); err != nil {
		return "", "", "", err
	}

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	var afterMovePath string
	var mu sync.Mutex
	streamProgress := func(line string) {
		if onProgress == nil {
			return
		}
		progress, ok := parseYTDLPProgress(line)
		if ok {
			onProgress(progress)
		}
	}
	streamLine := func(scanner *bufio.Scanner, target *bytes.Buffer) {
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			target.WriteString(line)
			target.WriteByte('\n')
			if strings.HasPrefix(strings.TrimSpace(line), "roughdash-output:") {
				afterMovePath = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "roughdash-output:"))
			}
			mu.Unlock()
			streamProgress(line)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamLine(bufio.NewScanner(stdoutPipe), &stdoutBuffer)
	}()
	go func() {
		defer wg.Done()
		streamLine(bufio.NewScanner(stderrPipe), &stderrBuffer)
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	return stdoutBuffer.String(), stderrBuffer.String(), afterMovePath, waitErr
}

func parseYTDLPProgress(line string) (ProgressUpdate, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "roughdash-progress:") {
		return ProgressUpdate{}, false
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) < 4 {
		return ProgressUpdate{}, false
	}
	downloaded, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	total, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	estimated, _ := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
	if total <= 0 {
		total = estimated
	}
	fraction := 0.0
	if total > 0 {
		fraction = downloaded / total
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	message := "Downloading video payload"
	if total > 0 {
		message = fmt.Sprintf("Downloading %s / %s", formatBinaryBytes(downloaded), formatBinaryBytes(total))
	} else if downloaded > 0 {
		message = fmt.Sprintf("Downloading %s", formatBinaryBytes(downloaded))
	}
	return ProgressUpdate{
		Fraction: fraction,
		Message:  message,
	}, true
}

func probeDuration(ctx context.Context, sourcePath string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		sourcePath,
	)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
}

func runFFmpegWithProgress(ctx context.Context, args []string, durationSeconds float64, onProgress func(ProgressUpdate)) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var stderrBuffer bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		progressData := map[string]string{}
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			progressData[parts[0]] = parts[1]
			if parts[0] == "progress" && onProgress != nil {
				if update, ok := parseFFmpegProgress(progressData, durationSeconds); ok {
					onProgress(update)
				}
				if parts[1] == "end" {
					onProgress(ProgressUpdate{Fraction: 1, Message: "Finalizing transcode"})
				}
				progressData = map[string]string{}
			}
		}
	}()

	go func() {
		defer wg.Done()
		ioScanner := bufio.NewScanner(stderrPipe)
		ioScanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for ioScanner.Scan() {
			stderrBuffer.WriteString(ioScanner.Text())
			stderrBuffer.WriteByte('\n')
		}
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	if waitErr != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", waitErr, stderrBuffer.String())
	}
	return nil
}

func parseFFmpegProgress(values map[string]string, durationSeconds float64) (ProgressUpdate, bool) {
	if durationSeconds <= 0 {
		return ProgressUpdate{}, false
	}
	outTime := values["out_time"]
	if outTime == "" {
		return ProgressUpdate{}, false
	}
	elapsedSeconds, err := parseFFmpegTimestamp(outTime)
	if err != nil {
		return ProgressUpdate{}, false
	}
	fraction := elapsedSeconds / durationSeconds
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	message := fmt.Sprintf("Transcoding %s / %s", formatDuration(elapsedSeconds), formatDuration(durationSeconds))
	if speed := strings.TrimSpace(values["speed"]); speed != "" {
		message = fmt.Sprintf("%s at %s", message, speed)
	}
	return ProgressUpdate{Fraction: fraction, Message: message}, true
}

func parseFFmpegTimestamp(value string) (float64, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid ffmpeg timestamp %q", value)
	}
	hours, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, err
	}
	return (hours * 3600) + (minutes * 60) + seconds, nil
}

func formatBinaryBytes(value float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

func formatDuration(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	duration := time.Duration(seconds * float64(time.Second)).Round(time.Second)
	hours := int(duration / time.Hour)
	duration -= time.Duration(hours) * time.Hour
	minutes := int(duration / time.Minute)
	duration -= time.Duration(minutes) * time.Minute
	secs := int(duration / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
}

func summarizeWarnings(stderr string) string {
	lines := strings.Split(stderr, "\n")
	var warnings []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "WARNING:"):
			warnings = append(warnings, trimmed)
		case strings.HasPrefix(trimmed, "ERROR:"):
			warnings = append(warnings, trimmed)
		}
	}
	return strings.Join(warnings, " ")
}

func ytDLPCommand(ctx context.Context, args ...string) *exec.Cmd {
	if runtime.GOOS == "linux" {
		return exec.CommandContext(ctx, "python3", append([]string{"-m", "yt_dlp"}, withJSRuntime(args...)...)...)
	}
	return exec.CommandContext(ctx, "yt-dlp", withJSRuntime(args...)...)
}

func withJSRuntime(args ...string) []string {
	if path, err := exec.LookPath("deno"); err == nil && path != "" {
		return append([]string{"--remote-components", "ejs:github", "--js-runtimes", fmt.Sprintf("deno:%s", path)}, args...)
	}
	if path, err := exec.LookPath("node"); err == nil && path != "" {
		return append([]string{"--remote-components", "ejs:github", "--js-runtimes", "node"}, args...)
	}
	if path, err := exec.LookPath("nodejs"); err == nil && path != "" {
		return append([]string{"--remote-components", "ejs:github", "--js-runtimes", fmt.Sprintf("node:%s", path)}, args...)
	}
	return args
}
