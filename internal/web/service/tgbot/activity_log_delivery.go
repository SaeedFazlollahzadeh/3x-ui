package tgbot

import (
	"context"
	"errors"
	"fmt"
	"os"

	tu "github.com/mymmrac/telego/telegoutil"
)

type namedBackupFile struct {
	*os.File
	name string
}

func (file namedBackupFile) Name() string {
	return file.name
}

// SendActivityLogBackup uses the same bot, admin chat IDs, and topics as the
// existing database backup. Each upload gets its own reader.
func (t *Tgbot) SendActivityLogBackup(ctx context.Context, path, filename string) error {
	if !t.IsRunning() {
		return errors.New("Telegram backup bot is not running")
	}
	admins := adminSnapshot()
	if len(admins) == 0 {
		return errors.New("no Telegram admin chat IDs configured")
	}
	var failures []error
	for _, admin := range admins {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		file, err := os.Open(path)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		ok := t.sendBackupDocument(ctx, admin, tu.File(namedBackupFile{File: file, name: filename}), "")
		_ = file.Close()
		if !ok {
			failures = append(failures, fmt.Errorf("could not send activity logs to Telegram chat %d", admin))
		}
	}
	return errors.Join(failures...)
}
