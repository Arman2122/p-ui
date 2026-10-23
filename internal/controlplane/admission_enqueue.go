package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func (s *Store) EnqueueAdmissionDemand(ctx context.Context, principal identity.Principal, generation uint64, request *nodev1.ActivationRequest, termID string, policyDigest []byte) (model.FleetAdmissionDemand, error) {
	var result model.FleetAdmissionDemand
	if request == nil || !validID(termID) || !validID(request.RequestId) || !validID(request.PlacementId) || !validID(request.ClientId) ||
		!validID(request.InboundInstanceId) || !runtimeName.MatchString(request.RuntimeId) || generation == 0 || generation > math.MaxInt64 || len(policyDigest) != sha256.Size ||
		request.CredentialGeneration == 0 || request.CredentialGeneration > math.MaxInt64 || request.PlacementRevision == 0 || request.PlacementRevision > math.MaxInt64 ||
		request.DeploymentRevision == 0 || request.DeploymentRevision > math.MaxInt64 {
		return result, ErrInvalid
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockNode(tx, principal.NodeID, principal.InstallationID); err != nil {
			return err
		}
		if err := (&Store{db: tx}).CheckSession(ctx, principal, generation, false); err != nil {
			return err
		}
		if err := RequireRuntimeJournalTx(tx, principal.NodeID, principal.InstallationID, request.RuntimeId); err != nil {
			return err
		}
		var agent model.FleetNodeJournal
		if err := tx.First(&agent, "node_id = ?", principal.NodeID).Error; err != nil {
			return err
		}
		var runtime model.FleetRuntimeJournal
		if err := tx.First(&runtime, "node_id = ? AND runtime_id = ?", principal.NodeID, request.RuntimeId).Error; err != nil {
			return err
		}
		if agent.StateJournalID == nil {
			return ErrRecovery
		}
		client, err := lockClient(tx, request.ClientId)
		if err != nil {
			return err
		}
		var placement model.FleetPlacement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&placement, "id = ?", request.PlacementId).Error; err != nil {
			return err
		}
		if !client.Owned || !nodeadmission.SupportsRuntime(placement.CoreID) || uint64(client.CredentialGeneration) != request.CredentialGeneration || placement.ClientID != client.ID || !placement.Enabled || len(placement.GraphDigest) != sha256.Size ||
			placement.NodeID != principal.NodeID || placement.InstallationID != principal.InstallationID || placement.RuntimeID != request.RuntimeId ||
			uint64(placement.Revision) != request.PlacementRevision || placement.CredentialGeneration != client.CredentialGeneration || !bytes.Equal(placement.SourcePolicyDigest, policyDigest) {
			return ErrScope
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", principal.NodeID).Error; err != nil {
			return err
		}
		if node.DesiredRevision != node.AppliedRevision || node.AppliedRevision != int64(request.DeploymentRevision) {
			return ErrRevision
		}
		term, err := managedAuthority(tx, client.ID, termID, true)
		if err != nil || term == nil {
			return errorsOrScope(err)
		}
		descriptor, deploymentDigest, err := DeployedAdmissionTx(tx, placement, request)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return err
		}
		descriptorDigest := sha256.Sum256(encoded)
		encoded, err = json.Marshal(struct {
			Request    *nodev1.ActivationRequest
			TermID     string
			Generation uint64
			Policy     []byte
			Deployment []byte
			Descriptor []byte
			Journals   [3]string
		}{request, termID, generation, policyDigest, deploymentDigest, descriptorDigest[:], [3]string{*agent.StateJournalID, runtime.StateJournalID, runtime.CreditJournalID}})
		if err != nil {
			return err
		}
		digest := sha256.Sum256(encoded)
		now := time.Now().UnixMilli()
		if request.DeadlineUnixMilli <= now || request.DeadlineUnixMilli > now+15000 || term.ExpiresUnixMilli != 0 && term.ExpiresUnixMilli <= now {
			return ErrScope
		}
		var previous model.FleetAdmissionDemand
		if err := tx.Where("placement_id = ?", placement.ID).Find(&previous).Error; err != nil {
			return err
		}
		if previous.RequestID == request.RequestId {
			if !bytes.Equal(previous.RequestDigest, digest[:]) {
				return ErrConflict
			}
			result = previous
			return nil
		}
		if previous.Revision == math.MaxInt64 {
			return ErrRevision
		}
		result = model.FleetAdmissionDemand{
			PlacementID: placement.ID, NodeID: principal.NodeID, InstallationID: principal.InstallationID, ClientID: client.ID, TermID: term.ID, RuntimeID: placement.RuntimeID,
			AgentStateJournalID: *agent.StateJournalID, StateJournalID: runtime.StateJournalID, CreditJournalID: runtime.CreditJournalID,
			InboundInstanceID: descriptor.InboundInstanceID, InboundTag: descriptor.InboundTag, RequestID: request.RequestId, RequestDigest: digest[:],
			SourcePolicyDigest: bytes.Clone(policyDigest), DeploymentDigest: deploymentDigest, DescriptorDigest: descriptorDigest[:], Revision: previous.Revision + 1,
			SessionGeneration: int64(generation), PlacementRevision: int64(request.PlacementRevision), CredentialGeneration: int64(request.CredentialGeneration), DeploymentRevision: int64(request.DeploymentRevision),
			ReceivedUnixMilli: now, DeadlineUnixMilli: request.DeadlineUnixMilli, AvailableUnixMilli: now, Pending: true,
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&result).Error; err != nil {
			return err
		}
		queue := model.FleetAdmissionNodeQueue{NodeID: principal.NodeID, Pending: true, AvailableUnixMilli: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}}, DoUpdates: clause.Assignments(map[string]any{
			"pending": true, "available_unix_milli": gorm.Expr("CASE WHEN fleet_admission_node_queues.pending THEN LEAST(fleet_admission_node_queues.available_unix_milli, EXCLUDED.available_unix_milli) ELSE EXCLUDED.available_unix_milli END"),
		})}).Create(&queue).Error
	})
	return result, err
}
