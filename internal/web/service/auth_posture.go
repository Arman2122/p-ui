package service

import (
	"strings"

	"github.com/Arman2122/p-ui/internal/logger"
)

// authPostureLogPrefix is what an operator greps their journal for. Changing it
// breaks a search the changelog told people to run.
const authPostureLogPrefix = "[AUTH_POSTURE]"

/*
LogAuthPosture names an authentication setting that is on but cannot work, at
Error, on every boot.

Both states below are refused at the door now, so neither is exploitable — but
refusing is invisible. An operator whose 2FA has silently stopped accepting any
code, or whose LDAP sign-in has silently stopped working, needs to be told which
setting to look at rather than left debugging a login screen.

Reported, never repaired. Turning a security setting off on the operator's behalf
is the one thing worse than leaving it misconfigured.
*/
func (s *SettingService) LogAuthPosture() {
	if enabled, err := s.GetTwoFactorEnable(); err == nil && enabled {
		if token, err := s.GetTwoFactorToken(); err == nil && strings.TrimSpace(token) == "" {
			logger.Errorf("%s two-factor authentication is enabled with no secret, so no code can be accepted and every login is refused. "+
				"Re-enrol it from Settings, or clear it from a root shell with: p-ui setting -resetTwoFactor", authPostureLogPrefix)
		}
	}

	if enabled, err := s.GetLdapEnable(); err == nil && enabled {
		if filter, err := s.GetLdapAdminFilter(); err == nil && strings.TrimSpace(filter) == "" {
			logger.Errorf("%s LDAP is enabled but ldapAdminFilter is empty, so signing in to the PANEL through LDAP is off. "+
				"Client sync is unaffected. This is the safe default: without a filter, every account matching ldapUserFilter could sign in here as an administrator. "+
				`Set one from a root shell, e.g: p-ui setting -ldapAdminFilter "(memberOf=cn=panel-admins,ou=groups,dc=example,dc=com)"`, authPostureLogPrefix)
		}
	}
}
