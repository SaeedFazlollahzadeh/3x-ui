package job

import (
	"context"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func RunActivityLogMaintenance(ctx context.Context) {
	if err := service.MaintainActivityLogs(ctx, time.Now()); err != nil {
		logger.Warning("maintain activity logs:", err)
	}
}
