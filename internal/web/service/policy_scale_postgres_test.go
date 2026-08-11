package service

import (
	"context"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
TestIPLimitPostgresScale measures the pass that runs every ten seconds forever.

The work is all batched lookups — the cap, the owning inbound and the persisted
row for every observed client — so a per-email query reintroduced anywhere shows
up here as a wall time that grows with the panel rather than with the number of
people actually connected. Run twice: the first pass creates the tracking rows
and the second takes the update path every later pass takes.
*/
func TestIPLimitPostgresScale(t *testing.T) {
	for _, n := range scaleSizes(t, 10000, 100000) {
		for _, observed := range []int{50, 1000} {
			t.Run(fmt.Sprintf("N=%d_observed=%d", n, observed), func(t *testing.T) {
				setupScaleDB(t)
				ds := seedScaleDataset(t, n, 50)
				if err := database.GetDB().Model(&model.ClientRecord{}).
					Where("1 = 1").Update("limit_ip", 1).Error; err != nil {
					t.Fatalf("set limit_ip: %v", err)
				}

				base := &declaredCore{id: "proxy", kind: "vless"}
				installCores(t, newProxyCore(base))
				svc := &PolicyService{}

				// Two addresses each, so every observed client is over its cap and
				// the ban path is measured rather than skipped.
				at := time.Now().Add(-time.Minute)
				m := min(observed, len(ds.emails))
				for i := range m {
					base.sessions = append(base.sessions,
						core.Session{
							Email:             ds.emails[i],
							Source:            netip.MustParseAddr(fmt.Sprintf("10.%d.%d.1", i/250, i%250+1)),
							LastSeenUnixMilli: at.Unix() * 1000,
						},
						core.Session{
							Email:             ds.emails[i],
							Source:            netip.MustParseAddr(fmt.Sprintf("10.%d.%d.2", i/250, i%250+1)),
							LastSeenUnixMilli: at.Add(time.Second).Unix() * 1000,
						})
				}

				start := time.Now()
				probe := AnyClientHasAnIPLimit()
				gate := time.Since(start)
				if !probe {
					t.Fatal("AnyClientHasAnIPLimit = false with every client capped")
				}

				start = time.Now()
				scan := svc.ObserveSessions(context.Background())
				observeFor := time.Since(start)

				start = time.Now()
				first, err := svc.EvaluateIPLimits(scan, true)
				if err != nil {
					t.Fatalf("first pass: %v", err)
				}
				firstFor := time.Since(start)

				start = time.Now()
				second, err := svc.EvaluateIPLimits(scan, true)
				if err != nil {
					t.Fatalf("second pass: %v", err)
				}
				secondFor := time.Since(start)

				t.Logf("N=%-7d observed=%-5d gate=%-8v observe=%-8v first=%-10v second=%-10v (%.2fms/client)",
					n, m, gate.Round(time.Millisecond), observeFor.Round(time.Millisecond),
					firstFor.Round(time.Millisecond), secondFor.Round(time.Millisecond),
					float64(secondFor.Milliseconds())/float64(m))

				// The pass has to reach every observed client on both passes, or the
				// timings above are measuring a shortcut rather than the work.
				if len(first) != m || len(second) != m {
					t.Fatalf("verdicts: first %d, second %d, want %d each", len(first), len(second), m)
				}
				var rows int64
				if err := database.GetDB().Model(&model.InboundClientIps{}).Count(&rows).Error; err != nil {
					t.Fatalf("count tracking rows: %v", err)
				}
				if rows != int64(m) {
					t.Fatalf("inbound_client_ips rows = %d, want %d", rows, m)
				}
			})
		}
	}
}
