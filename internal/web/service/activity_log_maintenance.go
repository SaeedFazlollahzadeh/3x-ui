package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

var (
	activityLogMaintenanceMu  sync.Mutex
	errAccessSegmentUnchanged = errors.New("access segment unchanged")
	subscriptionBackupName    = regexp.MustCompile(`^subscription-login-logs_[0-9]{4}(?:_[0-9]{2}){5}\.jsonl$`)
)

// MaintainActivityLogs removes expired live entries.
// The two log types are independent, so an error in one does not block the other.
func MaintainActivityLogs(ctx context.Context, now time.Time) error {
	if !activityLogMaintenanceMu.TryLock() {
		return nil
	}
	defer activityLogMaintenanceMu.Unlock()

	settings := &SettingService{}
	accessRetention, err := settings.getInt("accessLogRetentionDays")
	if err != nil {
		return err
	}
	subRetention, err := settings.getInt("subscriptionLogRetentionDays")
	if err != nil {
		return err
	}
	now = now.UTC()
	accessErr := maintainAccessLogs(ctx, now, "", accessRetention, 0)
	subErr := maintainSubscriptionLogs(ctx, now, "", subRetention, 0)
	return errors.Join(accessErr, subErr)
}

func maintainAccessLogs(ctx context.Context, now time.Time, backupRoot string, retentionDays, backupDays int) error {
	if retentionDays <= 0 && backupDays <= 0 {
		return nil
	}
	path, err := xray.GetAccessLogPath()
	if errors.Is(err, os.ErrNotExist) || (err == nil && (path == "" || path == "none")) {
		if backupDays > 0 {
			return refreshAccessBackups(ctx, filepath.Join(backupRoot, "access-logs"), nil, now.AddDate(0, 0, -backupDays))
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := rotateAccessLog(ctx, path, now); err != nil {
		return err
	}
	accessLogFilesMu.Lock()
	defer accessLogFilesMu.Unlock()
	segments, err := accessSegments(accessArchiveDir(path))
	if err != nil {
		return err
	}

	if backupDays > 0 {
		if err := refreshAccessBackups(ctx, filepath.Join(backupRoot, "access-logs"), segments, now.AddDate(0, 0, -backupDays)); err != nil {
			return err
		}
	}
	if retentionDays > 0 {
		cutoff := now.AddDate(0, 0, -retentionDays)
		for _, segment := range segments {
			if err := filterAccessSegment(ctx, segment, segment, cutoff); err != nil {
				return err
			}
		}
	}
	return nil
}

func refreshAccessBackups(ctx context.Context, dir string, segments []string, cutoff time.Time) error {
	if len(segments) > 0 {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	backups, err := accessSegments(dir)
	if err != nil {
		return err
	}
	sources := make(map[string]string, len(backups))
	for _, backup := range backups {
		sources[backup] = backup
	}
	for _, source := range segments {
		target := filepath.Join(dir, filepath.Base(source))
		if sources[target] == "" {
			sources[target] = source
			continue
		}
		live, err := os.Stat(source)
		if err != nil {
			return err
		}
		saved, err := os.Stat(target)
		if err != nil {
			return err
		}
		// Both files contain an age-filtered subset of the same segment.
		// Take the larger subset so extending the backup window can use live data.
		if live.Size() > saved.Size() {
			sources[target] = source
		}
	}
	for target, source := range sources {
		if err := filterAccessSegment(ctx, source, target, cutoff); err != nil {
			return err
		}
	}
	return nil
}

func filterAccessSegment(ctx context.Context, source, target string, cutoff time.Time) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	kept := false
	err = writeActivityBackup(target, func(output io.Writer) error {
		reader := bufio.NewReaderSize(file, maxAccessLogLineBytes)
		continuation, expired, removed := false, false, false
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunk, err := reader.ReadSlice('\n')
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
				return err
			}
			if len(chunk) > 0 {
				if !continuation {
					expired = isInternalAccessLogLine(chunk) || accessLineOlderThan(chunk, cutoff)
				}
				if expired {
					removed = true
				} else {
					if _, err := output.Write(chunk); err != nil {
						return err
					}
					kept = true
				}
			}
			if errors.Is(err, io.EOF) {
				if source == target && kept && !removed {
					return errAccessSegmentUnchanged
				}
				return nil
			}
			continuation = errors.Is(err, bufio.ErrBufferFull)
		}
	})
	if errors.Is(err, errAccessSegmentUnchanged) {
		return nil
	}
	if err != nil {
		return err
	}
	if !kept {
		return os.Remove(target)
	}
	return nil
}

