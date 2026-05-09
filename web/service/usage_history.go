package service

import (
	"sort"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

type ClientUsageHistoryPoint struct {
	Timestamp int64 `json:"timestamp"`
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	Total     int64 `json:"total"`
}

type ClientUsageHistoryResponse struct {
	InboundId     int                      `json:"inboundId"`
	InboundRemark string                   `json:"inboundRemark"`
	ClientEmail   string                   `json:"clientEmail"`
	StartedAt     int64                    `json:"startedAt"`
	UpdatedAt     int64                    `json:"updatedAt"`
	CurrentUp     int64                    `json:"currentUp"`
	CurrentDown   int64                    `json:"currentDown"`
	CurrentTotal  int64                    `json:"currentTotal"`
	BucketSeconds int64                    `json:"bucketSeconds"`
	Points        []ClientUsageHistoryPoint `json:"points"`
}

// RecordClientTrafficSnapshots persists absolute counters for clients that had
// traffic activity in the current polling cycle. Values are stored in bytes.
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

	snapshots := make([]model.ClientTrafficSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshots = append(snapshots, model.ClientTrafficSnapshot{
			InboundId:   row.InboundId,
			ClientEmail: row.Email,
			Up:          row.Up,
			Down:        row.Down,
			Total:       row.Up + row.Down,
		})
	}
	return db.Create(&snapshots).Error
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

	var snapshots []model.ClientTrafficSnapshot
	if err := db.Model(&model.ClientTrafficSnapshot{}).
		Where("inbound_id = ? AND client_email = ?", inboundId, email).
		Order("created_at asc").
		Find(&snapshots).Error; err != nil {
		return nil, err
	}

	resp := &ClientUsageHistoryResponse{
		InboundId:     inboundId,
		InboundRemark: inbound.Remark,
		ClientEmail:   email,
		CurrentUp:     current.Up,
		CurrentDown:   current.Down,
		CurrentTotal:  current.Up + current.Down,
		Points:        []ClientUsageHistoryPoint{},
	}

	if len(snapshots) == 0 {
		return resp, nil
	}

	resp.StartedAt = snapshots[0].CreatedAt
	resp.UpdatedAt = snapshots[len(snapshots)-1].CreatedAt
	resp.Points, resp.BucketSeconds = aggregateClientUsageHistory(snapshots, 240)
	return resp, nil
}

func aggregateClientUsageHistory(snapshots []model.ClientTrafficSnapshot, maxPoints int) ([]ClientUsageHistoryPoint, int64) {
	if len(snapshots) == 0 {
		return []ClientUsageHistoryPoint{}, 0
	}
	if maxPoints <= 0 || len(snapshots) <= maxPoints {
		points := make([]ClientUsageHistoryPoint, 0, len(snapshots))
		for _, snapshot := range snapshots {
			points = append(points, ClientUsageHistoryPoint{
				Timestamp: snapshot.CreatedAt,
				Up:        snapshot.Up,
				Down:      snapshot.Down,
				Total:     snapshot.Total,
			})
		}
		return points, 0
	}

	spanMs := snapshots[len(snapshots)-1].CreatedAt - snapshots[0].CreatedAt
	if spanMs <= 0 {
		last := snapshots[len(snapshots)-1]
		return []ClientUsageHistoryPoint{{
			Timestamp: last.CreatedAt,
			Up:        last.Up,
			Down:      last.Down,
			Total:     last.Total,
		}}, 0
	}

	bucketMs := spanMs / int64(maxPoints-1)
	if bucketMs < 10000 {
		bucketMs = 10000
	}

	grouped := make(map[int64]model.ClientTrafficSnapshot, maxPoints)
	order := make([]int64, 0, maxPoints)
	for _, snapshot := range snapshots {
		bucket := ((snapshot.CreatedAt - snapshots[0].CreatedAt) / bucketMs) * bucketMs
		bucketStart := snapshots[0].CreatedAt + bucket
		if _, exists := grouped[bucketStart]; !exists {
			order = append(order, bucketStart)
		}
		grouped[bucketStart] = snapshot
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	points := make([]ClientUsageHistoryPoint, 0, len(order))
	for _, bucketStart := range order {
		snapshot := grouped[bucketStart]
		points = append(points, ClientUsageHistoryPoint{
			Timestamp: snapshot.CreatedAt,
			Up:        snapshot.Up,
			Down:      snapshot.Down,
			Total:     snapshot.Total,
		})
	}
	return points, bucketMs / 1000
}
