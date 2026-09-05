package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// seedUnionRow is what a pass or a node sync leaves behind: the client's
// persisted addresses, each last seen at the given moment.
func seedUnionRow(t *testing.T, email string, seen time.Time, addresses ...string) {
	t.Helper()
	entries := make([]model.ClientIpEntry, 0, len(addresses))
	for _, address := range addresses {
		entries = append(entries, model.ClientIpEntry{IP: address, Timestamp: seen.Unix(), FirstSeen: seen.Add(-time.Minute).Unix()})
	}
	blob, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.InboundClientIps{ClientEmail: email, Ips: string(blob)}).Error; err != nil {
		t.Fatalf("seed union row: %v", err)
	}
}

func admission(email, source string, limit int, held ...string) core.Admission {
	a := core.Admission{Kind: "ssh", InstanceID: 7, Email: email, Source: netip.MustParseAddr(source), LimitIP: limit}
	for _, h := range held {
		a.Held = append(a.Held, netip.MustParseAddr(h))
	}
	return a
}

/*
The cap is per client, not per core: a device live on ANOTHER core or node holds
a slot against a login here. The core hands over the sources it holds itself;
the panel adds the cluster's within the live window, and only that window — an
address seen half an hour ago is history, not a device.
*/
func TestAdmitLoginCountsTheClusterAgainstTheCap(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	t.Run("a device live elsewhere fills the last slot", func(t *testing.T) {
		seedUnionRow(t, "shared", time.Now().Add(-5*time.Second), "203.0.113.7")
		err := svc.AdmitLogin(context.Background(), admission("shared", "198.51.100.9", 1))
		if !errors.Is(err, core.ErrDeviceLimit) {
			t.Fatalf("a new device was admitted past a cap another node's device fills: %v", err)
		}
	})

	t.Run("that same device logging in again is let back in", func(t *testing.T) {
		if err := svc.AdmitLogin(context.Background(), admission("shared", "203.0.113.7", 1)); err != nil {
			t.Fatalf("a device already holding its slot was refused: %v", err)
		}
	})

	t.Run("an address past the live window is history, not a device", func(t *testing.T) {
		seedUnionRow(t, "roamer", time.Now().Add(-ipLimitLiveWindow-time.Second), "203.0.113.8")
		if err := svc.AdmitLogin(context.Background(), admission("roamer", "198.51.100.9", 1)); err != nil {
			t.Fatalf("a stale address held a slot against a live login: %v", err)
		}
	})

	t.Run("the core's own held sources count without any row", func(t *testing.T) {
		err := svc.AdmitLogin(context.Background(), admission("local", "198.51.100.9", 2, "198.51.100.1", "198.51.100.2"))
		if !errors.Is(err, core.ErrDeviceLimit) {
			t.Fatalf("two devices the core itself holds did not fill a cap of two: %v", err)
		}
	})

	t.Run("no cap is no question", func(t *testing.T) {
		if err := svc.AdmitLogin(context.Background(), admission("free", "198.51.100.9", 0, "198.51.100.1", "198.51.100.2", "198.51.100.3")); err != nil {
			t.Fatalf("an uncapped client was refused: %v", err)
		}
	})
}
