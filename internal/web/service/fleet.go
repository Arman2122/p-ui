package service

import (
	"context"
	"encoding/json"
	"os"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/logger"
)

var fleetControl = controlplane.Manager{Activation: HandleFleetActivation, CreditDelivery: ValidateFleetCreditDeliveryTx, AuthorityDelivery: ValidateFleetAuthorityDeliveryTx, RefillScheduler: RunFleetAuthoritySchedulers}

type FleetStatusView struct {
	Ready                       bool   `json:"ready" example:"true"`
	ControlPlaneID              string `json:"controlPlaneId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	ControlAddress              string `json:"controlAddress" example:"control.example.com:7443"`
	EnrollmentURL               string `json:"enrollmentUrl" example:"https://control.example.com:7444/enroll"`
	RootSHA256                  string `json:"rootSha256"`
	CertificateExpiresUnixMilli int64  `json:"certificateExpiresUnixMilli"`
	RecoveryRequired            bool   `json:"recoveryRequired"`
	RecoveryReason              string `json:"recoveryReason"`
	Error                       string `json:"error"`
}

type FleetNodeView struct {
	ID                string                      `json:"id" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	Name              string                      `json:"name" example:"fra-1"`
	Enrolled          bool                        `json:"enrolled" example:"true"`
	Online            bool                        `json:"online" example:"true"`
	LastSeenUnixMilli int64                       `json:"lastSeenUnixMilli"`
	DesiredRevision   int64                       `json:"desiredRevision" example:"4"`
	AppliedRevision   int64                       `json:"appliedRevision" example:"4"`
	DeploymentState   string                      `json:"deploymentState" example:"applied"`
	RuntimeState      string                      `json:"runtimeState" example:"ready"`
	RuntimeObservedAt int64                       `json:"runtimeObservedAt"`
	RuntimeConditions []FleetRuntimeConditionView `json:"runtimeConditions"`
}

type FleetRuntimeConditionView struct {
	ResourceID string `json:"resourceId" example:"xray"`
	Condition  string `json:"condition" example:"CONFIGURATION_APPLIED"`
	Ready      bool   `json:"ready" example:"true"`
}

type (
	FleetEnrollmentRequest struct {
		Name string `json:"name" binding:"required" validate:"required,max=128" example:"fra-1"`
	}
	FleetEnrollmentView struct {
		NodeID           string `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
		ExpiresUnixMilli int64  `json:"expiresUnixMilli"`
		PackageJSON      string `json:"packageJson" example:"{}"`
	}
)

type FleetNodePage struct {
	Items      []FleetNodeView `json:"items"`
	NextCursor string          `json:"nextCursor"`
}

func FleetStatus(ctx context.Context) (FleetStatusView, error) {
	host, err := FleetHost()
	if err != nil {
		return FleetStatusView{Error: err.Error()}, nil
	}
	status, err := host.Status(ctx)
	if err != nil {
		return FleetStatusView{}, err
	}
	return FleetStatusView{Ready: true, ControlPlaneID: status.ControlPlaneID, ControlAddress: status.ControlAddress, EnrollmentURL: status.EnrollmentURL, RootSHA256: status.RootSHA256, CertificateExpiresUnixMilli: status.CertificateExpiresUnixMilli, RecoveryRequired: status.RecoveryRequired, RecoveryReason: status.RecoveryReason}, nil
}

func FleetNodes(ctx context.Context, after string, limit int) (FleetNodePage, error) {
	store, err := controlplane.NewStore(database.GetDB())
	if err != nil {
		return FleetNodePage{}, err
	}
	nodes, next, err := store.Nodes(ctx, after, limit)
	if err != nil {
		return FleetNodePage{}, err
	}
	ids := make([]string, len(nodes))
	for i, node := range nodes {
		ids[i] = node.ID
	}
	observations, err := store.Observations(ctx, ids)
	if err != nil {
		return FleetNodePage{}, err
	}
	result := make([]FleetNodeView, 0, len(nodes))
	freshAfter := time.Now().Add(-45 * time.Second).UnixMilli()
	for _, node := range nodes {
		view := FleetNodeView{
			ID: node.ID, Name: node.Name, Enrolled: node.Enabled, Online: node.Enabled && node.LastSeenUnixMilli > freshAfter,
			LastSeenUnixMilli: node.LastSeenUnixMilli, DesiredRevision: node.DesiredRevision, AppliedRevision: node.AppliedRevision,
			DeploymentState: "pending", RuntimeState: "unknown", RuntimeConditions: []FleetRuntimeConditionView{},
		}
		if node.DesiredRevision == node.AppliedRevision {
			view.DeploymentState = "applied"
		}
		if node.DesiredRevision == 0 {
			view.DeploymentState = "unconfigured"
		}
		if observed, ok := observations[node.ID]; ok {
			if observed.ResultRevision == node.DesiredRevision && observed.ResultStage == int32(nodev1.ApplyStage_APPLY_STAGE_REJECTED) {
				view.DeploymentState = "rejected"
			}
			var conditions []*nodev1.RuntimeCondition
			if err := json.Unmarshal([]byte(observed.ConditionsJSON), &conditions); err != nil {
				return FleetNodePage{}, err
			}
			view.RuntimeObservedAt = observed.ReceivedUnixMilli
			if observed.ReceivedUnixMilli > 0 {
				view.RuntimeState = "stale"
				if observed.RuntimeGeneration == node.AuthorityGeneration && observed.ReceivedUnixMilli > freshAfter && view.Online && len(conditions) > 0 {
					view.RuntimeState = "ready"
					for _, condition := range conditions {
						if !condition.Ready {
							view.RuntimeState = "degraded"
						}
					}
				}
			}
			for _, condition := range conditions {
				view.RuntimeConditions = append(view.RuntimeConditions, FleetRuntimeConditionView{ResourceID: condition.ResourceId, Condition: condition.Condition, Ready: condition.Ready})
			}
		}
		result = append(result, view)
	}
	return FleetNodePage{Items: result, NextCursor: next}, nil
}

func EnrollFleetNode(ctx context.Context, request FleetEnrollmentRequest) (FleetEnrollmentView, error) {
	host, err := FleetHost()
	if err != nil {
		return FleetEnrollmentView{}, err
	}
	pack, err := host.Enroll(ctx, request.Name)
	if err != nil {
		return FleetEnrollmentView{}, err
	}
	encoded, err := json.Marshal(pack)
	if err != nil {
		return FleetEnrollmentView{}, err
	}
	return FleetEnrollmentView{NodeID: pack.Configuration.Identity.NodeID, ExpiresUnixMilli: pack.ExpiresUnixMilli, PackageJSON: string(encoded)}, nil
}

func FleetHost() (*controlplane.Host, error) { return fleetControl.Current() }

func RunFleetControl(ctx context.Context) {
	path := os.Getenv("PUI_FLEET_CONFIG")
	if path == "" {
		path = "/etc/penhoon/control.json"
	}
	fleetControl.Run(ctx, database.GetDB, path, func(err error) { logger.Warning("node control plane unavailable: ", err) })
}
