package service

import (
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

// BulkSetPatch is one bulk "set": an absent field is left alone, a present
// zero value clears it (0 = unlimited quota or expiry, "" = no flow or group).
type BulkSetPatch struct {
	LimitIP    *int    `json:"limitIp"`
	Comment    *string `json:"comment"`
	TgID       *int64  `json:"tgId"`
	Reset      *int    `json:"reset"`
	TotalGB    *int64  `json:"totalGB"`
	ExpiryTime *int64  `json:"expiryTime"`
	Flow       *string `json:"flow"`
	Group      *string `json:"group"`
	PolicyID   *int    `json:"policyId"`
}

func (p BulkSetPatch) touchesClientEntry() bool {
	return p.LimitIP != nil || p.Comment != nil || p.TgID != nil || p.Reset != nil
}

func (p BulkSetPatch) touchesInbound() bool {
	return p.touchesClientEntry() || p.TotalGB != nil || p.ExpiryTime != nil || p.Flow != nil
}

func (p BulkSetPatch) empty() bool {
	return !p.touchesInbound() && p.Group == nil && p.PolicyID == nil
}

func (p BulkSetPatch) validate() error {
	switch {
	case p.LimitIP != nil && *p.LimitIP < 0:
		return common.NewError("limitIp must not be negative")
	case p.Reset != nil && *p.Reset < 0:
		return common.NewError("reset must not be negative")
	case p.TgID != nil && *p.TgID < 0:
		return common.NewError("tgId must not be negative")
	case p.TotalGB != nil && *p.TotalGB < 0:
		return common.NewError("totalGB must not be negative")
	case p.ExpiryTime != nil && *p.ExpiryTime < 0:
		return common.NewError("expiryTime must be a timestamp, or 0 for unlimited")
	case p.PolicyID != nil && *p.PolicyID < 0:
		return common.NewError("policyId must not be negative")
	}
	return nil
}

func patchClientEntry(c map[string]any, p *BulkSetPatch) {
	if p == nil {
		return
	}
	if p.LimitIP != nil {
		c["limitIp"] = *p.LimitIP
	}
	if p.Comment != nil {
		c["comment"] = *p.Comment
	}
	if p.TgID != nil {
		c["tgId"] = *p.TgID
	}
	if p.Reset != nil {
		c["reset"] = *p.Reset
	}
}

func applyBulkSetPatch(c *model.Client, p *BulkSetPatch) {
	if p == nil {
		return
	}
	if p.LimitIP != nil {
		c.LimitIP = *p.LimitIP
	}
	if p.Comment != nil {
		c.Comment = *p.Comment
	}
	if p.TgID != nil {
		c.TgID = *p.TgID
	}
	if p.Reset != nil {
		c.Reset = *p.Reset
	}
}

type BulkSetResult struct {
	Updated int         `json:"updated"`
	Outcome BulkOutcome `json:"outcome"`
}

func dedupeEmails(emails []string) []string {
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(emails))
	for _, e := range emails {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		clean = append(clean, e)
	}
	return clean
}

