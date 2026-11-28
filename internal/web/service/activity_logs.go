package service

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// ActivityLogFilter queries a page of events in reverse order.
type ActivityLogFilter struct {
	Client  string `form:"client" binding:"max=256"`
	Inbound string `form:"inbound" binding:"max=256"`
	Search  string `form:"search" binding:"max=256"`
	Status  string `form:"status" binding:"omitempty,oneof=accepted rejected"`
	From    int64  `form:"from" binding:"min=0"`
	To      int64  `form:"to" binding:"min=0"`
	Before  int64  `form:"before" binding:"min=0"`
	Limit   int    `form:"limit" binding:"min=0,max=100"`
}

func (filter ActivityLogFilter) pageSize() int {
	if filter.Limit < 1 || filter.Limit > 100 {
		return 50
	}
	return filter.Limit
}

type SubscriptionLogEntry struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	At        int64  `json:"at"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
}

type SubscriptionLogPage struct {
	Rows       []SubscriptionLogEntry `json:"rows"`
	NextBefore int64                  `json:"nextBefore"`
}

const subscriptionLogEmail = "COALESCE(NULLIF(client_subscription_fetches.email, ''), c.email, '')"

func ReadSubscriptionLogPage(ctx context.Context, filter ActivityLogFilter) (SubscriptionLogPage, error) {
	page := SubscriptionLogPage{Rows: []SubscriptionLogEntry{}}
	query := database.GetDB().WithContext(ctx).Model(&model.ClientSubscriptionFetch{}).
		Select("client_subscription_fetches.id, " + subscriptionLogEmail + " AS email, client_subscription_fetches.at, client_subscription_fetches.ip, client_subscription_fetches.user_agent").
		Joins("LEFT JOIN clients c ON c.id = client_subscription_fetches.client_id")
	contains := func(value string) string { return "%" + escapeLikeLiteral(strings.ToLower(value)) + "%" }
	if filter.Client != "" {
		query = query.Where("LOWER("+subscriptionLogEmail+") LIKE ? ESCAPE '\\'", contains(filter.Client))
	}
	if filter.Inbound != "" {
		value := contains(filter.Inbound)
		query = query.Where(`EXISTS (
			SELECT 1 FROM client_inbounds ci JOIN inbounds i ON i.id = ci.inbound_id
			WHERE ci.client_id = c.id AND (LOWER(i.tag) LIKE ? ESCAPE '\' OR LOWER(i.remark) LIKE ? ESCAPE '\')
		)`, value, value)
	}
	if filter.Search != "" {
		value := contains(filter.Search)
		query = query.Where("(LOWER(client_subscription_fetches.ip) LIKE ? ESCAPE '\\' OR LOWER(client_subscription_fetches.user_agent) LIKE ? ESCAPE '\\')", value, value)
	}
	if filter.From > 0 {
		query = query.Where("client_subscription_fetches.at >= ?", filter.From)
	}
	if filter.To > 0 {
		query = query.Where("client_subscription_fetches.at < ?", filter.To)
	}
	if filter.Before > 0 {
		query = query.Where("client_subscription_fetches.id < ?", filter.Before)
	}
	limit := filter.pageSize()
	if err := query.Order("client_subscription_fetches.id DESC").Limit(limit + 1).Scan(&page.Rows).Error; err != nil {
		return page, err
	}
	if len(page.Rows) > limit {
		page.Rows = page.Rows[:limit]
		page.NextBefore = page.Rows[limit-1].ID
	}
	return page, nil
}

type AccessLogEntry struct {
	Offset      int64     `json:"offset"`
	Status      string    `json:"status"`
	DateTime    time.Time `json:"DateTime"`
	Email       string    `json:"Email"`
	Inbound     string    `json:"Inbound"`
	ClientName  string    `json:"ClientName"`
	InboundName string    `json:"InboundName"`
	FromAddress string    `json:"FromAddress"`
	ToAddress   string    `json:"ToAddress"`
}

type AccessLogPage struct {
	Rows       []AccessLogEntry `json:"rows"`
	NextBefore int64            `json:"nextBefore"`
	Enabled    bool             `json:"enabled"`
}

func accessLogDestination(destination string) string {
	// HTTP proxy logs can contain credentials, paths and query parameters.
	if target, err := url.Parse(destination); err == nil && target.Host != "" {
		return target.Host
	}
	return destination
}

func ReadAccessLogPage(ctx context.Context, filter ActivityLogFilter) (AccessLogPage, error) {
	page := AccessLogPage{Rows: []AccessLogEntry{}}
	path, err := xray.GetAccessLogPath()
	if errors.Is(err, os.ErrNotExist) || (err == nil && (path == "" || path == "none")) {
		return page, nil
	}
	if err != nil {
		return page, err
	}
	page.Enabled = true
	// Keep only one page plus a lookahead. The cursor is the byte position of
	// the oldest returned line, so new appends can't shift the next page.
	limit := filter.pageSize()
	window := make([]AccessLogEntry, limit+1)
	matches := 0
	names := readAccessLogNames(ctx)
	client, inbound, search := strings.ToLower(filter.Client), strings.ToLower(filter.Inbound), strings.ToLower(filter.Search)
	err = scanAccessLogs(ctx, path, filter.Before, func(line string, offset int64) {
		line, recorded := splitAccessLogNames(line)
		if isInternalAccessLogLine([]byte(line)) {
			return
		}
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[2] != "from" {
			return
		}
		status := fields[4]
		if (status != "accepted" && status != "rejected") || (filter.Status != "" && status != filter.Status) {
			return
		}
		entry := parseAccessLogTokens(fields)
		labels := names.labels(entry, recorded)
		entry.FromAddress = strings.TrimLeft(fields[3], "/")
		entry.ToAddress = ""
		if len(fields) > 5 && fields[5] != "<nil>" &&
			!(status == "rejected" && strings.Contains(line, " rejected  ")) {
			entry.ToAddress = strings.TrimLeft(fields[5], "/")
		}
		at := entry.DateTime.UnixMilli()
		if entry.DateTime.IsZero() ||
			(filter.From > 0 && at < filter.From) || (filter.To > 0 && at >= filter.To) ||
			(!strings.Contains(strings.ToLower(entry.Email), client) && !strings.Contains(strings.ToLower(labels.ClientName), client)) ||
			(!strings.Contains(strings.ToLower(entry.Inbound), inbound) && !strings.Contains(strings.ToLower(labels.InboundName), inbound)) {
			return
		}
		entry.ToAddress = accessLogDestination(entry.ToAddress)
		if search != "" &&
			!strings.Contains(strings.ToLower(entry.ToAddress), search) &&
			!strings.Contains(strings.ToLower(entry.FromAddress), search) {
			return
		}
		window[matches%len(window)] = AccessLogEntry{
			Offset: offset, Status: status, DateTime: entry.DateTime, Email: entry.Email,
			Inbound: entry.Inbound, FromAddress: entry.FromAddress, ToAddress: entry.ToAddress,
			ClientName: labels.ClientName, InboundName: labels.InboundName,
		}
		matches++
	})
	if err != nil {
		return page, err
	}
	for i := range min(matches, limit) {
		page.Rows = append(page.Rows, window[(matches-1-i)%len(window)])
	}
	if matches > limit {
		page.NextBefore = page.Rows[limit-1].Offset
	}
	return page, nil
}
