package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func activityLogFilter(c *gin.Context) (service.ActivityLogFilter, bool) {
	c.Header("Cache-Control", "no-store")
	var filter service.ActivityLogFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		jsonObj(c, nil, err)
		return filter, false
	}
	if filter.To > 0 && filter.From >= filter.To {
		jsonObj(c, nil, errors.New("start date must be before end date"))
		return filter, false
	}
	return filter, true
}

func panelAccessLogs(c *gin.Context) {
	filter, ok := activityLogFilter(c)
	if !ok {
		return
	}
	page, err := service.ReadAccessLogPage(c.Request.Context(), filter)
	jsonObj(c, page, err)
}

func panelSubscriptionLogs(c *gin.Context) {
	filter, ok := activityLogFilter(c)
	if !ok {
		return
	}
	page, err := service.ReadSubscriptionLogPage(c.Request.Context(), filter)
	jsonObj(c, page, err)
}
