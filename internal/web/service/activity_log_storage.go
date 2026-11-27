package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

var (
	accessLogFilesMu  sync.RWMutex
	accessSegmentName = regexp.MustCompile(`^(?:access-logs_[0-9]{4}(?:_[0-9]{2}){5}|[0-9]{20}-[0-9]{9})\.log$`)
)

func activityBackupName(prefix, extension string, at time.Time) string {
	return prefix + "_" + at.UTC().Format("2006_01_02_15_04_05") + extension
}

func accessArchiveDir(path string) string {
	return path + ".xui-history"
}

func accessSegments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && accessSegmentName.MatchString(entry.Name()) {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths, nil
}

// scanAccessLogs reads completed segments and the active file as a single stream.
// It opens one file at a time, even when years of history have been kept.
func scanAccessLogs(ctx context.Context, path string, before int64, visit func(string, int64)) error {
	accessLogFilesMu.RLock()
	defer accessLogFilesMu.RUnlock()
	paths, err := accessSegments(accessArchiveDir(path))
	if err != nil {
		return err
	}
	paths = append(paths, filepath.Join(accessArchiveDir(path), "pending"), path)
	var base int64
	for _, name := range paths {
		if before > 0 && base >= before {
			break
		}
		size, err := scanAccessSegment(ctx, name, base, before, visit)
		if err != nil {
			return err
		}
		base += size
	}
	return nil
}

func scanAccessSegment(ctx context.Context, path string, base, before int64, visit func(string, int64)) (int64, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if before > 0 {
		size = min(size, before-base)
	}
	section := io.NewSectionReader(file, 0, size)
	reader := bufio.NewReaderSize(section, maxAccessLogLineBytes)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		position, _ := section.Seek(0, io.SeekCurrent)
		offset := base + position - int64(reader.Buffered())
		line, err := readAccessLogLine(reader)
		if errors.Is(err, io.EOF) {
			return size, nil
		}
		if err != nil {
			return 0, err
		}
		visit(line, offset)
	}
}

// rotateAccessLog switches Xray to a fresh access file without stopping traffic.
// Pending is visible to readers and retried if the logger could not be reopened.
func rotateAccessLog(ctx context.Context, path string, now time.Time) error {
	lock.Lock()
	defer lock.Unlock()
	currentPath, err := xray.GetAccessLogPath()
	if err != nil {
		return err
	}
	if currentPath != path {
		return errors.New("Xray access log path changed during maintenance")
	}
	accessLogFilesMu.Lock()
	defer accessLogFilesMu.Unlock()
	dir := accessArchiveDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pending := filepath.Join(dir, "pending")
	destination := filepath.Join(dir, activityBackupName("access-logs", ".log", now))
	for at := now.Add(time.Second); ; at = at.Add(time.Second) {
		if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return err
		}
		destination = filepath.Join(dir, activityBackupName("access-logs", ".log", at))
	}
	if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() == 0) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("access log is not a regular file")
		}
		if process := currentXrayProcess(); process != nil && process.IsRunning() && process.GetAPIPort() <= 0 {
			return errors.New("Xray API must be enabled to rotate access logs")
		}
		if err := os.Rename(path, pending); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if process := currentXrayProcess(); process != nil && process.IsRunning() {
		var api xray.XrayAPI
		if err := api.Init(process.GetAPIPort()); err != nil {
			return err
		}
		defer api.Close()
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := api.RestartLogger(callCtx); err != nil {
			return fmt.Errorf("reopen Xray logs: %w", err)
		}
	}
	return os.Rename(pending, destination)
}
