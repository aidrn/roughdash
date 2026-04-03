package system

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"roughdash/internal/models"
)

var (
	videoExts = []string{".mp4", ".mov", ".mxf", ".avi", ".mkv", ".mts", ".m2ts", ".webm"}
	photoExts = []string{".jpg", ".jpeg", ".png", ".raw", ".cr3", ".nef", ".arw", ".heic", ".tif", ".tiff"}
)

func EnsureWithinRoot(root, path string) error {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes approved root")
	}
	return nil
}

func ListEntries(path string) ([]models.FileEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var items []models.FileEntry
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		items = append(items, models.FileEntry{
			Name:    entry.Name(),
			Path:    filepath.Join(path, entry.Name()),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	slices.SortFunc(items, func(a, b models.FileEntry) int {
		switch {
		case a.IsDir && !b.IsDir:
			return -1
		case !a.IsDir && b.IsDir:
			return 1
		default:
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
	})
	return items, nil
}

func ExpandMediaPaths(paths []string) (files []string, skipped []models.FileSkip, err error) {
	seen := make(map[string]struct{})
	for _, input := range paths {
		info, statErr := os.Stat(input)
		if statErr != nil {
			skipped = append(skipped, models.FileSkip{Path: input, Reason: statErr.Error()})
			continue
		}
		if info.IsDir() {
			err = filepath.WalkDir(input, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					skipped = append(skipped, models.FileSkip{Path: path, Reason: walkErr.Error()})
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if !isSupportedMedia(path) {
					return nil
				}
				if _, ok := seen[path]; !ok {
					seen[path] = struct{}{}
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return nil, skipped, err
			}
			continue
		}
		if !isSupportedMedia(input) {
			skipped = append(skipped, models.FileSkip{Path: input, Reason: "unsupported file type"})
			continue
		}
		if _, ok := seen[input]; !ok {
			seen[input] = struct{}{}
			files = append(files, input)
		}
	}

	for _, path := range slices.Clone(files) {
		if !isVideo(path) {
			continue
		}
		xmlPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".xml"
		if _, err := os.Stat(xmlPath); err == nil {
			if _, ok := seen[xmlPath]; !ok {
				seen[xmlPath] = struct{}{}
				files = append(files, xmlPath)
			}
		}
	}

	slices.Sort(files)
	return files, skipped, nil
}

func IsVideo(path string) bool {
	return isVideo(path)
}

func IsPhoto(path string) bool {
	return isPhoto(path)
}

func BuildCameraDestination(root, sourcePath string, modTime time.Time) string {
	kind := "Photo"
	if isVideo(sourcePath) {
		kind = "Video"
	}
	year := modTime.Format("2006")
	date := modTime.Format("2006-01-02")
	return filepath.Join(root, "Camera", kind, year, date)
}

func ComputeSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func CopyFile(sourcePath, destinationPath string) error {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	input, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer input.Close()

	tempPath := destinationPath + ".part"
	output, err := os.Create(tempPath)
	if err != nil {
		return err
	}

	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, destinationPath)
}

func isSupportedMedia(path string) bool {
	return isVideo(path) || isPhoto(path) || strings.EqualFold(filepath.Ext(path), ".xml")
}

func isVideo(path string) bool {
	return slices.Contains(videoExts, strings.ToLower(filepath.Ext(path)))
}

func isPhoto(path string) bool {
	return slices.Contains(photoExts, strings.ToLower(filepath.Ext(path)))
}
