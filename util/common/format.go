package common

import (
	"fmt"
)

// FormatTraffic formats traffic bytes into human-readable units. Traffic below
// 1 MB is intentionally shown in KB so small daily usage values still align
// with the panel's per-day usage views.
func FormatTraffic(trafficBytes int64) string {
	if trafficBytes <= 0 {
		return "0.00KB"
	}
	size := float64(trafficBytes)
	if size < 1024*1024 {
		return fmt.Sprintf("%.2fKB", size/1024)
	}
	if size < 1024*1024*1024 {
		return fmt.Sprintf("%.2fMB", size/(1024*1024))
	}
	if size < 1024*1024*1024*1024 {
		return fmt.Sprintf("%.2fGB", size/(1024*1024*1024))
	}
	if size < 1024*1024*1024*1024*1024 {
		return fmt.Sprintf("%.2fTB", size/(1024*1024*1024*1024))
	}
	return fmt.Sprintf("%.2fPB", size/(1024*1024*1024*1024*1024))
}
