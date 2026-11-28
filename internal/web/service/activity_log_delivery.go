package service

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

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
	if accessDays > 0 {
		if err := maintainAccessLogs(ctx, now.UTC(), stage, 0, accessDays); err != nil {
			return "", "", err
		}
		paths, err := accessSegments(filepath.Join(stage, "access-logs"))
		if err != nil {
			return "", "", err
		}
		for _, path := range paths {
			files[path] = "access-logs/" + filepath.Base(path)
		}
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
	temp, err := buildActivityArchive(ctx, stage, files)
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

func buildActivityArchive(ctx context.Context, dir string, files map[string]string) (string, error) {
	temp, err := os.CreateTemp(dir, ".activity-delivery-*.zip")
	if err != nil {
		return "", err
	}
	archive := zip.NewWriter(temp)
	var copyErr error
	for path, name := range files {
		if copyErr = addActivityArchiveFile(ctx, archive, path, name); copyErr != nil {
			break
		}
	}
	if err := errors.Join(copyErr, archive.Close(), temp.Close()); err != nil {
		_ = os.Remove(temp.Name())
		return "", err
	}
	return temp.Name(), nil
}

func addActivityArchiveFile(ctx context.Context, archive *zip.Writer, path, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, file)
	return err
}
