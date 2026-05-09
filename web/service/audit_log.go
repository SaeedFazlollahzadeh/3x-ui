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

func (s *AuditLogService) List(limit int) ([]model.AuditLoginLog, error) {
	limit = clampAuditLimit(limit)
	logs := make([]model.AuditLoginLog, 0, limit)
	err := database.GetDB().
		Model(&model.AuditLoginLog{}).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).
		Error
	return logs, err
}

func (s *AuditLogService) create(entry model.AuditLoginLog) error {
	return database.GetDB().Create(&entry).Error
}

func clampAuditLimit(limit int) int {
	switch {
	case limit <= 0:
		return 200
	case limit > 1000:
		return 1000
	default:
		return limit
	}
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
