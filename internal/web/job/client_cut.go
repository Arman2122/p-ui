package job

import (
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
)

// cutDisabledClients honours restartXrayOnClientDisable for clients a pass just switched off.
// A hot removal never closes a tunnel that is already up; replacing the process is the only lever.
func cutDisabledClients(disabled []string, settings *service.SettingService, xray *service.XrayService, inbounds *service.InboundService) {
	restartOnDisable, err := settings.GetRestartXrayOnClientDisable()
	if err != nil {
		logger.Warning("get RestartXrayOnClientDisable failed:", err)
		return
	}
	if restartOnDisable {
		if depletedStillConnected(disabled, inbounds.GetOnlineClients()) {
			if err := xray.RestartXray(true); err != nil {
				logger.Warning("reconcile xray after disabling clients failed:", err)
				xray.SetToNeedRestart()
			}
		} else {
			// Nobody switched off is connected, so no tunnel needs cutting now; the
			// flag lets the restart wait for the next reason.
			xray.SetToNeedRestart()
		}
	}
}
