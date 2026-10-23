package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *InboundService) GetAllInboundClientIps() ([]model.InboundClientIps, error) {
	db := database.GetDB()
	var ips []model.InboundClientIps
	err := db.Model(&model.InboundClientIps{}).Find(&ips).Error
	return ips, err
}

func (s *InboundService) GetNodeInboundClientIps(nodeID int) ([]model.InboundClientIps, error) {
	db := database.GetDB()
	emails := db.Model(&model.NodeClientTraffic{}).Select("email").Where("node_id = ?", nodeID)
	var ips []model.InboundClientIps
	err := db.Where("client_email IN (?)", emails).Find(&ips).Error
	return ips, err
}

// clientIpStaleAfterSeconds mirrors job.ipStaleAfterSeconds: client IPs older than
// 30 minutes are evicted. Applying the same cutoff inside the cross-node merge keeps
// the synced blob bounded and stops the master's push-back from resurrecting IPs that
// a node has already pruned (otherwise the merge defeats the eviction cluster-wide).
const clientIpStaleAfterSeconds = int64(30 * 60)

// clientIpEntry is the on-disk shape of each element of InboundClientIps.Ips: the
// same wire shape the per-node attribution rows and every node sync carry.
type clientIpEntry = model.ClientIpEntry

/*
mergeClientIpEntries unions old and incoming observations, dropping anything
older than cutoff, and returns the result sorted newest-first. One address is one
run across the cluster: the later sighting and the earlier recorded start both
win, so a node's own start for a live session survives the master's push-back.
*/
func mergeClientIpEntries(old, incoming []clientIpEntry, cutoff int64) []clientIpEntry {
	byIP := make(map[string]clientIpEntry, len(old)+len(incoming))
	for _, e := range old {
		if e.IP == "" || e.Timestamp < cutoff {
			continue
		}
		byIP[e.IP] = e
	}
	for _, e := range incoming {
		if e.IP == "" || e.Timestamp < cutoff {
			continue
		}
		cur, ok := byIP[e.IP]
		if !ok {
			byIP[e.IP] = e
			continue
		}
		cur.Timestamp = max(cur.Timestamp, e.Timestamp)
		cur.FirstSeen = earliestStart(cur.FirstSeen, e.FirstSeen)
		byIP[e.IP] = cur
	}
	out := make([]clientIpEntry, 0, len(byIP))
	for _, e := range byIP {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })
	return out
}

// earliestStart is the earlier of two run starts, ignoring one nobody recorded.
func earliestStart(a, b int64) int64 {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	return min(a, b)
}

// MergeInboundClientIps folds client IPs synced from another node into the local
// inbound_client_ips table without double-counting an IP seen on multiple nodes and
// without resurrecting stale entries. Existing rows are updated in place; brand-new
// clients (typically node-only clients with no local row) are created with a fresh
// local id.
func (s *InboundService) MergeInboundClientIps(incomingIps []model.InboundClientIps) error {
	db := database.GetDB()
	var currentIps []model.InboundClientIps
	if err := db.Model(&model.InboundClientIps{}).Find(&currentIps).Error; err != nil {
		return err
	}

	currentMap := make(map[string]*model.InboundClientIps, len(currentIps))
	for i := range currentIps {
		currentMap[currentIps[i].ClientEmail] = &currentIps[i]
	}

	now := time.Now().Unix()
	cutoff := now - clientIpStaleAfterSeconds

	// Node syncs run concurrently (one goroutine per node) and shared clients
	// appear in several nodes' reports. Locking rows in each node's arbitrary
	// report order lets two merges grab the same rows in opposite order, which
	// Postgres aborts as a deadlock (40P01) — take them in one global order.
	sort.Slice(incomingIps, func(i, j int) bool {
		return incomingIps[i].ClientEmail < incomingIps[j].ClientEmail
	})

	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, incoming := range incomingIps {
		if incoming.ClientEmail == "" || incoming.Ips == "" {
			continue
		}

		var incomingEntries []clientIpEntry
		_ = json.Unmarshal([]byte(incoming.Ips), &incomingEntries)

		current, exists := currentMap[incoming.ClientEmail]
		if !exists {
			// New client we've never seen locally. Drop stale entries up front and
			// skip the row entirely if nothing is fresh, so we don't persist a row
			// that is dead on arrival.
			fresh := mergeClientIpEntries(nil, incomingEntries, cutoff)
			if len(fresh) == 0 {
				continue
			}
			b, _ := json.Marshal(fresh)
			incoming.Ips = string(b)
			// Never carry the remote node's primary key into the local table: id
			// spaces are independent across nodes and the remote id would collide
			// with an unrelated local row. OnConflict guards the race where
			// check_client_ip_job creates the same brand-new email between the
			// snapshot above and this insert.
			incoming.Id = 0
			result := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "client_email"}},
				DoNothing: true,
			}).Create(&incoming)
			if result.Error != nil {
				tx.Rollback()
				return result.Error
			}
			if result.RowsAffected > 0 {
				currentMap[incoming.ClientEmail] = &incoming
				continue
			}
			current = &model.InboundClientIps{}
			if err := tx.Where("client_email = ?", incoming.ClientEmail).First(current).Error; err != nil {
				tx.Rollback()
				return err
			}
		}

		if err := mergeExistingClientIps(tx, current.Id, incomingEntries, cutoff); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

