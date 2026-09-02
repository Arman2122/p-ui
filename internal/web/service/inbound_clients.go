package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

// enrichClientStats parses each inbound's clients once, fills in the
// UUID/SubId fields on the preloaded ClientStats, and tops up rows owned by
// a sibling inbound (shared-email mode — the row is keyed on email so it
// only preloads on its owning inbound).
func (s *InboundService) enrichClientStats(db *gorm.DB, inbounds []*model.Inbound) {
	if len(inbounds) == 0 {
		return
	}
	clientsByInbound := s.backfillClientStats(db, inbounds)
	for i, inbound := range inbounds {
		clients := clientsByInbound[i]
		if len(clients) == 0 || len(inbound.ClientStats) == 0 {
			continue
		}
		cMap := make(map[string]model.Client, len(clients))
		for _, c := range clients {
			cMap[strings.ToLower(c.Email)] = c
		}
		for j := range inbound.ClientStats {
			email := strings.ToLower(inbound.ClientStats[j].Email)
			if c, ok := cMap[email]; ok {
				inbound.ClientStats[j].UUID = c.ID
				inbound.ClientStats[j].SubId = c.SubID
			}
		}
	}
}

// backfillClientStats tops up each inbound's preloaded ClientStats with rows
// owned by a sibling inbound: client_traffics is keyed on email, so a client
// attached to several inbounds has one row that only preloads on the inbound
// it was created on. Returns the parsed clients per inbound for reuse.
func (s *InboundService) backfillClientStats(db *gorm.DB, inbounds []*model.Inbound) [][]model.Client {
	clientsByInbound := make([][]model.Client, len(inbounds))
	seenByInbound := make([]map[string]struct{}, len(inbounds))
	missing := make(map[string]struct{})
	for i, inbound := range inbounds {
		clients, _ := s.GetClients(inbound)
		clientsByInbound[i] = clients
		seen := make(map[string]struct{}, len(inbound.ClientStats))
		for _, st := range inbound.ClientStats {
			if st.Email != "" {
				seen[strings.ToLower(st.Email)] = struct{}{}
			}
		}
		seenByInbound[i] = seen
		for _, c := range clients {
			if c.Email == "" {
				continue
			}
			if _, ok := seen[strings.ToLower(c.Email)]; !ok {
				missing[c.Email] = struct{}{}
			}
		}
	}
	if len(missing) > 0 {
		emails := make([]string, 0, len(missing))
		for e := range missing {
			emails = append(emails, e)
		}
		var extra []xray.ClientTraffic
		var loadErr error
		for _, batch := range chunkStrings(emails, sqlInChunk) {
			var page []xray.ClientTraffic
			if err := db.Model(xray.ClientTraffic{}).Where("email IN ?", batch).Find(&page).Error; err != nil {
				loadErr = err
				break
			}
			extra = append(extra, page...)
		}
		if loadErr != nil {
			logger.Warning("backfillClientStats:", loadErr)
		} else {
			byEmail := make(map[string]xray.ClientTraffic, len(extra))
			for _, st := range extra {
				byEmail[strings.ToLower(st.Email)] = st
			}
			for i, inbound := range inbounds {
				for _, c := range clientsByInbound[i] {
					if c.Email == "" {
						continue
					}
					key := strings.ToLower(c.Email)
					if _, ok := seenByInbound[i][key]; ok {
						continue
					}
					if st, ok := byEmail[key]; ok {
						inbound.ClientStats = append(inbound.ClientStats, st)
						seenByInbound[i][key] = struct{}{}
					}
				}
			}
		}
	}
	return clientsByInbound
}

// emailUsedByOtherInbounds reports whether email lives in any inbound other
// than exceptInboundId. Empty email returns false.
func (s *InboundService) emailUsedByOtherInbounds(email string, exceptInboundId int) (bool, error) {
	if email == "" {
		return false, nil
	}
	db := database.GetDB()
	var count int64
	query := fmt.Sprintf(
		"SELECT COUNT(*) %s WHERE inbounds.id != ? AND LOWER(%s) = LOWER(?)",
		database.JSONClientsFromInbound(),
		database.JSONFieldText("client.value", "email"),
	)
	if err := db.Raw(query, exceptInboundId, email).Scan(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *InboundService) emailsUsedByOtherInbounds(emails []string, exceptInboundId int) (map[string]bool, error) {
	shared := make(map[string]bool, len(emails))
	want := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			want[e] = struct{}{}
		}
	}
	if len(want) == 0 {
		return shared, nil
	}
	db := database.GetDB()
	var rows []string
	query := fmt.Sprintf(
		"SELECT DISTINCT LOWER(%s) %s WHERE inbounds.id != ?",
		database.JSONFieldText("client.value", "email"),
		database.JSONClientsFromInbound(),
	)
	if err := db.Raw(query, exceptInboundId).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, e := range rows {
		e = strings.ToLower(strings.TrimSpace(e))
		if _, ok := want[e]; ok {
			shared[e] = true
		}
	}
	return shared, nil
}

