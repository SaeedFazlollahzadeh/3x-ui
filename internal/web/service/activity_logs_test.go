package service

import (
	"context"
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
