package service

import (
	"context"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
)

/*
resetCoreQuotas clears the daemon-side usage counter for renewed clients.

Asked of the registry rather than of a core by name: any core that pushes a
byte budget down answers QuotaEnforcer, and a renewed client whose daemon keeps
counting stays blocked by that daemon until it restarts, however the panel feels
about the client's quota. Call it after the client is back on its core: a
daemon clears nothing for a client it does not hold.
*/
func resetCoreQuotas(ctx context.Context, emails []string) {
	reg := registry()
	if reg == nil || len(emails) == 0 {
		return
	}
	for _, bound := range reg.Cores() {
		resetQuotasOn(ctx, bound, emails)
	}
}

// resetCoreQuotasForAll follows a panel-wide reset: each quota-enforcing core
// is asked to clear the counter of every client it is serving.
func resetCoreQuotasForAll(ctx context.Context) {
	reg := registry()
	if reg == nil {
		return
	}
	svc := &InboundService{}
	for _, bound := range reg.Cores() {
		if bound.Quota == nil {
			continue
		}
		instances, err := svc.DesiredInstances(bound.Core.Kinds())
		if err != nil {
			logger.Warning("quota reset skipped on core", bound.Core.Describe().ID, "— its inbounds could not be read:", err)
			continue
		}
		var emails []string
		for _, inst := range instances {
			for _, user := range inst.Users {
				emails = append(emails, user.Email)
			}
		}
		resetQuotasOn(ctx, bound, emails)
	}
}

func resetQuotasOn(ctx context.Context, bound *core.Bound, emails []string) {
	if bound.Quota == nil {
		return
	}
	for _, email := range emails {
		if err := bound.Quota.ResetQuota(ctx, email); err != nil {
			logger.Warning("quota reset failed for", email, "on core", bound.Core.Describe().ID, ":", err,
				"— the client may stay blocked by its daemon until that daemon restarts")
		}
	}
}
