package service

import (
	"strings"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

type BulkRotateResult struct {
	Rotated int         `json:"rotated"`
	Outcome BulkOutcome `json:"outcome"`
}

func declaredCredentialNames(protocol model.Protocol) map[string]bool {
	kind := core.Kind(protocol)
	declared := map[string]bool{}
	if authority, ok := cores.ClientCredentialAuthority(kind); ok {
		for _, name := range authority.ClientCredentials(kind) {
			declared[name] = true
		}
	}
	return declared
}

// applyClientCredentials writes the minted values an inbound's kind declares
// into one settings-JSON client; the vocabulary's "uuid" is the blob's "id".
func applyClientCredentials(c map[string]any, creds map[string]string, declared map[string]bool) bool {
	changed := false
	for name, value := range creds {
		if !declared[name] {
			continue
		}
		key := name
		if name == core.CredUUID {
			key = "id"
		}
		if cur, _ := c[key].(string); cur != value {
			c[key] = value
			changed = true
		}
	}
	return changed
}

// BulkRotate mints fresh credentials and/or subscription ids for many clients,
// so every link they were handed stops working; delivery of the new ones is the operator's.
func (s *ClientService) BulkRotate(inboundSvc *InboundService, emails []string, credentials, subID bool) (BulkRotateResult, bool, error) {
	result := BulkRotateResult{}
	if !credentials && !subID {
		return result, false, common.NewError("nothing to rotate")
	}
	cleanEmails := dedupeEmails(emails)
	if len(cleanEmails) == 0 {
		result.Outcome.finalize()
		return result, false, nil
	}
	db := database.GetDB()
	var records []model.ClientRecord
	for _, batch := range chunkStrings(cleanEmails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := db.Where("email IN ?", batch).Find(&rows).Error; err != nil {
			return result, false, err
		}
		records = append(records, rows...)
	}
	recordsByEmail := make(map[string]*model.ClientRecord, len(records))
	recordIds := make([]int, 0, len(records))
	emailById := make(map[int]string, len(records))
	for i := range records {
		recordsByEmail[records[i].Email] = &records[i]
		recordIds = append(recordIds, records[i].Id)
		emailById[records[i].Id] = records[i].Email
	}
	inboundsByEmail := map[string][]int{}
	for _, batch := range chunkInts(recordIds, sqlInChunk) {
		var links []model.ClientInbound
		if err := db.Where("client_id IN ?", batch).Order("inbound_id ASC").Find(&links).Error; err != nil {
			return result, false, err
		}
		for _, l := range links {
			email := emailById[l.ClientId]
			inboundsByEmail[email] = append(inboundsByEmail[email], l.InboundId)
		}
	}

	inboundCache := map[int]*model.Inbound{}
	loadInbound := func(id int) (*model.Inbound, error) {
		if ib, ok := inboundCache[id]; ok {
			return ib, nil
		}
		ib, err := inboundSvc.GetInbound(id)
		if err != nil {
			return nil, err
		}
		inboundCache[id] = ib
		return ib, nil
	}

	skippedReasons := map[string]string{}
	plan := map[string]*bulkAdjustEntry{}
	for _, email := range cleanEmails {
		rec, found := recordsByEmail[email]
		if !found {
			skippedReasons[email] = "client not found"
			continue
		}
		entry := &bulkAdjustEntry{record: rec}
		if subID {
			entry.subID = uuid.NewString()
		}
		if credentials {
			creds := map[string]string{}
			seenKinds := map[core.Kind]bool{}
			var unmintable []string
			for _, ibId := range inboundsByEmail[email] {
				ib, err := loadInbound(ibId)
				if err != nil {
					skippedReasons[email] = err.Error()
					continue
				}
				kind := core.Kind(ib.Protocol)
				if seenKinds[kind] {
					continue
				}
				seenKinds[kind] = true
				authority, ok := cores.ClientCredentialAuthority(kind)
				if !ok {
					unmintable = append(unmintable, string(kind))
					continue
				}
				minted, err := authority.MintClientCredentials(kind, ib.Settings, map[string]string{})
				if err != nil {
					skippedReasons[email] = err.Error()
					continue
				}
				if len(minted) == 0 {
					unmintable = append(unmintable, string(kind))
					continue
				}
				for name, value := range minted {
					creds[name] = value
				}
			}
			switch {
			case len(creds) > 0:
				entry.creds = creds
			case len(unmintable) > 0:
				skippedReasons[email] = "no credential to mint for " + strings.Join(unmintable, ", ")
			}
		}
		if entry.creds != nil || entry.subID != "" {
			plan[email] = entry
		}
	}

	ok := map[string]struct{}{}
	needRestart := false
	if len(plan) > 0 {
		exec, err := s.bulkExecutePlan(inboundSvc, plan, "", skippedReasons)
		if err != nil {
			return result, exec.needRestart, err
		}
		needRestart = exec.needRestart
		for email := range exec.adjusted {
			ok[email] = struct{}{}
		}
		for email := range plan {
			if _, applied := exec.adjusted[email]; applied {
				continue
			}
			if _, already := skippedReasons[email]; !already {
				skippedReasons[email] = "not attached to any inbound"
			}
		}
	}
	for email := range ok {
		result.Outcome.ok(email)
	}
	for email, reason := range skippedReasons {
		result.Outcome.skip(email, reason)
	}
	result.Outcome.finalize()
	result.Rotated = len(ok)
	return result, needRestart, nil
}
