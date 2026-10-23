package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

var ErrFleetProviderBusy = errors.New("fleet outbound provider refresh is already claimed")

type FleetProviderService struct {
	db      *gorm.DB
	pool    *sql.DB
	owner   context.Context
	network fleetProviderNetwork
	now     func() time.Time
}

func NewFleetProviderService(owner context.Context, db *gorm.DB) (*FleetProviderService, error) {
	if owner == nil || owner.Err() != nil || db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.Error != nil || db.Name() != "postgres" || database.GetDB() != db {
		return nil, ErrFleetRuntimeDatabase
	}
	if _, transactional := db.Statement.ConnPool.(gorm.TxCommitter); transactional {
		return nil, ErrFleetRuntimeDatabase
	}
	pool, err := db.DB()
	if err != nil || pool == nil {
		return nil, ErrFleetRuntimeDatabase
	}
	ctx, cancel := context.WithTimeout(owner, 5*time.Second)
	defer cancel()
	if pool.PingContext(ctx) != nil || database.GetDB() != db {
		return nil, ErrFleetRuntimeDatabase
	}
	return &FleetProviderService{db: db, pool: pool, owner: owner, now: time.Now}, nil
}

func (s *FleetProviderService) check(ctx context.Context) error {
	if s == nil || s.db == nil || s.owner == nil || database.GetDB() != s.db {
		return ErrFleetRuntimeDatabase
	}
	if err := s.owner.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pool, err := s.db.DB()
	if err != nil || pool != s.pool {
		return ErrFleetRuntimeDatabase
	}
	return nil
}

func (s *FleetProviderService) transaction(ctx context.Context, action func(*gorm.DB) error) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.owner, cancel)
	defer cancel()
	defer stop()
	return s.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		if err := action(tx); err != nil {
			return err
		}
		return s.check(ctx)
	})
}

type FleetProviderRequest struct {
	OperationID      string                 `json:"operationId"`
	ProviderID       string                 `json:"providerId"`
	ExpectedRevision uint64                 `json:"expectedRevision"`
	Name             string                 `json:"name"`
	CoreID           string                 `json:"coreId"`
	Scope            string                 `json:"scope"`
	NodeID           string                 `json:"nodeId,omitempty"`
	InstallationID   string                 `json:"installationId,omitempty"`
	RefreshEnabled   bool                   `json:"refreshEnabled"`
	Fetch            FleetProviderFetchSpec `json:"fetch"`
}

type FleetProviderView struct {
	ProviderID            string `json:"providerId"`
	ConfigurationRevision uint64 `json:"configurationRevision"`
	CachedRevision        uint64 `json:"cachedRevision"`
	Name                  string `json:"name"`
	CoreID                string `json:"coreId"`
	Scope                 string `json:"scope"`
	NodeID                string `json:"nodeId,omitempty"`
	InstallationID        string `json:"installationId,omitempty"`
	RefreshEnabled        bool   `json:"refreshEnabled"`
	Diagnostic            string `json:"diagnostic,omitempty"`
	Replayed              bool   `json:"-"`
}

func providerView(row model.FleetOutboundProvider) FleetProviderView {
	view := FleetProviderView{
		ProviderID: row.ID, ConfigurationRevision: uint64(row.Revision), CachedRevision: uint64(row.CachedRevision), Name: row.Name,
		CoreID: row.CoreID, Scope: row.Scope, RefreshEnabled: row.RefreshEnabled, Diagnostic: row.Diagnostic,
	}
	if row.NodeID != nil && row.InstallationID != nil {
		view.NodeID, view.InstallationID = *row.NodeID, *row.InstallationID
	}
	return view
}

func providerOperation(tx *gorm.DB, id string, digest []byte, result any) (bool, error) {
	var row model.FleetOperation
	query := tx.Find(&row, "id = ?", id)
	if query.Error != nil || query.RowsAffected == 0 {
		return false, query.Error
	}
	if !bytes.Equal(row.Digest, digest) {
		return false, controlplane.ErrConflict
	}
	if json.Unmarshal(row.Result, result) != nil {
		return false, ErrFleetProvider
	}
	return true, nil
}

func saveProviderOperation(tx *gorm.DB, id string, digest []byte, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Create(&model.FleetOperation{ID: id, Digest: digest, Result: encoded}).Error
}

