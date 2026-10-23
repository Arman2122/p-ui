package service

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

// ExportAll returns every client in the same {client, inboundIds} shape that
// /add and /bulkCreate accept, so an exported file round-trips straight back
// through Import. Clients with no inbound attachment are included with an empty
// inboundIds list so an export taken before DeleteOrphans can restore them.
func (s *ClientService) ExportAll() ([]ClientCreatePayload, error) {
	db := database.GetDB()
	var rows []model.ClientRecord
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return s.exportRecords(db, rows)
}

func (s *ClientService) exportRecords(db *gorm.DB, rows []model.ClientRecord) ([]ClientCreatePayload, error) {
	out := make([]ClientCreatePayload, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}

	ids := make([]int, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].Id)
	}

	attachments := make(map[int][]int, len(rows))
	for _, batch := range chunkInts(ids, sqlInChunk) {
		var links []model.ClientInbound
		if err := db.Where("client_id IN ?", batch).Order("inbound_id ASC").Find(&links).Error; err != nil {
			return nil, err
		}
		for _, l := range links {
			attachments[l.ClientId] = append(attachments[l.ClientId], l.InboundId)
		}
	}

	creds, err := readClientCredentials(db, nil, 0)
	if err != nil {
		return nil, err
	}
	credsByClient := credentialsByClient(creds)
	linksByClient, err := exportableExternalLinks(db, ids)
	if err != nil {
		return nil, err
	}

	for i := range rows {
		client := rows[i].ToClient()
		applyCoreCredentials(client, credsByClient[rows[i].Id])
		// The per-inbound flow_override is the reliable flow for multi-inbound
		// clients; the canonical column can be left stale by SyncInbound (#4792).
		if flow, err := s.EffectiveFlow(db, rows[i].Id); err == nil && flow != "" {
			client.Flow = flow
		}
		out = append(out, ClientCreatePayload{
			Client:        *client,
			InboundIds:    attachments[rows[i].Id],
			ExternalLinks: linksByClient[rows[i].Id],
		})
	}
	return out, nil
}

// ImportClients recreates clients from an exported list. Items that carry
// inboundIds go through the normal BulkCreate path (added to every inbound and
// pushed to xray); items with no inboundIds are restored as bare records so an
// orphan-inclusive export round-trips. Existing emails are never overwritten —
// they are reported in Skipped. The boolean reports whether xray needs a restart.
// exportableExternalLinks reads every client's external links in id batches,
// in the order the subscription renders them.
func exportableExternalLinks(db *gorm.DB, ids []int) (map[int][]ExternalLinkInput, error) {
	links := map[int][]ExternalLinkInput{}
	for _, batch := range chunkInts(ids, sqlInChunk) {
		var rows []model.ClientExternalLink
		if err := db.Where("client_id IN ?", batch).Order("client_id ASC, sort_index ASC, id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			links[r.ClientId] = append(links[r.ClientId], ExternalLinkInput{Kind: r.Kind, Value: r.Value, Remark: r.Remark})
		}
	}
	return links, nil
}

