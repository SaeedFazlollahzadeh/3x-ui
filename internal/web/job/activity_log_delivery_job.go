package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/email"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tgbot"
)

func RunActivityLogBackup(ctx context.Context, bot *tgbot.Tgbot, settings service.SettingService, manual bool) (string, error) {
	now := time.Now()
	path, filename, err := service.BuildActivityLogDelivery(ctx, now, manual)
	if err != nil {
		return "", fmt.Errorf("create activity log backup: %w", err)
	}
	if !manual {
		if err := service.MaintainActivityLogs(ctx, now); err != nil {
			logger.Warning("prune activity logs:", err)
		}
	}
	if path == "" {
		if manual {
			return "", errors.New("set a positive backup window for access logs or subscription login logs and save the settings")
		}
		return "", nil
	}
	emailErr := sendActivityLogEmail(settings, path, filename)
	telegramErr := sendActivityLogTelegram(ctx, bot, settings, path, filename)
	return filename, errors.Join(emailErr, telegramErr)
}

func sendActivityLogEmail(settings service.SettingService, path, filename string) error {
	enabled, err := settings.GetActivityLogEmailBackup()
	if err != nil || !enabled {
		return err
	}
	smtpEnabled, err := settings.GetSmtpEnable()
	if err != nil {
		return err
	}
	if !smtpEnabled {
		return errors.New("activity backup email requires SMTP to be enabled")
	}
	if err := email.NewEmailService(settings).SendActivityLogBackup(path, filename); err != nil {
		return fmt.Errorf("send activity backup by email: %w", err)
	}
	return nil
}

func sendActivityLogTelegram(ctx context.Context, bot *tgbot.Tgbot, settings service.SettingService, path, filename string) error {
	enabled, err := settings.GetActivityLogTelegramBackup()
	if err != nil || !enabled {
		return err
	}
	if !bot.IsRunning() {
		return errors.New("activity backup Telegram bot is not running")
	}
	if err := bot.SendActivityLogBackup(ctx, path, filename); err != nil {
		return fmt.Errorf("send activity backup by Telegram: %w", err)
	}
	return nil
}
