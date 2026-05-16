package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/database"
	"github.com/mhsanaei/3x-ui/v3/database/model"
	"github.com/mhsanaei/3x-ui/v3/xray"
	"gorm.io/gorm/clause"
)

const usageDateLayout = "2006-01-02"

type ClientUsageHistoryPoint struct {
	Date      string `json:"date"`
	Timestamp int64  `json:"timestamp"`
	Up        int64  `json:"up"`
	Down      int64  `json:"down"`
	Total     int64  `json:"total"`
}

type ClientUsageHistoryResponse struct {
	InboundId     int                       `json:"inboundId"`
	InboundRemark string                    `json:"inboundRemark"`
	ClientEmail   string                    `json:"clientEmail"`
	StartedAt     int64                     `json:"startedAt"`
	UpdatedAt     int64                     `json:"updatedAt"`
	CurrentUp     int64                     `json:"currentUp"`
	CurrentDown   int64                     `json:"currentDown"`
	CurrentTotal  int64                     `json:"currentTotal"`
	Points        []ClientUsageHistoryPoint `json:"points"`
}

type DailyUsageListItem struct {
	UsageDate     string `json:"usageDate"`
	Timestamp     int64  `json:"timestamp"`
	InboundId     int    `json:"inboundId"`
	InboundRemark string `json:"inboundRemark"`
	ClientEmail   string `json:"clientEmail"`
	Up            int64  `json:"up"`
	Down          int64  `json:"down"`
	Total         int64  `json:"total"`
}

type DailyUsageListResponse struct {
	From   string                   `json:"from"`
	To     string                   `json:"to"`
	Points []ClientUsageHistoryPoint `json:"points"`
	Items  []DailyUsageListItem     `json:"items"`
}

type SubUsageResponse struct {
	SubID      string                   `json:"subId"`
	From       string                   `json:"from"`
	To         string                   `json:"to"`
	ClientRows []DailyUsageListItem     `json:"clientRows"`
	Points     []ClientUsageHistoryPoint `json:"points"`
	Up         int64                    `json:"up"`
	Down       int64                    `json:"down"`
	Total      int64                    `json:"total"`
}

func usageDayBounds(date string) (string, int64, int64, error) {
	if strings.TrimSpace(date) == "" {
		date = time.Now().Format(usageDateLayout)
	}
	t, err := time.ParseInLocation(usageDateLayout, date, time.Local)
	if err != nil {
		return "", 0, 0, err
	}
	start := t.UnixMilli()
	end := t.Add(24*time.Hour - time.Millisecond).UnixMilli()
	return t.Format(usageDateLayout), start, end, nil
}

func usageDateRange(fromDate, toDate, fallbackDate string) (string, string, error) {
	if strings.TrimSpace(fromDate) == "" && strings.TrimSpace(toDate) == "" {
		if strings.TrimSpace(fallbackDate) == "" {
			fallbackDate = time.Now().Format(usageDateLayout)
		}
		fromDate = fallbackDate
		toDate = fallbackDate
	} else {
		if strings.TrimSpace(fromDate) == "" {
			fromDate = toDate
		}
		if strings.TrimSpace(toDate) == "" {
			toDate = fromDate
		}
	}
	from, err := time.ParseInLocation(usageDateLayout, fromDate, time.Local)
	if err != nil {
		return "", "", err
	}
	to, err := time.ParseInLocation(usageDateLayout, toDate, time.Local)
	if err != nil {
		return "", "", err
	}
	if from.After(to) {
		from, to = to, from
	}
	return from.Format(usageDateLayout), to.Format(usageDateLayout), nil
}

