package model

// InboundSpeedLimit applies to a whole inbound when ClientID is zero.
type InboundSpeedLimit struct {
	InboundID int `gorm:"primaryKey;autoIncrement:false"`
	ClientID  int `gorm:"primaryKey;autoIncrement:false"`
	Bytes     int `gorm:"not null"`
	// NULL preserves the shared cap stored by earlier panel versions.
	UploadBytes   *int
	DownloadBytes *int
}