func (s *InboundService) GetClientInboundByTrafficID(trafficId int) (traffic *xray.ClientTraffic, inbound *model.Inbound, err error) {
	db := database.GetDB()
	var traffics []*xray.ClientTraffic
	err = db.Model(xray.ClientTraffic{}).Where("id = ?", trafficId).Find(&traffics).Error
	if err != nil {
		logger.Warningf("Error retrieving ClientTraffic with trafficId %d: %v", trafficId, err)
		return nil, nil, err
	}
	if len(traffics) == 0 {
		return nil, nil, nil
	}
	traffic = traffics[0]

	inbound, err = s.GetInbound(traffic.InboundId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// client_traffics.inbound_id goes stale when an inbound is deleted and
		// recreated; fall back to the authoritative client_inbounds link by email.
		ids, idErr := s.clientService.GetInboundIdsForEmail(db, traffic.Email)
		if idErr != nil {
			return traffic, nil, idErr
		}
		if len(ids) > 0 {
			inbound, err = s.GetInbound(ids[0])
		}
	}
	return traffic, inbound, err
}

func (s *InboundService) GetClientInboundByEmail(email string) (traffic *xray.ClientTraffic, inbound *model.Inbound, err error) {
	db := database.GetDB()
	var traffics []*xray.ClientTraffic
	err = db.Model(xray.ClientTraffic{}).Where("email = ?", email).Find(&traffics).Error
	if err != nil {
		logger.Warningf("Error retrieving ClientTraffic with email %s: %v", email, err)
		return nil, nil, err
	}
	if len(traffics) == 0 {
		return nil, nil, nil
	}
	traffic = traffics[0]

	inbound, err = s.GetInbound(traffic.InboundId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// client_traffics.inbound_id is a legacy single-inbound pointer that goes
		// stale when an inbound is deleted and recreated: the email-keyed traffic
		// row survives but still references the missing inbound. Fall back to the
		// authoritative client_inbounds link so email lookups (reset, info, …) work.
		ids, idErr := s.clientService.GetInboundIdsForEmail(db, email)
		if idErr != nil {
			return traffic, nil, idErr
		}
		if len(ids) > 0 {
			inbound, err = s.GetInbound(ids[0])
		}
	}
	if err == nil && inbound != nil && !s.inboundHasClientEmail(inbound, email) {
		// The pointed-at inbound still exists but no longer carries the client —
		// the client was moved to another inbound (#6059). Resolve through the
		// client_inbounds link to the inbound that actually hosts it now.
		ids, idErr := s.clientService.GetInboundIdsForEmail(db, email)
		if idErr == nil {
			for _, id := range ids {
				if id == inbound.Id {
					continue
				}
				if other, oErr := s.GetInbound(id); oErr == nil && s.inboundHasClientEmail(other, email) {
					inbound = other
					break
				}
			}
		}
	}
	return traffic, inbound, err
}

func (s *InboundService) inboundHasClientEmail(inbound *model.Inbound, email string) bool {
	clients, err := s.GetClients(inbound)
	if err != nil {
		return false
	}
	for _, client := range clients {
		if client.Email == email {
			return true
		}
	}
	return false
}

func (s *InboundService) GetClientByEmail(clientEmail string) (*xray.ClientTraffic, *model.Client, error) {
	traffic, inbound, err := s.GetClientInboundByEmail(clientEmail)
	if err != nil {
		return nil, nil, err
	}
	if inbound == nil {
		return nil, nil, common.NewError("Inbound Not Found For Email:", clientEmail)
	}

	clients, err := s.GetClients(inbound)
	if err != nil {
		return nil, nil, err
	}

	for _, client := range clients {
		if client.Email == clientEmail {
			return traffic, &client, nil
		}
	}

	return nil, nil, common.NewError("Client Not Found In Inbound For Email:", clientEmail)
}

// EmailsByInbound returns the list of client emails currently configured on
// an inbound's settings.clients[]. Used by the "delete all clients" flow on
// the inbounds page, which then feeds the list into ClientService.BulkDelete.
func (s *InboundService) EmailsByInbound(inboundId int) ([]string, error) {
	inbound, err := s.GetInbound(inboundId)
	if err != nil {
		return nil, err
	}
	clients, err := s.GetClients(inbound)
	if err != nil {
		return nil, err
	}
	emails := make([]string, 0, len(clients))
	for _, c := range clients {
		if e := strings.TrimSpace(c.Email); e != "" {
			emails = append(emails, e)
		}
	}
	return emails, nil
}