func usageDateTimestamp(date string) int64 {
	t, err := time.ParseInLocation(usageDateLayout, date, time.Local)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// RecordClientTrafficSnapshots persists absolute counters for active clients
// and folds their traffic deltas into the current day bucket.
func (s *InboundService) RecordClientTrafficSnapshots(activeEmails []string) error {
	uniq := uniqueNonEmptyStrings(activeEmails)
	if len(uniq) == 0 {
		return nil
	}

	db := database.GetDB()
	var rows []xray.ClientTraffic
	for _, batch := range chunkStrings(uniq, sqliteMaxVars) {
		var page []xray.ClientTraffic
		if err := db.Model(&xray.ClientTraffic{}).
			Select("inbound_id", "email", "up", "down").
			Where("email IN ?", batch).
			Find(&page).Error; err != nil {
			return err
		}
		rows = append(rows, page...)
	}
	if len(rows) == 0 {
		return nil
	}

	inboundIDs := make([]int, 0, len(rows))
	emails := make([]string, 0, len(rows))
	for _, row := range rows {
		inboundIDs = append(inboundIDs, row.InboundId)
		emails = append(emails, row.Email)
	}

	var previous []model.ClientTrafficSnapshot
	if err := db.Model(&model.ClientTrafficSnapshot{}).
		Where("inbound_id IN ? AND client_email IN ?", uniqueInts(inboundIDs), uniqueNonEmptyStrings(emails)).
		Order("created_at desc").
		Find(&previous).Error; err != nil {
		return err
	}
	prevByKey := make(map[string]model.ClientTrafficSnapshot, len(rows))
	for _, snapshot := range previous {
		key := fmt.Sprintf("%d|%s", snapshot.InboundId, snapshot.ClientEmail)
		if _, exists := prevByKey[key]; exists {
			continue
		}
		prevByKey[key] = snapshot
	}

	usageDate := time.Now().Format(usageDateLayout)
	snapshots := make([]model.ClientTrafficSnapshot, 0, len(rows))
	dailyRows := make([]model.DailyClientUsage, 0, len(rows))
	for _, row := range rows {
		snapshots = append(snapshots, model.ClientTrafficSnapshot{
			InboundId:   row.InboundId,
			ClientEmail: row.Email,
			Up:          row.Up,
			Down:        row.Down,
			Total:       row.Up + row.Down,
		})

		prev := prevByKey[fmt.Sprintf("%d|%s", row.InboundId, row.Email)]
		upDelta := row.Up - prev.Up
		downDelta := row.Down - prev.Down
		if upDelta < 0 {
			upDelta = row.Up
		}
		if downDelta < 0 {
			downDelta = row.Down
		}
		if upDelta == 0 && downDelta == 0 {
			continue
		}
		dailyRows = append(dailyRows, model.DailyClientUsage{
			UsageDate:   usageDate,
			InboundId:   row.InboundId,
			ClientEmail: row.Email,
			Up:          upDelta,
			Down:        downDelta,
			Total:       upDelta + downDelta,
		})
	}

	tx := db.Begin()
	if err := tx.Create(&snapshots).Error; err != nil {
		tx.Rollback()
		return err
	}
	if len(dailyRows) > 0 {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "usage_date"},
				{Name: "inbound_id"},
				{Name: "client_email"},
			},
			DoUpdates: clause.Assignments(map[string]any{
				"up":         gormExpr("daily_client_usages.up + excluded.up"),
				"down":       gormExpr("daily_client_usages.down + excluded.down"),
				"total":      gormExpr("daily_client_usages.total + excluded.total"),
				"updated_at": gormExpr("excluded.updated_at"),
			}),
		}).Create(&dailyRows).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

func gormExpr(sql string) clause.Expr {
	return clause.Expr{SQL: sql}
}

func (s *InboundService) GetClientUsageHistory(inboundId int, email string) (*ClientUsageHistoryResponse, error) {
	db := database.GetDB()

	var inbound model.Inbound
	if err := db.Model(&model.Inbound{}).
		Select("id", "remark").
		Where("id = ?", inboundId).
		First(&inbound).Error; err != nil {
		return nil, err
	}

	var current xray.ClientTraffic
	if err := db.Model(&xray.ClientTraffic{}).
		Select("inbound_id", "email", "up", "down").
		Where("inbound_id = ? AND email = ?", inboundId, email).
		First(&current).Error; err != nil {
		return nil, err
	}

	var rows []model.DailyClientUsage
	if err := db.Model(&model.DailyClientUsage{}).
		Where("inbound_id = ? AND client_email = ?", inboundId, email).
		Order("usage_date asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	resp := &ClientUsageHistoryResponse{
		InboundId:     inboundId,
		InboundRemark: inbound.Remark,
		ClientEmail:   email,
		CurrentUp:     current.Up,
		CurrentDown:   current.Down,
		CurrentTotal:  current.Up + current.Down,
		Points:        make([]ClientUsageHistoryPoint, 0, len(rows)),
	}
	for _, row := range rows {
		ts := usageDateTimestamp(row.UsageDate)
		if resp.StartedAt == 0 {
			resp.StartedAt = ts
		}
		resp.UpdatedAt = ts
		resp.Points = append(resp.Points, ClientUsageHistoryPoint{
			Date:      row.UsageDate,
			Timestamp: ts,
			Up:        row.Up,
			Down:      row.Down,
			Total:     row.Total,
		})
	}
	return resp, nil
}