// BulkSet writes exact values onto many clients: inbound-held fields ride the
// BulkAdjust plan (one write per inbound); group and speed plan apply per client.
func (s *ClientService) BulkSet(inboundSvc *InboundService, emails []string, patch BulkSetPatch) (BulkSetResult, bool, error) {
	result := BulkSetResult{}
	if patch.empty() {
		return result, false, common.NewError("nothing to set")
	}
	if err := patch.validate(); err != nil {
		return result, false, err
	}
	flow := ""
	if patch.Flow != nil {
		flow = strings.TrimSpace(*patch.Flow)
		if flow == "" {
			flow = bulkFlowClear
		}
		if _, ok := bulkFlowAllowed[flow]; !ok {
			return result, false, common.NewError("unknown flow:", *patch.Flow)
		}
	}
	policySvc := &PolicyService{}
	if patch.PolicyID != nil && *patch.PolicyID != 0 {
		if _, err := policySvc.Get(*patch.PolicyID); err != nil {
			return result, false, err
		}
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
	for i := range records {
		recordsByEmail[records[i].Email] = &records[i]
	}

	skippedReasons := map[string]string{}
	targets := make([]string, 0, len(cleanEmails))
	plan := map[string]*bulkAdjustEntry{}
	for _, email := range cleanEmails {
		rec, found := recordsByEmail[email]
		if !found {
			skippedReasons[email] = "client not found"
			continue
		}
		targets = append(targets, email)
		entry := &bulkAdjustEntry{record: rec}
		if patch.ExpiryTime != nil {
			if rec.ExpiryTime < 0 {
				skippedReasons[email] = "delayed start"
			} else {
				entry.applyExpiry = true
				entry.newExpiry = *patch.ExpiryTime
			}
		}
		if patch.TotalGB != nil {
			entry.applyTotal = true
			entry.newTotal = *patch.TotalGB
		}
		if patch.touchesClientEntry() {
			entry.patch = &patch
		}
		if entry.applyExpiry || entry.applyTotal || entry.patch != nil || flow != "" {
			plan[email] = entry
		}
	}

	ok := map[string]struct{}{}
	needRestart := false
	if len(plan) > 0 {
		exec, err := s.bulkExecutePlan(inboundSvc, plan, flow, skippedReasons)
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
			if _, already := skippedReasons[email]; already {
				continue
			}
			if exec.flowIneligible[email] {
				skippedReasons[email] = "flow not supported on inbound"
			} else {
				skippedReasons[email] = "not attached to any inbound"
			}
		}
	}

	if patch.Group != nil {
		if _, err := s.AddToGroup(targets, *patch.Group); err != nil {
			return result, needRestart, err
		}
		for _, email := range targets {
			ok[email] = struct{}{}
		}
	}
	if patch.PolicyID != nil {
		for _, email := range targets {
			if err := policySvc.Assign(email, *patch.PolicyID); err != nil {
				delete(ok, email)
				result.Outcome.fail(email, "speed plan: "+err.Error())
				continue
			}
			ok[email] = struct{}{}
		}
	}

	for email := range ok {
		result.Outcome.ok(email)
	}
	for email, reason := range skippedReasons {
		result.Outcome.skip(email, reason)
	}
	result.Outcome.finalize()
	result.Updated = len(ok)
	return result, needRestart, nil
}

type BulkMoveResult struct {
	Moved   int         `json:"moved"`
	Outcome BulkOutcome `json:"outcome"`
}

// BulkMove attaches every client to the target inbounds before detaching it
// from the sources, so no client is ever left without an inbound mid-way.
func (s *ClientService) BulkMove(inboundSvc *InboundService, emails []string, fromIds, toIds []int) (BulkMoveResult, bool, error) {
	result := BulkMoveResult{}
	if len(fromIds) == 0 || len(toIds) == 0 {
		return result, false, common.NewError("source and target inbounds are required")
	}
	sources := map[int]struct{}{}
	for _, id := range fromIds {
		sources[id] = struct{}{}
	}
	for _, id := range toIds {
		if _, both := sources[id]; both {
			return result, false, common.NewError(fmt.Sprintf("inbound %d cannot be both source and target", id))
		}
	}

	attach, needRestart, err := s.BulkAttach(inboundSvc, emails, toIds)
	if err != nil {
		return result, needRestart, err
	}
	failed := map[string]bool{}
	for _, row := range attach.Outcome.Failed {
		failed[row.Email] = true
		result.Outcome.fail(row.Email, "attach: "+row.Reason)
	}
	proceed := map[string]struct{}{}
	for _, email := range attach.Outcome.OK {
		if !failed[email] {
			proceed[email] = struct{}{}
		}
	}
	for _, row := range attach.Outcome.Skipped {
		if failed[row.Email] {
			continue
		}
		if strings.HasPrefix(row.Reason, "already attached") {
			proceed[row.Email] = struct{}{}
			continue
		}
		result.Outcome.skip(row.Email, "attach: "+row.Reason)
	}
	if len(proceed) == 0 {
		result.Outcome.finalize()
		return result, needRestart, nil
	}

	detachEmails := make([]string, 0, len(proceed))
	for email := range proceed {
		detachEmails = append(detachEmails, email)
	}
	detach, detachRestart, err := s.BulkDetach(inboundSvc, detachEmails, fromIds)
	if err != nil {
		return result, needRestart || detachRestart, err
	}
	needRestart = needRestart || detachRestart
	moved := map[string]struct{}{}
	for _, row := range detach.Outcome.Failed {
		result.Outcome.fail(row.Email, "detach: "+row.Reason)
	}
	for _, row := range detach.Outcome.Skipped {
		result.Outcome.skip(row.Email, "detach: "+row.Reason)
	}
	for _, email := range detach.Outcome.OK {
		if _, seen := moved[email]; seen {
			continue
		}
		moved[email] = struct{}{}
		result.Outcome.ok(email)
	}
	result.Moved = len(moved)
	result.Outcome.finalize()
	return result, needRestart, nil
}
