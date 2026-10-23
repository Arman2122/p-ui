package service

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/runtime"

	"gorm.io/gorm"
)

func foreignNodeClientEmails(db *gorm.DB, nodeID int, emails []string) (map[string]bool, error) {
	for i := range emails {
		emails[i] = strings.ToLower(strings.TrimSpace(emails[i]))
	}
	emails = uniqueNonEmptyStrings(emails)
	foreign := make(map[string]bool)
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var claims []struct {
			Email string
			Owned bool
		}
		if err := db.Table("clients c").Select("c.email, BOOL_OR(COALESCE(i.node_id = ?, false)) AS owned", nodeID).
			Joins("JOIN client_inbounds ci ON ci.client_id = c.id").
			Joins("JOIN inbounds i ON i.id = ci.inbound_id").
			Where("LOWER(c.email) IN ?", batch).Group("c.email").Scan(&claims).Error; err != nil {
			return nil, err
		}
		for _, claim := range claims {
			if !claim.Owned {
				foreign[strings.ToLower(claim.Email)] = true
			}
		}
	}
	return foreign, nil
}

// Reject foreign identities before either traffic or credentials can be merged.
// A copy preserves the original wire snapshot and its reconcile fingerprint.
func filterNodeClientOwnership(db *gorm.DB, nodeID int, snap *runtime.TrafficSnapshot) (*runtime.TrafficSnapshot, error) {
	emails := slices.Clone(snap.OnlineEmails)
	for _, group := range snap.OnlineTree {
		emails = append(emails, group...)
	}
	for email := range snap.LastOnlineMap {
		emails = append(emails, email)
	}
	for _, inbound := range snap.Inbounds {
		if inbound == nil {
			continue
		}
		for _, traffic := range inbound.ClientStats {
			emails = append(emails, traffic.Email)
		}
		if clients, err := ParseInboundSettingsClients(inbound.Settings); err == nil {
			for _, client := range clients {
				emails = append(emails, client.Email)
			}
		}
	}
	foreign, err := foreignNodeClientEmails(db, nodeID, emails)
	if err != nil || len(foreign) == 0 {
		return snap, err
	}
	rejected := func(email string) bool { return foreign[strings.ToLower(strings.TrimSpace(email))] }
	filtered := *snap
	filtered.Inbounds = make([]*model.Inbound, len(snap.Inbounds))
	for i, inbound := range snap.Inbounds {
		if inbound == nil {
			continue
		}
		copyInbound := *inbound
		copyInbound.ClientStats = slices.DeleteFunc(slices.Clone(inbound.ClientStats), func(traffic core.ClientTraffic) bool { return rejected(traffic.Email) })
		copyInbound.Settings = filterSnapshotClientSettings(inbound.Settings, rejected)
		filtered.Inbounds[i] = &copyInbound
	}
	filtered.OnlineEmails = slices.DeleteFunc(slices.Clone(snap.OnlineEmails), rejected)
	filtered.OnlineTree = make(map[string][]string, len(snap.OnlineTree))
	for guid, group := range snap.OnlineTree {
		filtered.OnlineTree[guid] = slices.DeleteFunc(slices.Clone(group), rejected)
	}
	filtered.LastOnlineMap = make(map[string]int64, len(snap.LastOnlineMap))
	for email, at := range snap.LastOnlineMap {
		if !rejected(email) {
			filtered.LastOnlineMap[email] = at
		}
	}
	logger.Warningf("node %d reported %d client identities attached only elsewhere; ignoring those claims", nodeID, len(foreign))
	return &filtered, nil
}

func filterSnapshotClientSettings(settings string, rejected func(string) bool) string {
	var parsed map[string]json.RawMessage
	if json.Unmarshal([]byte(settings), &parsed) != nil {
		return settings
	}
	var clients []json.RawMessage
	if json.Unmarshal(parsed["clients"], &clients) != nil {
		return settings
	}
	kept := make([]json.RawMessage, 0, len(clients))
	for _, raw := range clients {
		var client struct {
			Email string `json:"email"`
		}
		if json.Unmarshal(raw, &client) != nil || !rejected(client.Email) {
			kept = append(kept, raw)
		}
	}
	if len(kept) == len(clients) {
		return settings
	}
	parsed["clients"], _ = json.Marshal(kept)
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return settings
	}
	return string(encoded)
}
