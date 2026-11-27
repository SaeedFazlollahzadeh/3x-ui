package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestActivityAccessRetentionAndIndependentBackup(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.Local)
	line := func(at time.Time, destination string) string {
		return at.Format("2006/01/02 15:04:05") + " from 192.0.2.1:100 accepted " + destination
	}
	lines := []string{
		line(now.AddDate(0, 0, -8), "tcp:expired.example:443"),
		line(now.AddDate(0, 0, -3), "tcp:backup-only.example:443"),
		line(now.Add(-time.Hour), "tcp:recent.example:443"),
	}
	writeAccessLog(t, len(lines), func(i int) string { return lines[i] })
	root := t.TempDir()
	if err := maintainAccessLogs(ctx, now, root, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "access-logs")); !os.IsNotExist(err) {
		t.Fatalf("zero days must leave backups disabled: %v", err)
	}
	all, err := ReadAccessLogPage(ctx, ActivityLogFilter{})
	if err != nil || len(all.Rows) != 3 {
		t.Fatalf("zero retention should keep all rows: %+v, %v", all, err)
	}

	if err := maintainAccessLogs(ctx, now, root, 1, 7); err != nil {
		t.Fatal(err)
	}
	path, err := xray.GetAccessLogPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line(now.Add(time.Minute), "tcp:latest.example:443")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := maintainAccessLogs(ctx, now.Add(time.Hour), root, 1, 7); err != nil {
		t.Fatal(err)
	}
	page, err := ReadAccessLogPage(ctx, ActivityLogFilter{Limit: 1})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ToAddress != "tcp:latest.example:443" || page.NextBefore == 0 {
		t.Fatalf("latest access page: %+v, %v", page, err)
	}
	page, err = ReadAccessLogPage(ctx, ActivityLogFilter{Before: page.NextBefore})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ToAddress != "tcp:recent.example:443" {
		t.Fatalf("retained access page: %+v, %v", page, err)
	}
	backups, err := accessSegments(filepath.Join(root, "access-logs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 ||
		filepath.Base(backups[0]) != "access-logs_"+now.UTC().Format("2006_01_02_15_04_05")+".log" ||
		filepath.Base(backups[1]) != "access-logs_"+now.Add(time.Hour).UTC().Format("2006_01_02_15_04_05")+".log" {
		t.Fatalf("unexpected access backup names: %v", backups)
	}
	var backup strings.Builder
	for _, name := range backups {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		backup.Write(data)
	}
	if got := backup.String(); !strings.Contains(got, "backup-only.example") ||
		strings.Contains(got, "expired.example") || strings.Count(got, "recent.example") != 1 {
		t.Fatalf("unexpected access backup contents: %s", got)
	}
}

func TestActivitySubscriptionRetentionAndIndependentBackup(t *testing.T) {
	setupBulkDB(t)
	ctx := context.Background()
	db := database.GetDB()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	row := model.ClientSubscriptionFetch{ClientID: 100, Email: "deleted@example.com", At: now.AddDate(0, 0, -3).UnixMilli()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := maintainSubscriptionLogs(ctx, now, root, 0, 0); err != nil {
		t.Fatal(err)
	}
	page, err := ReadSubscriptionLogPage(ctx, ActivityLogFilter{})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Email != row.Email {
		t.Fatalf("zero retention should keep deleted clients' recorded entries: %+v, %v", page, err)
	}
	if err := maintainSubscriptionLogs(ctx, now, root, 1, 7); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ClientSubscriptionFetch{ClientID: 100, Email: row.Email, At: now.UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := maintainSubscriptionLogs(ctx, now.Add(time.Hour), root, 1, 7); err != nil {
		t.Fatal(err)
	}
	page, err = ReadSubscriptionLogPage(ctx, ActivityLogFilter{})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].At != now.UnixMilli() {
		t.Fatalf("live subscription rows: %+v, %v", page, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	const nextBackup = "subscription-login-logs_2026_11_27_13_00_00.jsonl"
	if len(entries) != 1 || entries[0].Name() != nextBackup {
		t.Fatalf("expected one timestamped rolling subscription backup, got %v", entries)
	}
	file, err := os.Open(filepath.Join(root, nextBackup))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var backups []SubscriptionLogEntry
	for decoder.More() {
		var item SubscriptionLogEntry
		if err := decoder.Decode(&item); err != nil {
			t.Fatal(err)
		}
		backups = append(backups, item)
	}
	if len(backups) != 2 || backups[0].ID != row.ID || backups[0].Email != row.Email {
		t.Fatalf("rolling subscription backup: %+v", backups)
	}
}

func TestActivityLogRotationReopensLiveXray(t *testing.T) {
	bin := os.Getenv("XRAY_E2E_BINARY")
	if bin == "" {
		t.Skip("set XRAY_E2E_BINARY to run against a real Xray core")
	}
	binDir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(binDir, xray.GetBinaryName())); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_BIN_FOLDER", binDir)
	t.Setenv("XUI_LOG_FOLDER", binDir)
	path := filepath.Join(binDir, "access.log")
	cfg := (balancerTestConfig{outbounds: []string{"direct", "proxy-a"}}).build(t, freeLoopbackPort(t))
	cfg.API = ensureAPIServices(cfg.API)
	cfg.LogConfig = mustJSON(t, map[string]string{"access": path, "loglevel": "warning"})
	cfg.OutboundConfigs = mustJSON(t, []map[string]any{
		{"protocol": "freedom", "tag": "direct", "settings": map[string]any{"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}},
		{"protocol": "freedom", "tag": "proxy-a"},
	})
	proxyPort := freeLoopbackPort(t)
	cfg.InboundConfigs = append(cfg.InboundConfigs, xray.InboundConfig{
		Listen: mustJSON(t, "127.0.0.1"), Port: proxyPort, Protocol: "http", Settings: mustJSON(t, map[string]any{}), Tag: "test",
	})
	process := xray.NewTestProcess(cfg, filepath.Join(binDir, "config.json"))
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	previousProcess, previousResult := xrayState.snapshot()
	xrayState.replace(process)
	t.Cleanup(func() {
		xrayState.mu.Lock()
		xrayState.process = previousProcess
		xrayState.result = previousResult
		xrayState.mu.Unlock()
	})

	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	proxyURL := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort))}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	ctx := context.Background()
	sendTraffic := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			response, err := client.Get(target.URL)
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusNoContent {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("Xray did not proxy the request: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitForRows := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			page, err := ReadAccessLogPage(ctx, ActivityLogFilter{Inbound: "test"})
			if err == nil && len(page.Rows) >= want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("access rows = %+v, error = %v, want %d", page.Rows, err, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	sendTraffic()
	waitForRows(1)
	if err := rotateAccessLog(ctx, path, time.Now()); err != nil {
		t.Fatal(err)
	}
	sendTraffic()
	waitForRows(2)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Xray did not reopen the active file: %v", err)
	}
}

func TestActivitySubscriptionBackupReplacesLegacyFilename(t *testing.T) {
	setupBulkDB(t)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	legacy := filepath.Join(dir, "subscription-login-logs.jsonl")
	row := SubscriptionLogEntry{ID: 10, Email: "saved@example.com", At: now.UnixMilli()}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refreshSubscriptionBackup(context.Background(), now, dir, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy file was not replaced: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "subscription-login-logs_2026_11_27_12_00_00.jsonl"))
	if err != nil || string(saved) != string(data)+"\n" {
		t.Fatalf("legacy data was not preserved: %s, %v", saved, err)
	}
}
