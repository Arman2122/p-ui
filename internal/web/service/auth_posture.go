package service

import (
	"strings"

	"github.com/Arman2122/p-ui/internal/logger"
)

// authPostureLogPrefix is what an operator greps their journal for. Changing it
// breaks a search the changelog told people to run.
const authPostureLogPrefix = "[AUTH_POSTURE]"

// TLSPostureLogPrefix is the same contract for a listener that was configured
// for HTTPS and is serving cleartext. Exported because the panel and the
// subscription server are different packages with the same failure.
const TLSPostureLogPrefix = "[TLS_POSTURE]"

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
		// The panel offers ldap:// and ldaps:// and nothing else; there is no
		// StartTLS here, so an unencrypted transport cannot be upgraded later.
		if useTLS, err := s.GetLdapUseTLS(); err == nil && !useTLS {
			msg := "%s LDAP is enabled with ldapUseTLS off, so the panel talks to the directory in cleartext. " +
				"Every account identifier the sync reads is on the wire in plain text, on every run of ldapSyncCron. " +
				"This panel has no StartTLS, so the only encrypted option is ldaps: set ldapUseTLS true and ldapPort to 636."
			if bindDN, err := s.GetLdapBindDN(); err == nil && strings.TrimSpace(bindDN) != "" {
				msg += " A bind DN is configured, so its password crosses the network in plain text too."
			}
			if filter, err := s.GetLdapAdminFilter(); err == nil && strings.TrimSpace(filter) != "" {
				msg += " Panel sign-in through LDAP is on, so every administrator password is sent the same way."
			}
			logger.Errorf(msg, authPostureLogPrefix)
		}
	}
}
