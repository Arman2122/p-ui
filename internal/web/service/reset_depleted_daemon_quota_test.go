package service

import (
	"context"
	"testing"
)

/*
A reset clears the daemon-side counter of the client that was depleted.

The by-name MTProto reset converges the DESIRED set and resets the quota of
every secret in it — and the desired set excludes a depleted client on
purpose. The reset then re-enabled that client, whose secret came back to a
sidecar still holding the old counter, so the renewed client stayed blocked
until the sidecar restarted. Every reset path now asks the core registry to
clear the counters of exactly the clients it brought back. Feature gap A8.
*/
type quotaRecorder struct {
	*proxyCore
	resets []string
}

func (c *quotaRecorder) ResetQuota(_ context.Context, email string) error {
	c.resets = append(c.resets, email)
	return nil
}

func TestEveryResetPathClearsTheDaemonCounterOfADepletedClient(t *testing.T) {
	tests := []struct {
		name  string
		port  int
		tag   string
		email string
		reset func(t *testing.T, inboundID int, email string)
	}{
		{
			name: "the periodic reset job", port: 29021, tag: "in-q-periodic", email: "q-periodic@x",
			reset: func(t *testing.T, inboundID int, _ string) {
				if err := (&ClientService{}).ResetAllClientTraffics(&InboundService{}, inboundID); err != nil {
					t.Fatalf("ResetAllClientTraffics: %v", err)
				}
			},
		},
		{
			name: "the Clients page reset-all button", port: 29022, tag: "in-q-all", email: "q-all@x",
			reset: func(t *testing.T, _ int, _ string) {
				if _, err := (&ClientService{}).ResetAllTraffics(&InboundService{}); err != nil {
					t.Fatalf("ResetAllTraffics: %v", err)
				}
			},
		},
		{
			name: "the Telegram bot's reset traffic", port: 29023, tag: "in-q-bot", email: "q-bot@x",
			reset: func(t *testing.T, _ int, email string) {
				if err := (&InboundService{}).ResetClientTrafficByEmail(email); err != nil {
					t.Fatalf("ResetClientTrafficByEmail: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			initTestDB(t)
			withFakeEgressKernel(t)
			recorder := &quotaRecorder{proxyCore: newProxyCore(&declaredCore{id: "proxy", kind: "vless"})}
			installCores(t, recorder)
			inbound := seedDepletableClient(t, tc.port, tc.tag, tc.email)
			if _, _, err := (&InboundService{}).AddTraffic(nil, nil); err != nil {
				t.Fatalf("AddTraffic: %v", err)
			}
			if got := readEnableLocations(t, inbound.Id, tc.email); got.traffic {
				t.Fatal("the client was not depleted; the test has nothing to reset")
			}

			tc.reset(t, inbound.Id, tc.email)

			for _, email := range recorder.resets {
				if email == tc.email {
					return
				}
			}
			t.Fatalf("the daemon counter of %s was never reset (resets seen: %v); the renewed client stays blocked by its daemon", tc.email, recorder.resets)
		})
	}
}
