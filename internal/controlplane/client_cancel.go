package controlplane

import (
	"context"
	"math"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func (s *Store) CancelTransition(ctx context.Context, request CancelTransitionRequest) (TransitionResult, error) {
	if !validID(request.ClientID) || !validID(request.PendingTermID) || request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64 ||
		request.ExpectedCredentialGeneration == 0 || request.ExpectedCredentialGeneration > math.MaxInt64 {
		return TransitionResult{}, ErrInvalid
	}
	return s.lifecycleOperation(ctx, request.OperationID, "cancel", request, func(tx *gorm.DB) (TransitionResult, error) {
		client, err := lockClient(tx, request.ClientID)
		if err != nil {
			return TransitionResult{}, err
		}
		if uint64(client.Revision) != request.ExpectedRevision || uint64(client.CredentialGeneration) != request.ExpectedCredentialGeneration || client.Revision == math.MaxInt64 ||
			client.PendingTermID == nil || *client.PendingTermID != request.PendingTermID || client.CurrentTermID == nil {
			return TransitionResult{}, ErrRevision
		}
		var term, previous model.FleetTerm
		if err := tx.First(&term, "id = ? AND client_id = ?", request.PendingTermID, client.ID).Error; err != nil {
			return TransitionResult{}, err
		}
		if err := tx.First(&previous, "id = ? AND client_id = ?", *client.CurrentTermID, client.ID).Error; err != nil {
			return TransitionResult{}, err
		}
		if term.State != "pending" || previous.State != "draining" || term.PredecessorID == nil || *term.PredecessorID != previous.ID {
			return TransitionResult{}, ErrRevision
		}
		if err := tx.Model(&term).Update("state", "closed").Error; err != nil {
			return TransitionResult{}, err
		}
		previous.State = "active"
		if err := tx.Model(&previous).Update("state", previous.State).Error; err != nil {
			return TransitionResult{}, err
		}
		client.PendingTermID = nil
		client.Revision++
		if err := tx.Save(&client).Error; err != nil {
			return TransitionResult{}, err
		}
		return TransitionResult{Client: client, Term: previous}, nil
	})
}
