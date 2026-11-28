package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestActivityAccessLogFiltersAndPagination(t *testing.T) {
	lines := []string{
		"2026/11/24 12:00:00 from 192.0.2.8:443 accepted tcp:early.example:443 [test-a >> direct] email: alice",
		"2026/11/25 12:00:00 from 192.0.2.8:443 accepted tcp:older.example:443 [test-a >> direct] email: alice",
		"2026/11/25 13:00:00 from 192.0.2.8:443 accepted tcp:elsewhere.example:443 [test-b >> direct] email: alice",
		"2026/11/25 14:00:00 from 192.0.2.8:443 rejected  authentication failed",
		"2026/11/25 15:00:00 from 192.0.2.8:443 accepted https://new.example/path?secret=x [test-a >> direct] email: ALICE",
	}
	writeAccessLog(t, len(lines), func(i int) string { return lines[i] })
	filter := ActivityLogFilter{
		Client: "alice", Inbound: "test-a", Search: "192.0.2.8", Status: "accepted",
		From: time.Date(2026, 11, 25, 0, 0, 0, 0, time.Local).UnixMilli(), Limit: 1,
	}
	page, err := ReadAccessLogPage(context.Background(), filter)
	if err != nil || !page.Enabled || len(page.Rows) != 1 || page.Rows[0].ToAddress != "new.example" || page.NextBefore == 0 {
		t.Fatalf("first page = %+v, error = %v", page, err)
	}
	filter.Before = page.NextBefore
	page, err = ReadAccessLogPage(context.Background(), filter)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ToAddress != "tcp:older.example:443" || page.NextBefore != 0 {
		t.Fatalf("second page = %+v, error = %v", page, err)
	}
	page, err = ReadAccessLogPage(context.Background(), ActivityLogFilter{Status: "rejected"})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Status != "rejected" || page.Rows[0].ToAddress != "" {
		t.Fatalf("rejected page = %+v, error = %v", page, err)
	}
}

func TestActivitySubscriptionLogFiltersAndPagination(t *testing.T) {
	const email = "alice@example.com"
	inbound := seedUsageHistoryClient(t, &InboundService{}, email, "b49e5c40-4586-4768-9482-72407f1c4c82", 0, 0)
	db := database.GetDB()
	var client model.ClientRecord
	if err := db.Where("email = ?", email).First(&client).Error; err != nil {
		t.Fatal(err)
	}
	rows := []model.ClientSubscriptionFetch{
		{ClientID: client.Id, At: 10, IP: "192.0.2.8", UserAgent: "Example VPN"},
		{ClientID: client.Id, At: 20, IP: "192.0.2.8", UserAgent: "Example VPN"},
		{ClientID: client.Id, At: 30, IP: "192.0.2.8", UserAgent: "Different app"},
		{ClientID: client.Id, At: 40, IP: "192.0.2.8", UserAgent: "Example VPN"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	filter := ActivityLogFilter{Client: "ALICE", Inbound: inbound.Tag, Search: "vpn", From: 10, To: 40, Limit: 1}
	page, err := ReadSubscriptionLogPage(context.Background(), filter)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].At != 20 || page.Rows[0].Email != email || page.NextBefore == 0 {
		t.Fatalf("first page = %+v, error = %v", page, err)
	}
	filter.Before = page.NextBefore
	page, err = ReadSubscriptionLogPage(context.Background(), filter)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].At != 10 || page.NextBefore != 0 {
		t.Fatalf("second page = %+v, error = %v", page, err)
	}
	filter.Inbound = "unattached"
	page, err = ReadSubscriptionLogPage(context.Background(), filter)
	if err != nil || len(page.Rows) != 0 {
		t.Fatalf("unattached inbound page = %+v, error = %v", page, err)
	}
}

func TestActivityAccessLogsShowReadableNames(t *testing.T) {
	const email = "random-client-id"
	inbound := seedUsageHistoryClient(t, &InboundService{}, email, "b49e5c40-4586-4768-9482-72407f1c4c82", 0, 0)
	db := database.GetDB()
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", email).Update("comment", "Alice’s phone").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", inbound.Id).Update("remark", "Tehran VLESS").Error; err != nil {
		t.Fatal(err)
	}
	writeAccessLog(t, 1, func(int) string {
		return "2026/11/27 17:37:42.909381 from 192.0.2.8:1234 accepted tcp:example.com:443 [" + inbound.Tag + " >> direct] email: " + email
	})

	ctx := context.Background()
	filter := ActivityLogFilter{Client: "phone", Inbound: "tehran"}
	page, err := ReadAccessLogPage(ctx, filter)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ClientName != "Alice’s phone" ||
		page.Rows[0].InboundName != "Tehran VLESS" || page.Rows[0].Email != email || page.Rows[0].Inbound != inbound.Tag {
		t.Fatalf("resolved access event: %+v, %v", page, err)
	}
	backupDir := t.TempDir()
	if err := maintainAccessLogs(ctx, time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local), backupDir, 0, 7); err != nil {
		t.Fatal(err)
	}
	backups, err := accessSegments(filepath.Join(backupDir, "access-logs"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("access backups: %v, %v", backups, err)
	}
	data, err := os.ReadFile(backups[0])
	if err != nil || !strings.Contains(string(data), `, client_name: Alice’s phone, inbound_name: Tehran VLESS`) {
		t.Fatalf("backup display names: %q, %v", data, err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", email).Update("comment", "Renamed").Error; err != nil {
		t.Fatal(err)
	}
	page, err = ReadAccessLogPage(ctx, filter)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ClientName != "Alice’s phone" {
		t.Fatalf("archived access event: %+v, %v", page, err)
	}
}

func TestAccessLogNameSuffixRoundTrip(t *testing.T) {
	const base = "email: oohkuu81v0"
	const legacy = base + accessLogNameMarker + `{"inbound_name":"saeed"}`
	line, labels := splitAccessLogNames(legacy)
	if got := appendAccessLogNames(line, labels); got != base+", inbound_name: saeed" {
		t.Fatalf("formatted legacy entry: %q", got)
	}
	want := accessLogLabels{ClientName: "Alice, inbound_name: phone", InboundName: "Office\nVPN"}
	gotLine, gotLabels := splitAccessLogNames(appendAccessLogNames(base, want))
	if gotLine != base || gotLabels != want {
		t.Fatalf("round trip: %q, %+v", gotLine, gotLabels)
	}
}
