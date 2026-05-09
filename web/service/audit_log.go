package service

import (
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
)

const (
	AuditEventAdminLogin        = "admin_login"
	AuditEventSubscriptionLogin = "subscription_access"
)

type AuditLogPage struct {
	Items    []model.AuditLoginLog `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"pageSize"`
}

// AuditLogService stores and retrieves panel audit login events.
type AuditLogService struct{}

func (s *AuditLogService) LogAdminLogin(username, ip, userAgent, requestPath string) error {
	return s.create(model.AuditLoginLog{
		EventType:   AuditEventAdminLogin,
		Username:    strings.TrimSpace(username),
		Subject:     strings.TrimSpace(username),
		RequestPath: normalizeRequestPath(requestPath),
		IPAddress:   normalizeAuditValue(ip, 128),
		UserAgent:   normalizeAuditValue(userAgent, 2048),
		CreatedAt:   time.Now().UnixMilli(),
	})
}

func (s *AuditLogService) LogSubscriptionAccess(subject, subID string, clientEmails []string, ip, userAgent, requestPath string) error {
	return s.create(model.AuditLoginLog{
		EventType:    AuditEventSubscriptionLogin,
		Subject:      normalizeAuditValue(subject, 255),
		SubID:        strings.TrimSpace(subID),
		ClientEmails: strings.Join(uniqueNonEmpty(clientEmails), ", "),
		RequestPath:  normalizeRequestPath(requestPath),
		IPAddress:    normalizeAuditValue(ip, 128),
		UserAgent:    normalizeAuditValue(userAgent, 2048),
		CreatedAt:    time.Now().UnixMilli(),
	})
}

func (s *AuditLogService) ListPage(page, pageSize int, eventType string) (*AuditLogPage, error) {
	page, pageSize = clampAuditPage(page, pageSize)
	logs := make([]model.AuditLoginLog, 0, pageSize)

	query := database.GetDB().Model(&model.AuditLoginLog{})
	if eventType != "" {
		query = query.Where("event_type = ?", eventType)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	if err := query.
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&logs).
		Error; err != nil {
		return nil, err
	}

	return &AuditLogPage{
		Items:    logs,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *AuditLogService) create(entry model.AuditLoginLog) error {
	return database.GetDB().Create(&entry).Error
}

func clampAuditPage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	switch {
	case pageSize <= 0:
		pageSize = 20
	case pageSize > 200:
		pageSize = 200
	}
	return page, pageSize
}

func normalizeRequestPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	return normalizeAuditValue(path, 512)
}

func normalizeAuditValue(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > maxLen {
		return value[:maxLen]
	}
	return value
}

func uniqueNonEmpty(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
