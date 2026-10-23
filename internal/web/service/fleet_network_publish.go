package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

var ErrFleetNetworkFunding = errors.New("explicit fleet authority funding is required")

// Funding is a master allocator input, never part of the operator networking request.
type FleetNetworkFunding struct {
	PlacementID string
	Bytes       int64
	Rates       controlplane.Rates
}

func (s *FleetNetworkService) Publish(ctx context.Context, actor *model.User, request FleetNetworkPublishRequest) (FleetNetworkPublicationView, error) {
	return s.PublishFunded(ctx, actor, request, nil)
}

func (s *FleetNetworkService) PublishFunded(ctx context.Context, actor *model.User, request FleetNetworkPublishRequest, funding []FleetNetworkFunding) (FleetNetworkPublicationView, error) {
	var view FleetNetworkPublicationView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var err error
		view, err = publishFleetNetworkTx(ctx, tx, request, funding)
		return err
	})
	return view, err
}

func publishFleetNetworkTx(ctx context.Context, tx *gorm.DB, request FleetNetworkPublishRequest, funding []FleetNetworkFunding) (FleetNetworkPublicationView, error) {
	var view FleetNetworkPublicationView
	if !fleetID(request.OperationID) || !fleetID(request.CompilationID) || len(funding) > 128 {
		return view, ErrFleetNetwork
	}
	funding = slices.Clone(funding)
	slices.SortFunc(funding, func(a, b FleetNetworkFunding) int { return strings.Compare(a.PlacementID, b.PlacementID) })
	for i, item := range funding {
		if !fleetID(item.PlacementID) || item.Bytes <= 0 || item.Bytes > 1<<30 || i > 0 && funding[i-1].PlacementID == item.PlacementID {
			return view, ErrFleetNetworkFunding
		}
	}
	encoded, err := json.Marshal(struct {
		Request FleetNetworkPublishRequest
		Funding []FleetNetworkFunding
	}{request, funding})
	if err != nil {
		return view, err
	}
	digest := sha256.Sum256(append([]byte("fleet-network-publication/v1:"), encoded...))
	err = func() error {
		var compilation model.FleetNetworkCompilation
		if err := tx.First(&compilation, "id = ?", request.CompilationID).Error; err != nil {
			return err
		}
		if err := lockFleetNode(tx, compilation.NodeID, compilation.InstallationID); err != nil {
			return err
		}
		op := model.FleetOperation{ID: request.OperationID, Digest: digest[:], Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&op, "id = ?", op.ID).Error; err != nil {
				return err
			}
			if !bytes.Equal(op.Digest, digest[:]) {
				return controlplane.ErrConflict
			}
			if err := json.Unmarshal(op.Result, &view); err != nil {
				return err
			}
			view.Replayed = true
			return nil
		}
		state, err := loadFleetNetworkState(tx, compilation.NodeID, compilation.InstallationID)
		if err != nil {
			return err
		}
		if !compilation.Ready || compilation.CompilerVersion != fleetNetworkCompilerVersion || compilation.NodeRevisionID != state.Head.CurrentRevisionID ||
			compilation.DefaultsRevisionID != state.Head.DesiredDefaultsID || state.Node.AppliedRevision != state.Node.DesiredRevision {
			return ErrFleetNetworkRevision
		}
		if err := retireFleetNativeBindings(tx, state.Node); err != nil {
			return err
		}
		input, current, issues, err := prepareFleetNetworkSources(tx, state)
		if err != nil {
			return err
		}
		assetPins, issuesAssets, err := prepareFleetNetworkAssets(tx, state, &input)
		if err != nil {
			return err
		}
		if len(issues) != 0 || len(issuesAssets) != 0 {
			return ErrFleetNetworkDependency
		}
		current.Assets = assetPins
		var original FleetNetworkManifest
		if json.Unmarshal([]byte(compilation.Manifest), &original) != nil {
			return ErrFleetNetwork
		}
		before, _, err := canonicalFleetArtifact(original, 32<<20)
		if err != nil {
			return err
		}
		after, _, err := canonicalFleetArtifact(current, 32<<20)
		if err != nil || !bytes.Equal(before, after) {
			return ErrFleetNetworkRevision
		}
		var runtimes []nodeconfig.RuntimeDefinition
		if json.Unmarshal([]byte(compilation.RuntimeDefinitions), &runtimes) != nil {
			return ErrFleetNetwork
		}
		_, graphDigest, err := canonicalFleetArtifact(runtimes, 32<<20)
		if err != nil || !bytes.Equal(graphDigest, compilation.Digest) {
			return ErrFleetNetworkRevision
		}
		currentCompiled, err := nodeconfig.Compile(input)
		if err != nil || len(fleetNetworkDependencies(currentCompiled)) != 0 {
			return ErrFleetNetworkDependency
		}
		_, currentDigest, err := canonicalFleetArtifact(currentCompiled.Runtimes, 32<<20)
		if err != nil || !bytes.Equal(graphDigest, currentDigest) {
			return ErrFleetNetworkRevision
		}
		bundle, err := fundFleetNetwork(tx, request.OperationID, state, current, graphDigest, runtimes, funding)
		if err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		revision, err := store.PublishConfiguration(ctx, identity.Principal{NodeID: state.Node.ID, InstallationID: state.Node.InstallationID}, bundle)
		if err != nil {
			return err
		}
		if err := persistFleetNativeBindings(tx, state.Node, revision, current.NativeBindings); err != nil {
			return err
		}
		cursor := ""
		for page := 0; page <= len(current.Placements)/128; page++ {
			cursor, err = store.RequeueNodeCredit(ctx, state.Node.ID, cursor, 128)
			if err != nil {
				return err
			}
			if cursor == "" {
				break
			}
		}
		if cursor != "" {
			return ErrFleetNetworkRevision
		}
		publication := model.FleetNetworkPublication{OperationID: request.OperationID, CompilationID: compilation.ID, NodeID: state.Node.ID, DeploymentRevision: int64(revision), CreatedUnixMilli: time.Now().UnixMilli()}
		if err := tx.Create(&publication).Error; err != nil {
			return err
		}
		if err := projectFleetProviderBindings(tx, publication, current); err != nil {
			return err
		}
		if err := tx.Model(&state.Head).Updates(map[string]any{"published_compilation_id": compilation.ID, "pending": false}).Error; err != nil {
			return err
		}
		view = FleetNetworkPublicationView{CompilationID: compilation.ID, NodeID: state.Node.ID, DeploymentRevision: revision}
		encoded, err := json.Marshal(view)
		if err != nil {
			return err
		}
		return tx.Model(&op).Update("result", encoded).Error
	}()
	return view, err
}

