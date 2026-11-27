package service

import (
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"gorm.io/gorm"
)

const dailyTrafficDays = 30

func addClientDailyTraffic(tx *gorm.DB, email string, at time.Time, up, down int64) error {
	if up <= 0 && down <= 0 {
		return nil
	}
	return tx.Exec(fmt.Sprintf(`
		INSERT INTO client_daily_traffics (client_id, day, up, down)
		SELECT id, ?, ?, ? FROM clients WHERE email = ?
		ON CONFLICT (client_id, day) DO UPDATE SET up = %s, down = %s`,
		database.ClampedAddExpr("client_daily_traffics.up"),
		database.ClampedAddExpr("client_daily_traffics.down")),
		at.UTC().Format(time.DateOnly), max(up, 0), max(down, 0), email, max(up, 0), max(down, 0)).Error
}

// ReadClientDailyTraffic returns the last 30 UTC days, including days with zero usage.
func ReadClientDailyTraffic(emails []string) ([]model.ClientDailyTraffic, error) {
	rows := make([]model.ClientDailyTraffic, 0)
	if len(emails) == 0 {
		return rows, nil
	}
	now := time.Now().UTC()
	start := now.AddDate(0, 0, 1-dailyTrafficDays)
	byDay := make(map[string]model.ClientDailyTraffic)
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var page []model.ClientDailyTraffic
		if err := database.GetDB().Model(&model.ClientDailyTraffic{}).
			Joins("JOIN clients ON clients.id = client_daily_traffics.client_id").
			Where("clients.email IN ? AND day >= ?", batch, start.Format(time.DateOnly)).Find(&page).Error; err != nil {
			return nil, err
		}
		for _, row := range page {
			sum := byDay[row.Day]
			sum.Up = clampedTrafficAdd(sum.Up, row.Up)
			sum.Down = clampedTrafficAdd(sum.Down, row.Down)
			byDay[row.Day] = sum
		}
	}
	for i := range dailyTrafficDays {
		day := start.AddDate(0, 0, i).Format(time.DateOnly)
		row := byDay[day]
		row.Day = day
		rows = append(rows, row)
	}
	return rows, nil
}
