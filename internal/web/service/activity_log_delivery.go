package service

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

type activityAccessSegment struct {
	Name string
	Size int64
}

var nightlyActivityBackupName = regexp.MustCompile(`^activity-logs_[0-9]{4}(?:_[0-9]{2}){5}\.zip$`)

// BuildActivityLogDelivery publishes one ZIP, including entries from the
// previous nightly archive that are still within the configured windows.
func BuildActivityLogDelivery(ctx context.Context, now time.Time, manual bool) (string, string, error) {
	activityLogMaintenanceMu.Lock()
	defer activityLogMaintenanceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", "", err
	}

	settings := &SettingService{}
	accessDays, err := settings.getInt("accessLogBackupDays")
	if err != nil {
		return "", "", err
	}
	subscriptionDays, err := settings.getInt("subscriptionLogBackupDays")
	if err != nil {
		return "", "", err
	}
	if accessDays <= 0 && subscriptionDays <= 0 {
		return "", "", nil
	}

	return buildActivityLogBackup(ctx, config.GetActivityBackupFolder(), now, accessDays, subscriptionDays, manual)
}

func buildActivityLogBackup(ctx context.Context, root string, now time.Time, accessDays, subscriptionDays int, manual bool) (string, string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", err
	}
	stage, err := os.MkdirTemp(root, ".activity-backup-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(stage)
	if err := restoreActivityBackup(ctx, root, stage, accessDays > 0, subscriptionDays > 0); err != nil {
		return "", "", err
	}

	files := make(map[string]string)
	var accessPaths []string
	if accessDays > 0 {
		if err := maintainAccessLogs(ctx, now.UTC(), stage, 0, accessDays); err != nil {
			return "", "", err
		}
		paths, err := accessSegments(filepath.Join(stage, "access-logs"))
		if err != nil {
			return "", "", err
		}
		accessPaths = paths
	}
	if subscriptionDays > 0 {
		if err := maintainSubscriptionLogs(ctx, now.UTC(), stage, 0, subscriptionDays); err != nil {
			return "", "", err
		}
		filename := activityBackupName("subscription-login-logs", ".jsonl", now)
		files[filepath.Join(stage, filename)] = filename
	}
	prefix := "activity-logs"
	if manual {
		prefix += "-manual"
	}
	name := activityBackupName(prefix, ".zip", now)
	target := filepath.Join(root, name)
	for manual {
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return "", "", err
		}
		now = now.Add(time.Second)
		name = activityBackupName(prefix, ".zip", now)
		target = filepath.Join(root, name)
	}
	temp, err := buildActivityArchive(ctx, stage, files, accessPaths, now, accessDays > 0)
	if err != nil {
		return "", "", err
	}
	if err := os.Rename(temp, target); err != nil {
		return "", "", err
	}
	return target, name, nil
}

func restoreActivityBackup(ctx context.Context, root, stage string, access, subscription bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type().IsRegular() && nightlyActivityBackupName.MatchString(entry.Name()) {
			return restoreActivityArchive(ctx, filepath.Join(root, entry.Name()), stage, access, subscription)
		}
	}
	// Import the old hourly backup layout when creating the first nightly ZIP.
	if access {
		sources, err := accessSegments(filepath.Join(root, "access-logs"))
		if err != nil {
			return err
		}
		for _, source := range sources {
			if err := copyActivityBackup(ctx, source, filepath.Join(stage, "access-logs", filepath.Base(source))); err != nil {
				return err
			}
		}
	}
	if subscription {
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			name := entry.Name()
			if entry.Type().IsRegular() && (subscriptionBackupName.MatchString(name) || name == "subscription-login-logs.jsonl") {
				return copyActivityBackup(ctx, filepath.Join(root, name), filepath.Join(stage, name))
			}
		}
	}
	return nil
}

