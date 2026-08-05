package job

import (
	"context"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/xray"
)

// Cores is the registry the traffic job bills from, set by the web layer the
// way EventBus is. Nil in tests that never poll traffic.
var Cores *core.Registry

/*
collectCoreTraffic gathers one poll's usage from every core.

This is what replaced a traffic job per core. The shapes underneath differ —
one process answering gRPC, one sidecar per inbound answering HTTP — but the
question does not, so the loop is over capabilities rather than over protocols.

Per-user comes first on purpose: a core banks its tag deltas during that call,
because the underlying stats read is destructive and this is the only pass that
sees them. A core failing is skipped, never fatal — one dead daemon must not
stop the others being billed.
*/
func collectCoreTraffic() ([]*xray.Traffic, []*xray.ClientTraffic) {
	if Cores == nil {
		return nil, nil
	}
	ctx := context.Background()
	var (
		traffics []*xray.Traffic
		clients  []*xray.ClientTraffic
	)
	for _, bound := range Cores.Cores() {
		id := bound.Core.Describe().ID
		if bound.Traffic != nil {
			deltas, err := bound.Traffic.CollectTraffic(ctx)
			if err != nil {
				logger.Debug("core", id, "traffic collection failed:", err)
			}
			for _, d := range deltas {
				clients = append(clients, &xray.ClientTraffic{Email: d.Email, Up: d.Up, Down: d.Down})
			}
		}
		if bound.TagTraffic == nil {
			continue
		}
		tags, err := bound.TagTraffic.CollectTagTraffic(ctx)
		if err != nil {
			logger.Debug("core", id, "tag traffic collection failed:", err)
		}
		for _, t := range tags {
			traffics = append(traffics, &xray.Traffic{
				Tag:        t.Tag,
				IsInbound:  !t.Outbound,
				IsOutbound: t.Outbound,
				Up:         t.Up,
				Down:       t.Down,
			})
		}
	}
	return traffics, clients
}

// collectIdleOnline names the clients a core reports as connected that moved no
// bytes this poll. Those are the ones the delta heuristic cannot see.
func collectIdleOnline(deltaActive map[string]bool) []string {
	if Cores == nil {
		return nil
	}
	ctx := context.Background()
	var idle []string
	seen := make(map[string]bool)
	for _, bound := range Cores.Cores() {
		if bound.Online == nil {
			continue
		}
		emails, err := bound.Online.OnlineEmails(ctx)
		if err != nil {
			logger.Debug("core", bound.Core.Describe().ID, "online report failed:", err)
			continue
		}
		for _, email := range emails {
			if email == "" || deltaActive[email] || seen[email] {
				continue
			}
			seen[email] = true
			idle = append(idle, email)
		}
	}
	return idle
}
