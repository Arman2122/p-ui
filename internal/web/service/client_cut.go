package service

import "github.com/Arman2122/p-ui/internal/logger"

// CutDisabledClients honours restartXrayOnClientDisable for clients a pass just switched off.
// A hot removal never closes a tunnel that is already up; replacing the process is the only lever.
func CutDisabledClients(disabled []string, settings *SettingService, xray *XrayService, inbounds *InboundService) {
	restartOnDisable, err := settings.GetRestartXrayOnClientDisable()
	if err != nil {
		logger.Warning("get RestartXrayOnClientDisable failed:", err)
		return
	}
	if restartOnDisable {
		if ClientsStillConnected(disabled, inbounds.GetOnlineClients()) {
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

// ClientsStillConnected reports whether any client just switched off is in the
// online set, the only case a forced restart buys anything.
func ClientsStillConnected(disabled, online []string) bool {
	if len(disabled) == 0 || len(online) == 0 {
		return false
	}
	on := make(map[string]struct{}, len(online))
	for _, email := range online {
		on[email] = struct{}{}
	}
	for _, email := range disabled {
		if _, ok := on[email]; ok {
			return true
		}
	}
	return false
}
