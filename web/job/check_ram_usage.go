package job

import (
	"strconv"

	"github.com/mhsanaei/3x-ui/v3/web/service"

	"github.com/shirou/gopsutil/v4/mem"
)

// CheckRamJob monitors RAM usage and sends Telegram notifications when usage exceeds the configured threshold.
type CheckRamJob struct {
	tgbotService   service.Tgbot
	settingService service.SettingService
}

// NewCheckRamJob creates a new RAM monitoring job instance.
func NewCheckRamJob() *CheckRamJob {
	return new(CheckRamJob)
}

// Run checks current RAM usage and sends a Telegram alert if it exceeds the threshold.
func (j *CheckRamJob) Run() {
	threshold, err := j.settingService.GetTgRam()
	if err != nil || threshold <= 0 {
		return
	}

	vm, err := mem.VirtualMemory()
	if err == nil && vm.UsedPercent > float64(threshold) {
		msg := j.tgbotService.I18nBot("tgbot.messages.ramThreshold",
			"Percent=="+strconv.FormatFloat(vm.UsedPercent, 'f', 2, 64),
			"Threshold=="+strconv.Itoa(threshold))

		j.tgbotService.SendMsgToTgbotAdmins(msg)
	}
}
