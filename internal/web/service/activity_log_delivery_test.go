package service

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestActivityDeliveryArchivePreservesBackupNamesAndContents(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	want := map[string]string{
		"access-logs/access-logs_2026_11_28_00_00_00.log":   "access event\n",
		"subscription-login-logs_2026_11_28_00_00_00.jsonl": "{\"at\":123}\n",
	}
	for relative, text := range want {
		path := filepath.Join(root, filepath.Base(relative))
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		files[path] = relative
	}
	now := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	path, err := buildActivityArchive(context.Background(), root, files, nil, now, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if len(got.File) != len(want) {
		t.Fatalf("archive includes %d files", len(got.File))
	}
	for _, entry := range got.File {
		if !entry.Modified.Equal(now) {
			t.Fatalf("archive entry %q timestamp = %s, want %s", entry.Name, entry.Modified, now)
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || string(data) != want[entry.Name] {
			t.Fatalf("archive entry %q = %q, %v", entry.Name, data, err)
		}
	}
}

func TestActivityBackupSavesOneArchiveAndFiltersAPITraffic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 28, 0, 0, 0, 0, time.Local)
	at := now.Add(-time.Hour).Format("2006/01/02 15:04:05")
	client := at + " from 192.0.2.1:123 accepted tcp:example.com:443 [inbound >> direct] email: test"
	internal := at + " from 127.0.0.1:123 accepted tcp:127.0.0.1:62789 [api -> api]"
	writeAccessLog(t, 2, func(i int) string { return []string{client, internal}[i] })
	root := t.TempDir()

	nightly, _, err := buildActivityLogBackup(ctx, root, now, 2, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	older := at + " from 192.0.2.2:123 accepted tcp:older.example:443 [inbound >> direct] email: test"
	// Add a second live segment after the nightly archive was written.
	logPath, err := xray.GetAccessLogPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte(older+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manual, name, err := buildActivityLogBackup(ctx, root, now, 2, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if nightly == manual || !strings.HasPrefix(name, "activity-logs-manual_") {
		t.Fatalf("unexpected manual path %s", manual)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected only two ZIPs after two runs, got %v, %v", entries, err)
	}
	for _, path := range []string{nightly, manual} {
		archive, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer archive.Close()
		if len(archive.File) != 1 {
			t.Fatalf("unexpected entries in %s: %v", path, archive.File)
		}
		file, err := archive.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(file)
		_ = file.Close()
		want := client + "\n"
		if path == manual {
			want += older + "\n"
		}
		if err != nil || string(got) != want {
			t.Fatalf("unexpected log in %s: %q (%v)", path, got, err)
		}
	}
}