func (s *ClientService) ImportClients(inboundSvc *InboundService, items []ClientCreatePayload) (BulkCreateResult, bool, error) {
	result := BulkCreateResult{}
	if len(items) == 0 {
		return result, false, nil
	}

	skip := func(email, reason string) {
		if strings.TrimSpace(email) == "" {
			email = "(missing email)"
		}
		result.Skipped = append(result.Skipped, BulkCreateReport{Email: email, Reason: reason})
	}

	// Links are checked before anything is created, so a bad link skips the
	// row instead of leaving a client without the links it was exported with.
	attached := make([]ClientCreatePayload, 0, len(items))
	orphans := make([]ClientCreatePayload, 0)
	linksByEmail := map[string][]ExternalLinkInput{}
	for i := range items {
		email := strings.TrimSpace(items[i].Client.Email)
		if len(items[i].ExternalLinks) > 0 {
			if _, err := normalizeExternalLinks(items[i].ExternalLinks); err != nil {
				skip(email, "external links: "+err.Error())
				continue
			}
			linksByEmail[email] = items[i].ExternalLinks
		}
		if len(items[i].InboundIds) > 0 {
			attached = append(attached, items[i])
		} else {
			orphans = append(orphans, items[i])
		}
	}
	restoreLinks := func(email string) {
		links, ok := linksByEmail[email]
		if !ok {
			return
		}
		if err := s.SetExternalLinksByEmail(email, links); err != nil {
			skip(email, "created without its external links: "+err.Error())
		}
	}

	needRestart := false
	if len(attached) > 0 {
		sub, nr, err := s.BulkCreate(inboundSvc, attached)
		if err != nil {
			return result, needRestart, err
		}
		needRestart = needRestart || nr
		result.Created += sub.Created
		result.Skipped = append(result.Skipped, sub.Skipped...)
		for _, email := range sub.Outcome.OK {
			restoreLinks(email)
		}
	}

	db := database.GetDB()
	for i := range orphans {
		client := orphans[i].Client
		email := strings.TrimSpace(client.Email)
		if email == "" {
			skip("", "client email is required")
			continue
		}
		if verr := validateClientEmail(email); verr != nil {
			skip(email, verr.Error())
			continue
		}
		if verr := validateClientSubID(client.SubID); verr != nil {
			skip(email, verr.Error())
			continue
		}

		// An existing record (in the DB or just created from the attached set
		// above) always wins — import never clobbers a live client.
		var taken int64
		if err := db.Model(&model.ClientRecord{}).Where("email = ?", email).Count(&taken).Error; err != nil {
			return result, needRestart, err
		}
		if taken > 0 {
			skip(email, "email already in use: "+email)
			continue
		}

		client.Email = email
		if client.SubID == "" {
			client.SubID = uuid.NewString()
		}
		if client.SubID != "" {
			var subTaken int64
			if err := db.Model(&model.ClientRecord{}).
				Where("sub_id = ? AND email <> ?", client.SubID, email).
				Count(&subTaken).Error; err != nil {
				return result, needRestart, err
			}
			if subTaken > 0 {
				skip(email, "subId already in use: "+client.SubID)
				continue
			}
		}
		// Enable is taken as given; see the note in Create. On this path in
		// particular, forcing it meant a client exported while disabled came
		// back enabled. F046/F170.
		now := time.Now().UnixMilli()
		if client.CreatedAt == 0 {
			client.CreatedAt = now
		}
		client.UpdatedAt = now

		if err := db.Transaction(func(tx *gorm.DB) error {
			record := client.ToRecord()
			if err := tx.Create(record).Error; err != nil {
				return err
			}
			return applyStagedOff(tx, &model.ClientRecord{}, "enable", client.Enable, record.Id)
		}); err != nil {
			skip(email, err.Error())
			continue
		}
		result.Created++
		restoreLinks(email)
	}

	return result, needRestart, nil
}

// DeleteOrphans removes every client that is not attached to any inbound,
// together with its traffic rows, IP log, and external links. It mirrors the
// cleanup the single-client Delete performs, batched into one transaction.
// Returns the number of clients deleted.
func (s *ClientService) DeleteOrphans() (int, error) {
	db := database.GetDB()
	sub := database.GetDB().Table("client_inbounds").Select("client_id")
	var rows []model.ClientRecord
	if err := db.Where("id NOT IN (?)", sub).Order("id ASC").Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	ids := make([]int, 0, len(rows))
	emails := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].Id)
		if rows[i].Email != "" {
			emails = append(emails, rows[i].Email)
		}
	}
	tombstoneClientEmails(emails)

	if err := runSerializedTx(func(tx *gorm.DB) error {
		if e := adjustGroupBaselinesForRemovedTraffic(tx, emails); e != nil {
			return e
		}
		for _, batch := range chunkInts(ids, sqlInChunk) {
			if e := tx.Where("client_id IN ?", batch).Delete(&model.ClientInbound{}).Error; e != nil {
				return e
			}
			if e := tx.Where("client_id IN ?", batch).Delete(&model.ClientExternalLink{}).Error; e != nil {
				return e
			}
			// Foreign keys are disabled repo-wide, so nothing else reaps these.
			if e := deleteClientCredentials(tx, batch, nil); e != nil {
				return e
			}
		}
		if len(emails) > 0 {
			for _, batch := range chunkStrings(emails, sqlInChunk) {
				if e := tx.Where("email IN ?", batch).Delete(&xray.ClientTraffic{}).Error; e != nil {
					return e
				}
				if e := tx.Where("client_email IN ?", batch).Delete(&model.InboundClientIps{}).Error; e != nil {
					return e
				}
			}
			if e := clearGlobalTraffic(tx, emails...); e != nil {
				return e
			}
		}
		for _, batch := range chunkInts(ids, sqlInChunk) {
			if e := tx.Where("id IN ?", batch).Delete(&model.ClientRecord{}).Error; e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return len(ids), nil
}
