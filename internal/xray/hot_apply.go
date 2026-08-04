package xray

import (
	"encoding/json"

	"github.com/Arman2122/p-ui/internal/logger"
)

/*
Applying a hot diff.

This is the other half of hot_diff.go and lived in internal/web/service, which
meant the Xray core could not apply its own changes without the web layer doing
it on its behalf. Computing the diff and applying it belong together.

Every step is best-effort in one direction only: on any failure the caller falls
back to a full process restart, which cleans up whatever was partially applied.
That is why a half-applied diff is safe and why nothing here rolls back.
*/

// ApplyHotDiff reconciles a running instance with diff through the core API.
// It returns false on the first failure, leaving the caller to restart.
func ApplyHotDiff(api *XrayAPI, diff *HotDiff) bool {
	if api == nil || diff == nil {
		return false
	}
	// Removals first, so changed handlers and port swaps never collide with the
	// additions that follow.
	for _, u := range diff.RemovedUsers {
		if err := api.RemoveUser(u.Tag, u.Email); err != nil && !IsMissingHandlerErr(err) {
			logger.Info("hot apply: remove user [", u.Email, "] from [", u.Tag, "] failed:", err)
			return false
		}
	}
	for _, tag := range diff.RemovedInboundTags {
		if err := api.DelInbound(tag); err != nil && !IsMissingHandlerErr(err) {
			logger.Info("hot apply: remove inbound [", tag, "] failed:", err)
			return false
		}
	}
	for _, tag := range diff.RemovedOutboundTags {
		if err := api.DelOutbound(tag); err != nil && !IsMissingHandlerErr(err) {
			logger.Info("hot apply: remove outbound [", tag, "] failed:", err)
			return false
		}
	}
	for _, ob := range diff.AddedOutbounds {
		if err := addOutboundReconciling(api, ob); err != nil {
			logger.Info("hot apply: add outbound failed:", err)
			return false
		}
	}
	for _, ib := range diff.AddedInbounds {
		if err := addInboundReconciling(api, ib); err != nil {
			logger.Info("hot apply: add inbound failed:", err)
			return false
		}
	}
	for _, u := range diff.AddedUsers {
		if err := addUserReconciling(api, u); err != nil {
			logger.Info("hot apply: add user [", u.Email, "] to [", u.Tag, "] failed:", err)
			return false
		}
	}
	if diff.RoutingConfig != nil {
		if err := api.ApplyRoutingConfig(diff.RoutingConfig); err != nil {
			logger.Info("hot apply: apply routing config failed:", err)
			return false
		}
	}
	return true
}

// addUserReconciling adds a user, and on an email conflict (the user was
// already applied through the runtime API) replaces the existing user instead.
func addUserReconciling(api *XrayAPI, u UserOp) error {
	err := api.AddUser(u.Protocol, u.Tag, u.User)
	if err == nil || !IsUserExistsErr(err) {
		return err
	}
	if delErr := api.RemoveUser(u.Tag, u.Email); delErr != nil && !IsMissingHandlerErr(delErr) {
		return delErr
	}
	return api.AddUser(u.Protocol, u.Tag, u.User)
}

// addInboundReconciling adds an inbound, and on a tag conflict (the handler was
// already created through the runtime API while the stored snapshot was stale)
// replaces the existing handler instead.
func addInboundReconciling(api *XrayAPI, inbound []byte) error {
	err := api.AddInbound(inbound)
	if err == nil || !IsExistingTagErr(err) {
		return err
	}
	tag, ok := tagOf(inbound)
	if !ok {
		return err
	}
	if delErr := api.DelInbound(tag); delErr != nil && !IsMissingHandlerErr(delErr) {
		return delErr
	}
	return api.AddInbound(inbound)
}

// addOutboundReconciling mirrors addInboundReconciling for outbounds.
func addOutboundReconciling(api *XrayAPI, outbound []byte) error {
	err := api.AddOutbound(outbound)
	if err == nil || !IsExistingTagErr(err) {
		return err
	}
	tag, ok := tagOf(outbound)
	if !ok {
		return err
	}
	if delErr := api.DelOutbound(tag); delErr != nil && !IsMissingHandlerErr(delErr) {
		return delErr
	}
	return api.AddOutbound(outbound)
}

func tagOf(raw []byte) (string, bool) {
	var meta struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Tag == "" {
		return "", false
	}
	return meta.Tag, true
}