func (s *FleetProviderService) Configure(ctx context.Context, actor *model.User, request FleetProviderRequest) (FleetProviderView, error) {
	var view FleetProviderView
	if !fleetID(request.OperationID) || !fleetID(request.ProviderID) || request.ExpectedRevision >= math.MaxInt64 || !fleetNetworkName.MatchString(request.Name) || request.CoreID != "xray" ||
		(request.Scope != "shared" || request.NodeID != "" || request.InstallationID != "") && (request.Scope != "node" || !fleetID(request.NodeID) || !fleetID(request.InstallationID)) {
		return view, ErrFleetProvider
	}
	if err := validateFleetProviderFetch(request.Fetch); err != nil {
		return view, err
	}
	encoded, configDigest, err := canonicalFleetArtifact(request.Fetch, 16<<10)
	if err != nil {
		return view, ErrFleetProvider
	}
	_, digest, err := canonicalFleetArtifact(struct {
		Kind    string
		Request FleetProviderRequest
	}{"provider-config/v1", request}, 32<<10)
	if err != nil {
		return view, ErrFleetProvider
	}
	err = s.transaction(ctx, func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.fleet-provider:' || ?,0))", request.ProviderID).Error; err != nil {
			return err
		}
		replayed, err := providerOperation(tx, request.OperationID, digest, &view)
		if err != nil || replayed {
			view.Replayed = replayed
			return err
		}
		var row model.FleetOutboundProvider
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&row, "id = ?", request.ProviderID).Error; err != nil {
			return err
		}
		if uint64(row.Revision) != request.ExpectedRevision {
			return ErrFleetProviderRevision
		}
		if row.Revision != 0 {
			old := providerView(row)
			if old.CoreID != request.CoreID || old.Scope != request.Scope || old.NodeID != request.NodeID || old.InstallationID != request.InstallationID {
				return ErrFleetProvider
			}
		} else {
			row.ID, row.CoreID, row.Scope, row.CreatedUnixMilli = request.ProviderID, request.CoreID, request.Scope, s.now().UnixMilli()
			if request.Scope == "node" {
				row.NodeID, row.InstallationID = &request.NodeID, &request.InstallationID
			}
		}
		row.Revision++
		row.Name, row.Configuration, row.ConfigurationDigest, row.RefreshEnabled = request.Name, string(encoded), configDigest, request.RefreshEnabled
		row.LeaseID, row.LeaseUntilUnixMilli, row.ReadyUnixMilli, row.Diagnostic, row.ActorID = nil, 0, 0, "", actor.Id
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		view = providerView(row)
		return saveProviderOperation(tx, request.OperationID, digest, view)
	})
	return view, err
}

func providerCacheDigest(row model.FleetOutboundProviderRevision) ([]byte, error) {
	_, digest, err := canonicalFleetArtifact(struct {
		Provider                             string
		Revision, ConfigurationRevision      int64
		Configuration, Outbounds, Identities string
	}{row.ProviderID, row.Revision, row.ConfigurationRevision, hex.EncodeToString(row.ConfigurationDigest), hex.EncodeToString(row.OutboundsDigest), hex.EncodeToString(row.IdentitiesDigest)}, 2048)
	return digest, err
}

func loadProviderCache(tx *gorm.DB, providerID string, revision int64) (model.FleetOutboundProviderRevision, error) {
	var row model.FleetOutboundProviderRevision
	if err := tx.First(&row, "provider_id = ? AND revision = ?", providerID, revision).Error; err != nil {
		return row, err
	}
	_, outboundDigest, err := canonicalFleetArtifact(json.RawMessage(row.Outbounds), maxFleetProviderBytes)
	if err != nil || !bytes.Equal(outboundDigest, row.OutboundsDigest) {
		return row, fleetProviderFailure{"CACHE_CORRUPT"}
	}
	_, identityDigest, err := canonicalFleetArtifact(json.RawMessage(row.Identities), maxFleetProviderBytes)
	if err != nil || !bytes.Equal(identityDigest, row.IdentitiesDigest) {
		return row, fleetProviderFailure{"CACHE_CORRUPT"}
	}
	digest, err := providerCacheDigest(row)
	if err != nil || !bytes.Equal(digest, row.Digest) {
		return row, fleetProviderFailure{"CACHE_CORRUPT"}
	}
	return row, nil
}
