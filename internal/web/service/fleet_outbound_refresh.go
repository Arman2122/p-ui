package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetProviderRefreshRequest struct {
	OperationID      string `json:"operationId"`
	ProviderID       string `json:"providerId"`
	ExpectedRevision uint64 `json:"expectedRevision"`
}

type FleetProviderRefreshResult struct {
	ProviderID            string `json:"providerId"`
	ConfigurationRevision uint64 `json:"configurationRevision"`
	CachedRevision        uint64 `json:"cachedRevision"`
	SHA256                string `json:"sha256"`
	Entries               int    `json:"entries"`
	Changed               bool   `json:"changed"`
	Replayed              bool   `json:"-"`
}

type fleetProviderClaim struct {
	Source    model.FleetOutboundProvider
	Operation string
	Digest    []byte
}

func (s *FleetProviderService) claim(ctx context.Context, actor *model.User, request FleetProviderRefreshRequest, due bool) (fleetProviderClaim, FleetProviderRefreshResult, error) {
	var claim fleetProviderClaim
	var result FleetProviderRefreshResult
	if !fleetID(request.OperationID) || !due && (!fleetID(request.ProviderID) || request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64) {
		return claim, result, ErrFleetProvider
	}
	_, digest, err := canonicalFleetArtifact(struct {
		Kind    string
		Request FleetProviderRefreshRequest
	}{"provider-refresh/v1", request}, 2048)
	if err != nil {
		return claim, result, ErrFleetProvider
	}
	err = s.transaction(ctx, func(tx *gorm.DB) error {
		if !due {
			if err := requireFleetAdmin(tx, actor); err != nil {
				return err
			}
			replayed, err := providerOperation(tx, request.OperationID, digest, &result)
			if err != nil || replayed {
				result.Replayed = replayed
				return err
			}
		}
		now := s.now().UnixMilli()
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"})
		if due {
			query = tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("refresh_enabled AND ready_unix_milli <= ? AND lease_until_unix_milli <= ?", now, now).Order("ready_unix_milli, id")
		} else {
			query = query.Where("id = ?", request.ProviderID)
		}
		if err := query.First(&claim.Source).Error; err != nil {
			return err
		}
		if !due {
			replayed, err := providerOperation(tx, request.OperationID, digest, &result)
			if err != nil || replayed {
				result.Replayed = replayed
				return err
			}
		}
		if !due && uint64(claim.Source.Revision) != request.ExpectedRevision {
			return ErrFleetProviderRevision
		}
		if claim.Source.LeaseID != nil && claim.Source.LeaseUntilUnixMilli > now {
			return ErrFleetProviderBusy
		}
		claim.Operation, claim.Digest = request.OperationID, digest
		claim.Source.LeaseID, claim.Source.LeaseUntilUnixMilli = &claim.Operation, now+90_000
		return tx.Model(&model.FleetOutboundProvider{}).Where("id = ?", claim.Source.ID).Updates(map[string]any{
			"lease_id": claim.Operation, "lease_until_unix_milli": claim.Source.LeaseUntilUnixMilli, "ready_unix_milli": claim.Source.LeaseUntilUnixMilli, "last_attempt_unix_milli": now,
		}).Error
	})
	return claim, result, err
}

func (s *FleetProviderService) Refresh(ctx context.Context, actor *model.User, request FleetProviderRefreshRequest) (FleetProviderRefreshResult, error) {
	claim, result, err := s.claim(ctx, actor, request, false)
	if err != nil || result.Replayed {
		return result, err
	}
	return s.refreshClaim(ctx, claim)
}

func (s *FleetProviderService) RefreshDue(ctx context.Context, maximum int) (int, error) {
	if maximum < 1 || maximum > 8 {
		return 0, ErrFleetProvider
	}
	for count := range maximum {
		claim, _, err := s.claim(ctx, nil, FleetProviderRefreshRequest{OperationID: uuid.NewString()}, true)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		if _, err := s.refreshClaim(ctx, claim); err != nil && !errors.Is(err, ErrFleetProviderFetch) && !errors.Is(err, ErrFleetProviderRevision) {
			return count + 1, err
		}
	}
	return maximum, nil
}

