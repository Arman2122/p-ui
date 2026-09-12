package service

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func validateCreatePayloadBindings(payloads []ClientCreatePayload) (map[string]error, error) {
	emails := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		emails = append(emails, strings.TrimSpace(payload.Client.Email))
	}
	existing := map[string]model.ClientRecord{}
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var records []model.ClientRecord
		if err := database.GetDB().Where("email IN ?", batch).Find(&records).Error; err != nil {
			return nil, err
		}
		for _, rec := range records {
			existing[rec.Email] = rec
		}
	}
	type targets struct {
		ids     []int
		records []*model.ClientRecord
	}
	groups := map[string]*targets{}
	failures := map[string]error{}
	for _, payload := range payloads {
		rec, found := existing[strings.TrimSpace(payload.Client.Email)]
		if !found {
			continue
		}
		if rec.OwnerUserID != nil && payload.Client.Group != "" {
			failures[rec.Email] = ErrResellerForbidden
			continue
		}
		rec.Enable = payload.Client.Enable
		ids := slices.Clone(payload.InboundIds)
		slices.Sort(ids)
		ids = slices.Compact(ids)
		key := fmt.Sprint(ids)
		if groups[key] == nil {
			groups[key] = &targets{ids: ids}
		}
		groups[key].records = append(groups[key].records, &rec)
	}
	for _, group := range groups {
		invalid, err := validateAttachmentBatch(group.records, group.ids)
		if err != nil {
			return nil, err
		}
		for email, err := range invalid {
			if err != nil {
				failures[email] = err
			}
		}
	}
	return failures, nil
}

func validateAttachmentBatch(records []*model.ClientRecord, add []int) (map[string]error, error) {
	failures := map[string]error{}
	ids, emails, owners := []int{}, []string{}, []int{}
	for _, rec := range records {
		ids = append(ids, rec.Id)
		emails = append(emails, rec.Email)
		if rec.OwnerUserID != nil {
			owners = append(owners, *rec.OwnerUserID)
		}
	}
	db := database.GetDB()
	current := map[int][]int{}
	for _, batch := range chunkInts(ids, sqlInChunk) {
		var links []model.ClientInbound
		if err := db.Where("client_id IN ?", batch).Find(&links).Error; err != nil {
			return nil, err
		}
		for _, link := range links {
			current[link.ClientId] = append(current[link.ClientId], link.InboundId)
		}
	}
	granted := map[int]map[int]bool{}
	accounts := map[int]model.ResellerAccount{}
	slices.Sort(owners)
	owners = slices.Compact(owners)
	for _, batch := range chunkInts(owners, sqlInChunk) {
		var grants []model.ResellerInboundGrant
		if err := db.Where("user_id IN ?", batch).Find(&grants).Error; err != nil {
			return nil, err
		}
		for _, grant := range grants {
			if granted[grant.UserID] == nil {
				granted[grant.UserID] = map[int]bool{}
			}
			granted[grant.UserID][grant.InboundID] = true
		}
		var rows []model.ResellerAccount
		if err := db.Where("user_id IN ?", batch).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			accounts[row.UserID] = row
		}
	}
	assignments := map[string]int{}
	policyIDs := []int{}
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientPolicy
		if err := db.Where("email IN ? AND policy_id IS NOT NULL AND NOT override_quota", batch).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			assignments[row.Email] = *row.PolicyId
			policyIDs = append(policyIDs, *row.PolicyId)
		}
	}
	policies := map[int]model.Policy{}
	slices.Sort(policyIDs)
	policyIDs = slices.Compact(policyIDs)
	for _, batch := range chunkInts(policyIDs, sqlInChunk) {
		var rows []model.Policy
		if err := db.Where("id IN ?", batch).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			policies[row.Id] = row
		}
	}
	checked := map[string]error{}
	for _, rec := range records {
		desired := append(slices.Clone(current[rec.Id]), add...)
		slices.Sort(desired)
		desired = slices.Compact(desired)
		if rec.OwnerUserID != nil {
			owner := *rec.OwnerUserID
			account, exists := accounts[owner]
			if !exists || rec.Group != "" {
				failures[rec.Email] = ErrResellerForbidden
				continue
			}
			if rec.Enable && (account.Suspended || account.BudgetBlocked || account.ExpiresAt > 0 && account.ExpiresAt <= time.Now().UnixMilli() || account.TrafficLimit > 0 && account.TrafficUsed >= account.TrafficLimit) {
				failures[rec.Email] = ErrResellerBudget
				continue
			}
			for _, id := range desired {
				if !granted[owner][id] {
					failures[rec.Email] = ErrResellerForbidden
					break
				}
			}
			if failures[rec.Email] != nil {
				continue
			}
		}
		policy, exists := policies[assignments[rec.Email]]
		if !exists || policy.OnDepletion == "disable" {
			continue
		}
		key := fmt.Sprintf("%d:%v", policy.Id, desired)
		if err, exists := checked[key]; exists {
			failures[rec.Email] = err
			continue
		}
		err := validatePlanInbounds(db, PlanScope{OwnerUserID: policy.OwnerUserID}, &policy, desired)
		checked[key], failures[rec.Email] = err, err
	}
	return failures, nil
}