// Re-read and merge after a lost compare-and-set so concurrent nodes keep both sightings.
func mergeExistingClientIps(tx *gorm.DB, id int, incoming []clientIpEntry, cutoff int64) error {
	for range 8 {
		var current model.InboundClientIps
		if err := tx.First(&current, id).Error; err != nil {
			return err
		}
		var existing []clientIpEntry
		_ = json.Unmarshal([]byte(current.Ips), &existing)
		encoded, err := json.Marshal(mergeClientIpEntries(existing, incoming, cutoff))
		if err != nil {
			return err
		}
		if current.Ips == string(encoded) {
			return nil
		}
		result := tx.Model(&model.InboundClientIps{}).
			Where("id = ? AND COALESCE(ips, '') = ?", id, current.Ips).Update("ips", string(encoded))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}
	}
	return fmt.Errorf("client IP merge changed concurrently too often for row %d", id)
}

func (s *InboundService) UpdateClientIPs(tx *gorm.DB, oldEmail string, newEmail string) error {
	// The caller only renames onto a free identity, so a row already sitting on
	// newEmail is stale tracking data — drop it instead of failing the edit.
	if oldEmail != newEmail {
		if err := tx.Where("client_email = ?", newEmail).Delete(model.InboundClientIps{}).Error; err != nil {
			return err
		}
	}
	return tx.Model(model.InboundClientIps{}).Where("client_email = ?", oldEmail).Update("client_email", newEmail).Error
}

func (s *InboundService) DelClientIPs(tx *gorm.DB, email string) error {
	return tx.Where("client_email = ?", email).Delete(model.InboundClientIps{}).Error
}

func (s *InboundService) delClientIPsByEmails(tx *gorm.DB, emails []string) error {
	const chunk = 400
	for start := 0; start < len(emails); start += chunk {
		end := min(start+chunk, len(emails))
		if err := tx.Where("client_email IN ?", emails[start:end]).Delete(model.InboundClientIps{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *InboundService) GetInboundClientIps(clientEmail string) (string, error) {
	db := database.GetDB()
	InboundClientIps := &model.InboundClientIps{}
	err := db.Model(model.InboundClientIps{}).Where("client_email = ?", clientEmail).First(InboundClientIps).Error
	if err != nil {
		return "", err
	}

	if InboundClientIps.Ips == "" {
		return "", nil
	}

	// Try to parse as new format (with timestamps)
	type IPWithTimestamp struct {
		IP        string `json:"ip"`
		Timestamp int64  `json:"timestamp"`
	}

	var ipsWithTime []IPWithTimestamp
	err = json.Unmarshal([]byte(InboundClientIps.Ips), &ipsWithTime)

	// If successfully parsed as new format, return with timestamps
	if err == nil && len(ipsWithTime) > 0 {
		return InboundClientIps.Ips, nil
	}

	// Otherwise, assume it's old format (simple string array)
	// Try to parse as simple array and convert to new format
	var oldIps []string
	err = json.Unmarshal([]byte(InboundClientIps.Ips), &oldIps)
	if err == nil && len(oldIps) > 0 {
		// Convert old format to new format with current timestamp
		newIpsWithTime := make([]IPWithTimestamp, len(oldIps))
		for i, ip := range oldIps {
			newIpsWithTime[i] = IPWithTimestamp{
				IP:        ip,
				Timestamp: time.Now().Unix(),
			}
		}
		result, _ := json.Marshal(newIpsWithTime)
		return string(result), nil
	}

	// Return as-is if parsing fails
	return InboundClientIps.Ips, nil
}

func (s *InboundService) ClearClientIps(clientEmail string) error {
	db := database.GetDB()

	result := db.Model(model.InboundClientIps{}).
		Where("client_email = ?", clientEmail).
		Update("ips", "")
	err := result.Error
	if err != nil {
		return err
	}
	return nil
}