func restoreActivityArchive(ctx context.Context, path, stage string, access, subscription bool) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		segment, isAccess := strings.CutPrefix(entry.Name, "access-logs/")
		if access && !isAccess && accessSegmentName.MatchString(entry.Name) {
			if err := restoreCombinedAccessBackup(ctx, entry, archive.Comment, stage); err != nil {
				return err
			}
			continue
		}
		if !(access && isAccess && accessSegmentName.MatchString(segment)) &&
			!(subscription && subscriptionBackupName.MatchString(entry.Name)) {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		err = saveActivityBackup(ctx, filepath.Join(stage, entry.Name), reader)
		_ = reader.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func copyActivityBackup(ctx context.Context, source, target string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	return saveActivityBackup(ctx, target, file)
}

func saveActivityBackup(ctx context.Context, target string, reader io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return writeActivityBackup(target, func(w io.Writer) error {
		_, err := io.Copy(w, reader)
		return err
	})
}

// The ZIP comment records the sizes of the merged access segments so the
// next run can restore them by name and merge with live history without duplicates.
func restoreCombinedAccessBackup(ctx context.Context, entry *zip.File, comment, stage string) error {
	var segments []activityAccessSegment
	index, err := gzip.NewReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(comment)))
	if err != nil {
		return err
	}
	defer index.Close()
	if err := json.NewDecoder(index).Decode(&segments); err != nil {
		return err
	}
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, segment := range segments {
		if !accessSegmentName.MatchString(segment.Name) || segment.Size < 0 {
			return errors.New("invalid access backup segment")
		}
		part := &io.LimitedReader{R: reader, N: segment.Size}
		if err := saveActivityBackup(ctx, filepath.Join(stage, "access-logs", segment.Name), part); err != nil {
			return err
		}
		if part.N != 0 {
			return io.ErrUnexpectedEOF
		}
	}
	var extra [1]byte
	if n, err := io.ReadFull(reader, extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.Join(errors.New("invalid combined access backup"), err)
	}
	return nil
}

func buildActivityArchive(ctx context.Context, dir string, files map[string]string, accessPaths []string, now time.Time, includeAccess bool) (string, error) {
	temp, err := os.CreateTemp(dir, ".activity-delivery-*.zip")
	if err != nil {
		return "", err
	}
	archive := zip.NewWriter(temp)
	var copyErr error
	if includeAccess {
		copyErr = addActivityAccessLog(ctx, archive, accessPaths, now)
	}
	if copyErr == nil {
		for path, name := range files {
			entry, err := createActivityZIPEntry(archive, name, now)
			if err == nil {
				_, err = copyActivityArchiveFile(ctx, entry, path)
			}
			if err != nil {
				copyErr = err
				break
			}
		}
	}
	if err := errors.Join(copyErr, archive.Close(), temp.Close()); err != nil {
		_ = os.Remove(temp.Name())
		return "", err
	}
	return temp.Name(), nil
}

func addActivityAccessLog(ctx context.Context, archive *zip.Writer, paths []string, now time.Time) error {
	entry, err := createActivityZIPEntry(archive, activityBackupName("access-logs", ".log", now), now)
	if err != nil {
		return err
	}
	segments := make([]activityAccessSegment, 0, len(paths))
	for _, path := range paths {
		size, err := copyActivityArchiveFile(ctx, entry, path)
		if err != nil {
			return err
		}
		segments = append(segments, activityAccessSegment{Name: filepath.Base(path), Size: size})
	}
	var comment bytes.Buffer
	encoded := base64.NewEncoder(base64.StdEncoding, &comment)
	compressed := gzip.NewWriter(encoded)
	if err := errors.Join(json.NewEncoder(compressed).Encode(segments), compressed.Close(), encoded.Close()); err != nil {
		return err
	}
	return archive.SetComment(comment.String())
}

func createActivityZIPEntry(archive *zip.Writer, name string, now time.Time) (io.Writer, error) {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now.UTC()}
	header.SetMode(0o600)
	return archive.CreateHeader(header)
}

func copyActivityArchiveFile(ctx context.Context, entry io.Writer, path string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return io.Copy(entry, file)
}
