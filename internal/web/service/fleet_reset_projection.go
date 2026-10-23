package service

import (
	"math"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func refreshFleetResetTermTx(tx *gorm.DB, client model.FleetClient, source model.ClientRecord, term model.FleetTerm) error {
	if term.ClientID != client.ID || term.State != "active" || term.StartedUnixMilli <= 0 {
		return ErrFleetResetSchedule
	}
	var schedule model.FleetResetSchedule
	found := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("client_id = ?", client.ID).Find(&schedule)
	if found.Error != nil {
		return found.Error
	}
	if schedule.PendingOperationID != nil {
		return nil
	}
	cycle, err := fleetResetCycleTx(tx, source, schedule.OverrideCycle)
	if err != nil {
		return err
	}
	if found.RowsAffected == 0 && cycle == "none" {
		return nil
	}
	cycleChanged := schedule.EffectiveCycle != cycle
	termChanged := schedule.BoundTermID == nil || *schedule.BoundTermID != term.ID
	if found.RowsAffected == 0 || cycleChanged {
		if schedule.Revision == math.MaxInt64 {
			return controlplane.ErrRevision
		}
		schedule.Revision++
	}
	schedule.ClientID, schedule.EffectiveCycle = client.ID, cycle
	if schedule.AnchorUnixMilli == 0 || termChanged && term.Kind != controlplane.TrafficReset {
		schedule.AnchorUnixMilli = term.StartedUnixMilli
	} else if cycleChanged {
		schedule.AnchorUnixMilli = max(term.StartedUnixMilli, fleetResetNow().UnixMilli())
	}
	if termChanged || cycleChanged || schedule.NextDueUnixMilli == 0 {
		schedule.Diagnostic = ""
		schedule.NextDueUnixMilli, err = nextFleetResetDue(cycle, schedule.AnchorUnixMilli, max(schedule.AnchorUnixMilli, term.StartedUnixMilli))
		if err != nil {
			return err
		}
	}
	schedule.BoundTermID = &term.ID
	schedule.ReadyUnixMilli = 0
	if source.Enable && (term.ExpiresUnixMilli == 0 || schedule.NextDueUnixMilli < term.ExpiresUnixMilli) {
		schedule.ReadyUnixMilli = schedule.NextDueUnixMilli
	}
	return tx.Save(&schedule).Error
}

func refreshFleetResetEnabledTx(tx *gorm.DB, client model.FleetClient, enabled bool) error {
	if !enabled {
		return tx.Model(&model.FleetResetSchedule{}).Where("client_id = ? AND pending_operation_id IS NULL", client.ID).Update("ready_unix_milli", 0).Error
	}
	if client.CurrentTermID == nil {
		return nil
	}
	var source model.ClientRecord
	if client.PanelClientID == nil {
		return ErrFleetSource
	}
	if err := tx.First(&source, *client.PanelClientID).Error; err != nil {
		return err
	}
	var term model.FleetTerm
	if err := tx.First(&term, "id = ? AND client_id = ?", *client.CurrentTermID, client.ID).Error; err != nil {
		return err
	}
	if term.State != "active" {
		return nil
	}
	return refreshFleetResetTermTx(tx, client, source, term)
}
