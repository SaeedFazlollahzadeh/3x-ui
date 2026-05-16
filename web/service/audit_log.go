package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/database"
	"github.com/mhsanaei/3x-ui/v3/database/model"
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

type AuditLogFilter struct {
	EventType string
	IPAddress string
	Subject   string
	Dest      string
	Inbound   string
	Email     string
	UserAgent string
	From      string
	To        string
}

type AccessLogPageItem struct {
	Id                 int    `json:"id"`
	Inbound            string `json:"inbound"`
	Email              string `json:"email"`
	SourceAddress      string `json:"sourceAddress"`
	DestinationAddress string `json:"destinationAddress"`
	Date               int64  `json:"date"`
}

type AccessLogPage struct {
	Items    []AccessLogPageItem `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
}

// AuditLogService stores and retrieves panel audit login events.
type AuditLogService struct{}

func (s *AuditLogService) LogAdminLogin(username, ip, userAgent, requestPath string) error {
	return s.create(model.AuditLoginLog{
		EventType:   AuditEventAdminLogin,
		Username:    strings.TrimSpace(username),
		Subject:     strings.TrimSpace(username),
		Destination: "unknown",
		RequestPath: normalizeRequestPath(requestPath),
		IPAddress:   normalizeAuditValue(ip, 128),
		UserAgent:   normalizeAuditValue(userAgent, 2048),
		CreatedAt:   time.Now().UnixMilli(),
	})
}

func (s *AuditLogService) LogSubscriptionAccess(subject, subID string, clientEmails []string, ip, userAgent, requestPath, destination string) error {
	return s.create(model.AuditLoginLog{
		EventType:    AuditEventSubscriptionLogin,
		Subject:      normalizeAuditValue(subject, 255),
		SubID:        strings.TrimSpace(subID),
		ClientEmails: strings.Join(uniqueNonEmpty(clientEmails), ", "),
		Inbound:      normalizeAuditValue(extractInboundName(subject), 255),
		Destination:  normalizeAuditValue(destination, 1024),
		RequestPath:  normalizeRequestPath(requestPath),
		IPAddress:    normalizeAuditValue(ip, 128),
		UserAgent:    normalizeAuditValue(userAgent, 2048),
		CreatedAt:    time.Now().UnixMilli(),
	})
}

func (s *AuditLogService) ListPage(page, pageSize int, filter AuditLogFilter) (*AuditLogPage, error) {
	page, pageSize = clampAuditPage(page, pageSize)
	logs := make([]model.AuditLoginLog, 0, pageSize)

	query := database.GetDB().Model(&model.AuditLoginLog{})
	if filter.EventType != "" {
		query = query.Where("event_type = ?", filter.EventType)
	}
	if value := strings.TrimSpace(filter.IPAddress); value != "" {
		query = query.Where("ip_address LIKE ?", likePattern(value))
	}
	if value := strings.TrimSpace(filter.Subject); value != "" {
		query = query.Where("LOWER(subject) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.Dest); value != "" {
		query = query.Where("LOWER(destination) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.Inbound); value != "" {
		query = query.Where("LOWER(inbound) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.Email); value != "" {
		query = query.Where("LOWER(client_emails) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.UserAgent); value != "" {
		query = query.Where("LOWER(user_agent) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.From); value != "" {
		fromMillis, err := parseAuditTimeFilter(value)
		if err != nil {
			return nil, fmt.Errorf("invalid from date: %w", err)
		}
		query = query.Where("created_at >= ?", fromMillis)
	}
	if value := strings.TrimSpace(filter.To); value != "" {
		toMillis, err := parseAuditTimeFilter(value)
		if err != nil {
			return nil, fmt.Errorf("invalid to date: %w", err)
		}
		query = query.Where("created_at <= ?", toMillis)
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

func (s *AuditLogService) ListAccessPage(page, pageSize int, filter AuditLogFilter) (*AccessLogPage, error) {
	page, pageSize = clampAuditPage(page, pageSize)
	logs := make([]model.AuditLoginLog, 0, pageSize)

	query := database.GetDB().Model(&model.AuditLoginLog{}).
		Where("event_type = ?", AuditEventSubscriptionLogin).
		Where("user_agent = ?", "xray-access")

	if value := strings.TrimSpace(filter.IPAddress); value != "" {
		query = query.Where("ip_address LIKE ?", likePattern(value))
	}
	if value := strings.TrimSpace(filter.Inbound); value != "" {
		query = query.Where("LOWER(inbound) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.Email); value != "" {
		query = query.Where("LOWER(client_emails) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.Dest); value != "" {
		query = query.Where("LOWER(destination) LIKE ?", likePattern(strings.ToLower(value)))
	}
	if value := strings.TrimSpace(filter.From); value != "" {
		fromMillis, err := parseAuditTimeFilter(value)
		if err != nil {
			return nil, fmt.Errorf("invalid from date: %w", err)
		}
		query = query.Where("created_at >= ?", fromMillis)
	}
	if value := strings.TrimSpace(filter.To); value != "" {
		toMillis, err := parseAuditTimeFilter(value)
		if err != nil {
			return nil, fmt.Errorf("invalid to date: %w", err)
		}
		query = query.Where("created_at <= ?", toMillis)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	if err := query.Order("created_at DESC").Offset((page-1)*pageSize).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, err
	}

	items := make([]AccessLogPageItem, 0, len(logs))
	for _, entry := range logs {
		items = append(items, AccessLogPageItem{
			Id:                 entry.Id,
			Inbound:            emptyToUnknown(entry.Inbound),
			Email:              firstCSV(entry.ClientEmails),
			SourceAddress:      emptyToUnknown(entry.IPAddress),
			DestinationAddress: emptyToUnknown(entry.Destination),
			Date:               entry.CreatedAt,
		})
	}

	return &AccessLogPage{
		Items:    items,
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

func likePattern(value string) string {
	return "%" + value + "%"
}

func extractInboundName(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" || subject == "unknown" {
		return "unknown"
	}
	if idx := strings.LastIndex(subject, " - "); idx >= 0 {
		name := strings.TrimSpace(subject[:idx])
		if name != "" {
			return name
		}
	}
	return subject
}

func firstCSV(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if idx := strings.Index(value, ","); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return emptyToUnknown(value)
}

func emptyToUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func parseAuditTimeFilter(value string) (int64, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("unsupported time format: %s", value)
}