func (s *FleetProviderService) refreshClaim(parent context.Context, claim fleetProviderClaim) (FleetProviderRefreshResult, error) {
	var result FleetProviderRefreshResult
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	stop := context.AfterFunc(s.owner, cancel)
	defer cancel()
	defer stop()
	if err := s.check(ctx); err != nil {
		return result, err
	}
	var spec FleetProviderFetchSpec
	var previous model.FleetOutboundProviderRevision
	var identities map[string]string
	prepareErr := s.transaction(ctx, func(tx *gorm.DB) error {
		if json.Unmarshal([]byte(claim.Source.Configuration), &spec) != nil || validateFleetProviderFetch(spec) != nil {
			return fleetProviderFailure{"CONFIGURATION_CORRUPT"}
		}
		_, digest, err := canonicalFleetArtifact(spec, 16<<10)
		if err != nil || !bytes.Equal(digest, claim.Source.ConfigurationDigest) {
			return fleetProviderFailure{"CONFIGURATION_CORRUPT"}
		}
		if claim.Source.CachedRevision == 0 {
			return nil
		}
		previous, err = loadProviderCache(tx, claim.Source.ID, claim.Source.CachedRevision)
		if err != nil {
			return err
		}
		if json.Unmarshal([]byte(previous.Identities), &identities) != nil {
			return fleetProviderFailure{"CACHE_CORRUPT"}
		}
		return nil
	})
	if prepareErr != nil {
		return result, s.finishProviderFailure(ctx, claim, prepareErr)
	}
	body, err := fetchFleetProvider(ctx, spec, s.network)
	if err != nil {
		return result, s.finishProviderFailure(ctx, claim, err)
	}
	content, err := parseFleetProvider(claim.Source.ID, spec, body, identities)
	if err != nil {
		return result, s.finishProviderFailure(ctx, claim, err)
	}
	err = s.transaction(ctx, func(tx *gorm.DB) error {
		var source model.FleetOutboundProvider
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, "id = ?", claim.Source.ID).Error; err != nil {
			return err
		}
		if !providerClaimCurrent(source, claim, s.now().UnixMilli()) {
			return ErrFleetProviderRevision
		}
		changed := previous.Revision == 0 || previous.ConfigurationRevision != source.Revision || !bytes.Equal(previous.OutboundsDigest, content.OutboundsDigest) || !bytes.Equal(previous.IdentitiesDigest, content.IdentitiesDigest)
		cached := previous
		if changed {
			if source.CachedRevision == math.MaxInt64 {
				return ErrFleetProviderRevision
			}
			cached = model.FleetOutboundProviderRevision{
				ProviderID: source.ID, Revision: source.CachedRevision + 1, ConfigurationRevision: source.Revision, ConfigurationDigest: bytes.Clone(source.ConfigurationDigest),
				Outbounds: string(content.Outbounds), Identities: string(content.Identities), OutboundsDigest: content.OutboundsDigest, IdentitiesDigest: content.IdentitiesDigest, CreatedUnixMilli: s.now().UnixMilli(),
			}
			cached.Digest, err = providerCacheDigest(cached)
			if err != nil {
				return err
			}
			if err := tx.Create(&cached).Error; err != nil {
				return err
			}
		}
		result = FleetProviderRefreshResult{ProviderID: source.ID, ConfigurationRevision: uint64(source.Revision), CachedRevision: uint64(cached.Revision), SHA256: hex.EncodeToString(cached.Digest), Entries: content.Count, Changed: changed}
		updates := map[string]any{
			"cached_revision": cached.Revision, "lease_id": nil, "lease_until_unix_milli": 0,
			"ready_unix_milli": s.now().Add(time.Duration(spec.IntervalSeconds) * time.Second).UnixMilli(), "last_success_unix_milli": s.now().UnixMilli(), "diagnostic": "",
		}
		if changed {
			updates["fanout_revision"], updates["fanout_pending"], updates["fanout_after_node"] = cached.Revision, true, nil
			updates["fanout_ready_unix_milli"] = 0
		}
		if err := tx.Model(&source).Updates(updates).Error; err != nil {
			return err
		}
		return saveProviderOperation(tx, claim.Operation, claim.Digest, result)
	})
	return result, err
}

func providerClaimCurrent(source model.FleetOutboundProvider, claim fleetProviderClaim, now int64) bool {
	return source.Revision == claim.Source.Revision && source.CachedRevision == claim.Source.CachedRevision && source.LeaseID != nil && *source.LeaseID == claim.Operation &&
		source.LeaseUntilUnixMilli == claim.Source.LeaseUntilUnixMilli && source.LeaseUntilUnixMilli > now && bytes.Equal(source.ConfigurationDigest, claim.Source.ConfigurationDigest)
}

func (s *FleetProviderService) finishProviderFailure(ctx context.Context, claim fleetProviderClaim, cause error) error {
	code := "REFRESH_FAILED"
	var failure fleetProviderFailure
	if errors.As(cause, &failure) {
		code = failure.Code
	}
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&model.FleetOutboundProvider{}).Where("id = ? AND revision = ? AND cached_revision = ? AND lease_id = ? AND lease_until_unix_milli = ?",
			claim.Source.ID, claim.Source.Revision, claim.Source.CachedRevision, claim.Operation, claim.Source.LeaseUntilUnixMilli).Updates(map[string]any{
			"lease_id": nil, "lease_until_unix_milli": 0, "ready_unix_milli": s.now().Add(10 * time.Second).UnixMilli(), "diagnostic": code,
		}).Error
	})
	return errors.Join(cause, err)
}
