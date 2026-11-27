package model

// ClientDailyTraffic stores counted bytes independently of quota resets.
type ClientDailyTraffic struct {
	ClientID int    `gorm:"primaryKey;autoIncrement:false" json:"-"`
	Day      string `gorm:"primaryKey;size:10" json:"day"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
}

// ClientSubscriptionFetch records a successful retrieval of a subscription body.
type ClientSubscriptionFetch struct {
	ID        int64  `gorm:"primaryKey;index:idx_fetch_client_id,priority:2" json:"id"`
	ClientID  int    `gorm:"index:idx_fetch_client_id,priority:1" json:"-"`
	Email     string `gorm:"not null;default:''" json:"-"`
	At        int64  `gorm:"index" json:"at"`
	UserAgent string `json:"userAgent"`
	IP        string `json:"ip"`
}