func (s *InboundService) GetDailyUsage(date, fromDate, toDate, inboundQuery, emailQuery string) (*DailyUsageListResponse, error) {
	from, to, err := usageDateRange(fromDate, toDate, date)
	if err != nil {
		return nil, err
	}

	db := database.GetDB()
	query := db.Table("daily_client_usages AS d").
		Select("d.usage_date, d.inbound_id, i.remark AS inbound_remark, d.client_email, d.up, d.down, d.total").
		Joins("JOIN inbounds AS i ON i.id = d.inbound_id").
		Where("d.usage_date >= ? AND d.usage_date <= ?", from, to)

	if q := strings.TrimSpace(inboundQuery); q != "" {
		query = query.Where("i.remark LIKE ?", "%"+q+"%")
	}
	if q := strings.TrimSpace(emailQuery); q != "" {
		query = query.Where("d.client_email LIKE ?", "%"+q+"%")
	}

	type row struct {
		UsageDate     string
		InboundId     int
		InboundRemark string
		ClientEmail   string
		Up            int64
		Down          int64
		Total         int64
	}
	var rows []row
	if err := query.Order("i.remark asc, d.client_email asc").Scan(&rows).Error; err != nil {
		return nil, err
	}

	items := make([]DailyUsageListItem, 0, len(rows))
	pointMap := make(map[string]*ClientUsageHistoryPoint)
	for _, row := range rows {
		ts := usageDateTimestamp(row.UsageDate)
		items = append(items, DailyUsageListItem{
			UsageDate:     row.UsageDate,
			Timestamp:     ts,
			InboundId:     row.InboundId,
			InboundRemark: row.InboundRemark,
			ClientEmail:   row.ClientEmail,
			Up:            row.Up,
			Down:          row.Down,
			Total:         row.Total,
		})
		point, ok := pointMap[row.UsageDate]
		if !ok {
			point = &ClientUsageHistoryPoint{
				Date:      row.UsageDate,
				Timestamp: ts,
			}
			pointMap[row.UsageDate] = point
		}
		point.Up += row.Up
		point.Down += row.Down
		point.Total += row.Total
	}
	points := make([]ClientUsageHistoryPoint, 0, len(pointMap))
	for day := from; ; {
		ts := usageDateTimestamp(day)
		if point, ok := pointMap[day]; ok {
			points = append(points, *point)
		} else {
			points = append(points, ClientUsageHistoryPoint{
				Date:      day,
				Timestamp: ts,
				Up:        0,
				Down:      0,
				Total:     0,
			})
		}
		if day == to {
			break
		}
		next, _ := time.ParseInLocation(usageDateLayout, day, time.Local)
		day = next.Add(24 * time.Hour).Format(usageDateLayout)
	}
	return &DailyUsageListResponse{From: from, To: to, Points: points, Items: items}, nil
}

func (s *InboundService) GetSubDailyUsage(subID, date, fromDate, toDate string) (*SubUsageResponse, error) {
	from, to, err := usageDateRange(fromDate, toDate, date)
	if err != nil {
		return nil, err
	}

	db := database.GetDB()
	type row struct {
		UsageDate     string
		InboundId     int
		InboundRemark string
		ClientEmail   string
		Up            int64
		Down          int64
		Total         int64
	}
	var rows []row
	if err := db.Table("daily_client_usages AS d").
		Select("d.usage_date, d.inbound_id, i.remark AS inbound_remark, d.client_email, d.up, d.down, d.total").
		Joins("JOIN inbounds AS i ON i.id = d.inbound_id").
		Joins("JOIN JSON_EACH(JSON_EXTRACT(i.settings, '$.clients')) AS client").
		Where("d.usage_date >= ? AND d.usage_date <= ? AND i.enable = ?", from, to, true).
		Where("JSON_EXTRACT(client.value, '$.subId') = ?", subID).
		Where("JSON_EXTRACT(client.value, '$.email') = d.client_email").
		Order("d.usage_date asc, d.inbound_id asc, d.client_email asc").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	clientRows := make([]DailyUsageListItem, 0, len(rows))
	pointMap := make(map[string]*ClientUsageHistoryPoint)
	var up, down int64
	for _, row := range rows {
		ts := usageDateTimestamp(row.UsageDate)
		clientRows = append(clientRows, DailyUsageListItem{
			UsageDate:     row.UsageDate,
			Timestamp:     ts,
			InboundId:     row.InboundId,
			InboundRemark: row.InboundRemark,
			ClientEmail:   row.ClientEmail,
			Up:            row.Up,
			Down:          row.Down,
			Total:         row.Total,
		})
		up += row.Up
		down += row.Down
		point, ok := pointMap[row.UsageDate]
		if !ok {
			point = &ClientUsageHistoryPoint{
				Date:      row.UsageDate,
				Timestamp: ts,
			}
			pointMap[row.UsageDate] = point
		}
		point.Up += row.Up
		point.Down += row.Down
		point.Total += row.Total
	}
	points := make([]ClientUsageHistoryPoint, 0)
	for day := from; ; {
		ts := usageDateTimestamp(day)
		if point, ok := pointMap[day]; ok {
			points = append(points, *point)
		} else {
			points = append(points, ClientUsageHistoryPoint{
				Date:      day,
				Timestamp: ts,
				Up:        0,
				Down:      0,
				Total:     0,
			})
		}
		if day == to {
			break
		}
		next, _ := time.ParseInLocation(usageDateLayout, day, time.Local)
		day = next.Add(24 * time.Hour).Format(usageDateLayout)
	}
	return &SubUsageResponse{
		SubID:      subID,
		From:       from,
		To:         to,
		ClientRows: clientRows,
		Points:     points,
		Up:         up,
		Down:       down,
		Total:      up + down,
	}, nil
}
