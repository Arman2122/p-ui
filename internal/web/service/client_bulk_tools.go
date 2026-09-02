package service

import (
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

type BulkClearIpsResult struct {
	Cleared int         `json:"cleared"`
	Outcome BulkOutcome `json:"outcome"`
}

// BulkClearIps forgets the recorded source addresses of many clients, the
// way the per-client "clear IPs" does, so an IP-limit block lifts on its own.
func (s *ClientService) BulkClearIps(emails []string) (BulkClearIpsResult, error) {
	result := BulkClearIpsResult{}
	cleanEmails := dedupeEmails(emails)
	if len(cleanEmails) == 0 {
		result.Outcome.finalize()
		return result, nil
	}
	db := database.GetDB()
	known := map[string]struct{}{}
	for _, batch := range chunkStrings(cleanEmails, sqlInChunk) {
		var rows []string
		if err := db.Model(&model.ClientRecord{}).Where("email IN ?", batch).Pluck("email", &rows).Error; err != nil {
			return result, err
		}
		for _, e := range rows {
			known[e] = struct{}{}
		}
	}
	targets := make([]string, 0, len(known))
	for _, email := range cleanEmails {
		if _, ok := known[email]; ok {
			targets = append(targets, email)
		} else {
			result.Outcome.skip(email, "client not found")
		}
	}
	for _, batch := range chunkStrings(targets, sqlInChunk) {
		if err := db.Model(model.InboundClientIps{}).Where("client_email IN ?", batch).Update("ips", "").Error; err != nil {
			result.Outcome.failEach(batch, err.Error())
			continue
		}
		for _, email := range batch {
			result.Outcome.ok(email)
		}
	}
	result.Cleared = len(result.Outcome.OK)
	result.Outcome.finalize()
	return result, nil
}

// ExportEmails is ExportAll narrowed to a selection, in the same portable shape.
func (s *ClientService) ExportEmails(emails []string) ([]ClientCreatePayload, error) {
	cleanEmails := dedupeEmails(emails)
	db := database.GetDB()
	var rows []model.ClientRecord
	for _, batch := range chunkStrings(cleanEmails, sqlInChunk) {
		var part []model.ClientRecord
		if err := db.Where("email IN ?", batch).Find(&part).Error; err != nil {
			return nil, err
		}
		rows = append(rows, part...)
	}
	return s.exportRecords(db, rows)
}

type ClientLinksRow struct {
	Email string   `json:"email"`
	Links []string `json:"links"`
	Error string   `json:"error,omitempty"`
}

// GetClientLinksMany renders every share link of many clients, resolving each
// inbound once however many clients it carries.
func (s *InboundService) GetClientLinksMany(host string, emails []string) ([]ClientLinksRow, error) {
	if registeredSubLinkProvider == nil {
		return nil, common.NewError("sub link provider not registered")
	}
	inbounds := map[int]*model.Inbound{}
	out := make([]ClientLinksRow, 0, len(emails))
	for _, email := range dedupeEmails(emails) {
		row := ClientLinksRow{Email: email, Links: []string{}}
		rec, err := s.clientService.GetRecordByEmail(nil, email)
		if err != nil {
			row.Error = err.Error()
			out = append(out, row)
			continue
		}
		inboundIds, err := s.clientService.GetInboundIdsForRecord(rec.Id)
		if err != nil {
			row.Error = err.Error()
			out = append(out, row)
			continue
		}
		for _, ibId := range inboundIds {
			inbound, ok := inbounds[ibId]
			if !ok {
				loaded, getErr := s.GetInbound(ibId)
				if getErr != nil {
					row.Error = getErr.Error()
					break
				}
				inbound = loaded
				inbounds[ibId] = inbound
			}
			row.Links = append(row.Links, registeredSubLinkProvider.LinksForClient(host, inbound, email)...)
		}
		out = append(out, row)
	}
	return out, nil
}
