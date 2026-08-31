package service

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/common"
)

// openProxyLogPrefix is what an operator greps their journal for. Changing it
// breaks a search the changelog told people to run.
const openProxyLogPrefix = "[OPEN_PROXY]"

// socksAuthPassword is the ONE value xray reads as "check the accounts".
const socksAuthPassword = "password"

// authGatesAccounts asks the one capability table whether this inbound's
// settings.auth decides if its accounts are checked. Asked rather than
// switched on the protocol: the dispatch ratchet is right that a second place
// deciding what a protocol IS is how the first three copies happened.
func authGatesAccounts(inbound *model.Inbound) bool {
	return core.Can(core.CapAccountAuthGate, core.Facts{Protocol: string(inbound.Protocol)})
}

// accountAuthShape reads the two settings fields that decide enforcement.
// present is false for any inbound this question does not apply to.
func accountAuthShape(inbound *model.Inbound) (auth string, accounts int, present bool) {
	if inbound == nil || !authGatesAccounts(inbound) || strings.TrimSpace(inbound.Settings) == "" {
		return "", 0, false
	}
	var settings struct {
		Auth     string            `json:"auth"`
		Accounts []json.RawMessage `json:"accounts"`
		Users    []json.RawMessage `json:"users"`
	}
	if json.Unmarshal([]byte(inbound.Settings), &settings) != nil {
		return "", 0, false
	}
	// xray reads `accounts` in preference to `users` and falls back to it, so a
	// payload naming either one carries credentials.
	count := len(settings.Accounts)
	if count == 0 {
		count = len(settings.Users)
	}
	return settings.Auth, count, true
}

/*
refuseUnenforcedAccounts refuses an inbound whose credentials would not be checked.

xray's SocksServerConfig.Build maps auth=="password" to AuthType_PASSWORD and
EVERY other value — an absent key included — to AuthType_NO_AUTH; its own
warning for the unknown case is commented out, so nothing says so. It still
parses `accounts` into config.Accounts, and proxy/socks consults that map only
under AuthType_PASSWORD. The result is an open proxy on a public port while the
panel displays the credentials it is not enforcing.

The form always sends auth, so this is not reachable from the UI. It is
reachable through the REST API, through /inbounds/import — which bypasses every
validate: tag — and through the form's raw-JSON editor, whose payload is pruned
rather than re-parsed through the schema.
*/
func refuseUnenforcedAccounts(inbound *model.Inbound) error {
	auth, accounts, present := accountAuthShape(inbound)
	if !present || accounts == 0 || auth == socksAuthPassword {
		return nil
	}
	named := "no auth mode is set"
	if auth != "" {
		named = "auth is \"" + auth + "\""
	}
	return common.NewError("this inbound carries " + strconv.Itoa(accounts) +
		" account(s) but " + named + ", so xray accepts every connection on this port without checking them. " +
		"Set auth to \"password\" to enforce the accounts, or remove them to run this as a deliberate open proxy.")
}

/*
LogOpenProxyInbounds names every stored inbound whose credentials are not being
checked, at Error, on every boot.

The refusal above only protects new writes. A row already in this state is an
open proxy running right now, and a changelog line does not reach a box that is
already deployed — so this reports rather than repairs. Nothing is disabled and
nothing is rewritten: the settings are the operator's, and a panel that silently
edits them is the behaviour this campaign spent itself removing.

Once, at startup, would make it a notice. It is not a notice; it is a door
standing open, so it says so every time the panel starts.
*/
func (s *InboundService) LogOpenProxyInbounds() {
	for _, inbound := range s.OpenProxyInbounds() {
		auth, accounts, _ := accountAuthShape(inbound)
		logger.Errorf("%s inbound id=%d tag=%q port=%d: %d account(s) configured but auth is %q, so every connection is accepted without checking them",
			openProxyLogPrefix, inbound.Id, inbound.Tag, inbound.Port, accounts, auth)
	}
}

// OpenProxyInbounds returns the stored inbounds whose credentials the core
// would never check. Separated from the logging so the scan can be asserted
// against a real database rather than against captured log output.
func (s *InboundService) OpenProxyInbounds() []*model.Inbound {
	db := database.GetDB()
	if db == nil {
		return nil
	}
	// Every inbound, filtered by the capability rather than by a protocol list:
	// a filter here would be the same dispatch in SQL.
	var inbounds []*model.Inbound
	if err := db.Model(&model.Inbound{}).Find(&inbounds).Error; err != nil {
		logger.Warning("open-proxy scan skipped:", err)
		return nil
	}
	var open []*model.Inbound
	for _, inbound := range inbounds {
		if refuseUnenforcedAccounts(inbound) != nil {
			open = append(open, inbound)
		}
	}
	return open
}

/*
validateInstanceForKind refuses an inbound its own core cannot serve.

The sibling of validateClientForKind, one level up. A kind whose core declares
no validator is saved unvalidated, exactly as every kind was before — this adds
a refusal where one is available, it does not invent a policy for kinds that
have none.
*/
func validateInstanceForKind(inbound *model.Inbound) error {
	if inbound == nil {
		return nil
	}
	kind := core.Kind(inbound.Protocol)
	validator, ok := cores.InstanceValidatorFor(kind)
	if !ok {
		return nil
	}
	return validator.ValidateInstance(kind, inbound.Settings)
}

/*
refuseOnlyIfWorsened judges an edit against what it replaces.

A save-time refusal applied to rows that already violate it locks an operator
out of the row they most need to reach. The panel would refuse a renamed remark
on an inbound that has been running -- badly -- since before the rule existed;
worse, it would refuse to turn OFF an inbound the rule is warning about, and the
only way out would be deleting it and its clients.

So an edit that leaves a violation where it found it is allowed, and one that
introduces it is stopped. Create has no "before" and is judged on its own. The
carried-forward violation is logged rather than swallowed: it is still wrong,
and an operator who never sees it cannot go and fix it.

This is the rule for every save-time refusal on the edit path, not a decision
per refusal -- which is what makes it a rule and not a habit. Q1.
*/
func refuseOnlyIfWorsened(what string, oldInbound, inbound *model.Inbound, check func(*model.Inbound) error) error {
	err := check(inbound)
	if err == nil {
		return nil
	}
	if oldInbound == nil {
		return err
	}
	if check(oldInbound) != nil {
		logger.Warning("inbound", inbound.Tag, "is saved still carrying a", what,
			"its save path refuses, because this edit did not introduce it:", err)
		return nil
	}
	return err
}