func accessLineOlderThan(line []byte, cutoff time.Time) bool {
	day, rest, ok := strings.Cut(string(line[:min(len(line), 40)]), " ")
	if !ok {
		return false
	}
	clock, _, _ := strings.Cut(rest, " ")
	at, err := time.ParseInLocation("2006/01/02 15:04:05.999999", day+" "+clock, time.Local)
	return err == nil && at.Before(cutoff)
}

func maintainSubscriptionLogs(ctx context.Context, now time.Time, backupDir string, retentionDays, backupDays int) error {
	if backupDays > 0 {
		if err := os.MkdirAll(backupDir, 0o700); err != nil {
			return err
		}
		if err := refreshSubscriptionBackup(ctx, now, backupDir, backupDays); err != nil {
			return err
		}
	}
	if retentionDays > 0 {
		cutoff := now.AddDate(0, 0, -retentionDays).UnixMilli()
		return database.GetDB().WithContext(ctx).Where("at < ?", cutoff).Delete(&model.ClientSubscriptionFetch{}).Error
	}
	return nil
}

func refreshSubscriptionBackup(ctx context.Context, now time.Time, dir string, backupDays int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	legacy := filepath.Join(dir, "subscription-login-logs.jsonl")
	previous := legacy
	var backups []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if subscriptionBackupName.MatchString(entry.Name()) {
			previous = path
			backups = append(backups, path)
		} else if path == legacy {
			backups = append(backups, path)
		}
	}
	target := filepath.Join(dir, activityBackupName("subscription-login-logs", ".jsonl", now))
	cutoff := now.AddDate(0, 0, -backupDays).UnixMilli()
	if err := writeActivityBackup(target, func(output io.Writer) error {
		return backupSubscriptionLogs(ctx, previous, cutoff, output)
	}); err != nil {
		return err
	}
	for _, backup := range backups {
		if backup != target {
			if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func backupSubscriptionLogs(ctx context.Context, path string, cutoff int64, output io.Writer) error {
	encoder := json.NewEncoder(output)
	var previous *json.Decoder
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		previous = json.NewDecoder(bufio.NewReader(file))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	nextPrevious := func() (*SubscriptionLogEntry, error) {
		for previous != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var row SubscriptionLogEntry
			err := previous.Decode(&row)
			if errors.Is(err, io.EOF) {
				previous = nil
				break
			}
			if err != nil {
				return nil, fmt.Errorf("read previous subscription backup: %w", err)
			}
			if row.At >= cutoff {
				return &row, nil
			}
		}
		return nil, nil
	}

	// Merge sorted IDs. PostgreSQL transactions can commit in a different order
	// than they allocated IDs, so a single high watermark can miss an event.
	saved, err := nextPrevious()
	if err != nil {
		return err
	}
	db := database.GetDB().WithContext(ctx).Model(&model.ClientSubscriptionFetch{})
	rows, err := db.Select("client_subscription_fetches.id, "+subscriptionLogEmail+" AS email, client_subscription_fetches.at, client_subscription_fetches.ip, client_subscription_fetches.user_agent").
		Joins("LEFT JOIN clients c ON c.id = client_subscription_fetches.client_id").
		Where("client_subscription_fetches.at >= ?", cutoff).
		Order("client_subscription_fetches.id").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	flushPrevious := func(beforeID int64) error {
		for saved != nil && (beforeID == 0 || saved.ID < beforeID) {
			if err := encoder.Encode(saved); err != nil {
				return err
			}
			saved, err = nextPrevious()
			if err != nil {
				return err
			}
		}
		return nil
	}
	for rows.Next() {
		var row SubscriptionLogEntry
		if err := db.ScanRows(rows, &row); err != nil {
			return err
		}
		if err := flushPrevious(row.ID); err != nil {
			return err
		}
		if saved != nil && saved.ID == row.ID {
			saved, err = nextPrevious()
			if err != nil {
				return err
			}
		}
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flushPrevious(0)
}

func writeActivityBackup(path string, write func(io.Writer) error) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".activity-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	buffer := bufio.NewWriter(file)
	if err := write(buffer); err != nil {
		return err
	}
	if err := buffer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