func fundFleetNetwork(tx *gorm.DB, operationID string, state fleetNetworkState, manifest FleetNetworkManifest, digest []byte, definitions []nodeconfig.RuntimeDefinition, funding []FleetNetworkFunding) (configuration.Bundle, error) {
	bundle := configuration.Bundle{Version: 1}
	if err := requireFleetSourceStops(tx, state.Node, manifest); err != nil {
		return bundle, err
	}
	byRuntime := map[string]int{}
	for _, definition := range definitions {
		if err := controlplane.RequireRuntimeJournalTx(tx, state.Node.ID, state.Node.InstallationID, definition.ID); err != nil {
			return bundle, err
		}
		byRuntime[definition.ID] = len(bundle.Runtimes)
		bundle.Runtimes = append(bundle.Runtimes, configuration.Runtime{ID: definition.ID, Core: definition.Core, Configuration: definition.Configuration, FileAssets: definition.FileAssets})
	}
	funded := map[string]FleetNetworkFunding{}
	for _, item := range funding {
		funded[item.PlacementID] = item
	}
	var snapshots []fleetClientSnapshot
	var budgetSnapshots []fleetClientSnapshot
	placements := map[string]model.FleetPlacement{}
	identities := map[string]FleetNetworkAuthorityIdentity{}
	for _, identity := range manifest.Identities {
		identities[identity.PlacementID] = identity
	}
	for _, pin := range manifest.Placements {
		snapshot, placement, _, err := fleetPlacementSnapshot(tx, pin.PlacementID)
		if err != nil {
			return bundle, err
		}
		if err := requireFleetPlacementSourceReadyTx(tx, placement); err != nil {
			return bundle, err
		}
		if !snapshot.Fleet.Owned || snapshot.Term != nil && snapshot.Term.State != "active" || snapshot.Term == nil && !nodeadmission.SupportsRuntime(placement.CoreID) {
			return bundle, ErrFleetNetworkDependency
		}
		snapshots = append(snapshots, snapshot)
		placements[placement.ID] = placement
		_, newFunding := funded[placement.ID]
		if snapshot.Term != nil && (newFunding || placement.CurrentGrantID != nil && placement.CurrentGrantTermID != nil && *placement.CurrentGrantTermID == snapshot.Term.ID) {
			budgetSnapshots = append(budgetSnapshots, snapshot)
		}
	}
	for id := range funded {
		if _, exists := placements[id]; !exists {
			return bundle, ErrFleetNetworkFunding
		}
	}
	if err := lockFleetNetworkBudgets(tx, budgetSnapshots); err != nil {
		return bundle, err
	}
	included := map[string]bool{}
	for i, pin := range manifest.Placements {
		snapshot, placement := snapshots[i], placements[pin.PlacementID]
		index, exists := byRuntime[placement.RuntimeID]
		if !exists {
			return bundle, ErrFleetNetworkDependency
		}
		if err := tx.Model(&placement).Updates(map[string]any{"enabled": true, "graph_digest": digest}).Error; err != nil {
			return bundle, err
		}
		if nodeadmission.SupportsRuntime(placement.CoreID) {
			identity := identities[placement.ID]
			for _, instance := range identity.InboundInstanceIDs {
				bundle.Runtimes[index].Admissions = append(bundle.Runtimes[index].Admissions, nodeadmission.Descriptor{
					ClientID: snapshot.Fleet.ID, CredentialGeneration: uint64(snapshot.Fleet.CredentialGeneration), PlacementID: placement.ID,
					PlacementRevision: uint64(placement.Revision), InboundInstanceID: instance, InboundTag: identity.InboundTags[instance],
				})
			}
		}
		var credit FleetCredit
		if item, exists := funded[placement.ID]; exists {
			if snapshot.Term == nil {
				return bundle, ErrFleetNetworkFunding
			}
			if placement.CurrentGrantID != nil && placement.CurrentGrantTermID != nil && *placement.CurrentGrantTermID == snapshot.Term.ID {
				return bundle, ErrFleetNetworkFunding
			}
			var err error
			credit, err = AllocateCreditTx(tx.Statement.Context, tx, FleetCreditRequest{
				OperationID: uuid.NewSHA1(uuid.MustParse(operationID), []byte("placement/"+placement.ID)).String(), PlacementID: placement.ID, TermID: snapshot.Term.ID,
				ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(snapshot.Fleet.CredentialGeneration), Bytes: item.Bytes, Rates: item.Rates,
			})
			if err != nil {
				return bundle, err
			}
		} else {
			if snapshot.Term == nil || placement.CurrentGrantID == nil || placement.CurrentGrantTermID == nil || *placement.CurrentGrantTermID != snapshot.Term.ID {
				if nodeadmission.SupportsRuntime(placement.CoreID) {
					continue
				}
				return bundle, ErrFleetNetworkFunding
			}
			if err := tx.First(&credit.Grant, "id = ?", *placement.CurrentGrantID).Error; err != nil {
				return bundle, err
			}
			if err := tx.First(&credit.Rates, "id = ?", *placement.CurrentGrantID).Error; err != nil {
				return bundle, err
			}
			if credit.Rates.Revision != credit.Rates.AppliedRevision {
				return bundle, ErrFleetPhase
			}
		}
		var rates controlplane.Rates
		if json.Unmarshal([]byte(credit.Rates.Desired), &rates) != nil {
			return bundle, ErrFleetNetwork
		}
		bundle.Runtimes[index].Grants = append(bundle.Runtimes[index].Grants, governor.Grant{
			ID: credit.Grant.ID, ClientID: snapshot.Fleet.ID, RateRevision: uint64(credit.Rates.Revision),
			Policy: limits.Policy{
				Term: snapshot.Term.ID, Revision: 1, CreditRevision: uint64(credit.Grant.Revision), Enabled: true, ExpiresUnixMilli: snapshot.Term.ExpiresUnixMilli,
				QuotaUnlimited: credit.Grant.Unlimited, CreditCeiling: uint64(credit.Grant.IssuedBytes), Upload: rates.Upload, Download: rates.Download,
			},
		})
		included[credit.Grant.ID] = true
	}
	return bundle, requireFleetNetworkRetirements(tx, state.Node, included)
}
