package service

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InboundSpeedLimitEntry struct {
	ClientID      int    `json:"clientId"`
	Email         string `json:"email"`
	Bytes         int    `json:"bytesPerSecond"`
	UploadBytes   int    `json:"uploadBytesPerSecond"`
	DownloadBytes int    `json:"downloadBytesPerSecond"`
}

func SupportsInboundSpeedLimits(inbound *model.Inbound) bool {
	if inbound.NodeID != nil {
		return false
	}
	switch inbound.Protocol {
	case model.VLESS, model.VMESS, model.Trojan, model.Shadowsocks, model.Hysteria, model.MASQUE:
		return true
	default:
		return false
	}
}

// Earlier rows have NULL directional columns and use the shared bytes value.
const speedLimitColumns = `COALESCE(l.bytes, 0) AS bytes,
	COALESCE(l.upload_bytes, l.bytes, 0) AS upload_bytes,
	COALESCE(l.download_bytes, l.bytes, 0) AS download_bytes`

func (s *InboundService) GetInboundSpeedLimits(id int) ([]InboundSpeedLimitEntry, error) {
	rows := []InboundSpeedLimitEntry{}
	err := database.GetDB().Table("client_inbounds ci").
		Select("c.id AS client_id, c.email, "+speedLimitColumns).
		Joins("JOIN clients c ON c.id = ci.client_id").
		Joins("LEFT JOIN inbound_speed_limits l ON l.inbound_id = ci.inbound_id AND l.client_id = ci.client_id").
		Where("ci.inbound_id = ?", id).Order("c.email").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	var inboundLimit InboundSpeedLimitEntry
	if err := database.GetDB().Table("inbound_speed_limits l").Select(speedLimitColumns).
		Where("l.inbound_id = ? AND l.client_id = 0", id).Limit(1).Scan(&inboundLimit).Error; err != nil {
		return nil, err
	}
	return append([]InboundSpeedLimitEntry{inboundLimit}, rows...), nil
}

func (s *InboundService) SetInboundSpeedLimit(id, clientID, upload, download int) error {
	validRate := func(bytes int) bool { return bytes == 0 || (bytes >= 10000 && bytes <= 1000000000) }
	if clientID < 0 || !validRate(upload) || !validRate(download) {
		return errors.New("speed limit must be zero or between 0.01 and 1000 MB/s")
	}
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if clientID > 0 {
			var membership model.ClientInbound
			if err := tx.Where("inbound_id = ? AND client_id = ?", id, clientID).First(&membership).Error; err != nil {
				return err
			}
		}
		row := model.InboundSpeedLimit{InboundID: id, ClientID: clientID, UploadBytes: &upload, DownloadBytes: &download}
		if upload == download {
			row.Bytes = upload
		}
		if upload == 0 && download == 0 {
			return tx.Where("inbound_id = ? AND client_id = ?", id, clientID).Delete(&row).Error
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "inbound_id"}, {Name: "client_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"bytes", "upload_bytes", "download_bytes"}),
		}).Create(&row).Error
	})
}

func buildInboundSpeedLimits() (json.RawMessage, error) {
	var rows []struct {
		InboundSpeedLimitEntry
		NodeID   *int
		Protocol model.Protocol
		Tag      string
	}
	err := database.GetDB().Table("inbound_speed_limits l").
		Select("i.protocol, i.tag, i.node_id, COALESCE(c.email, '') AS email, "+speedLimitColumns).
		Joins("JOIN inbounds i ON i.id = l.inbound_id").
		Joins("LEFT JOIN client_inbounds ci ON ci.inbound_id = l.inbound_id AND ci.client_id = l.client_id").
		Joins("LEFT JOIN clients c ON c.id = ci.client_id").
		Where("i.enable = ? AND (l.client_id = 0 OR c.id IS NOT NULL)", true).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	type directionalLimit struct {
		Upload   int `json:"upload"`
		Download int `json:"download"`
	}
	result := make(map[string]map[string]directionalLimit)
	for _, row := range rows {
		if !SupportsInboundSpeedLimits(&model.Inbound{NodeID: row.NodeID, Protocol: row.Protocol}) {
			continue
		}
		if result[row.Tag] == nil {
			result[row.Tag] = make(map[string]directionalLimit)
		}
		result[row.Tag][strings.ToLower(row.Email)] = directionalLimit{Upload: row.UploadBytes, Download: row.DownloadBytes}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return json.Marshal(result)
}
