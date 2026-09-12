package model

import "encoding/json"

// Policy is a reusable client template; its speed ladder remains live on edits.
type Policy struct {
	Id              int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Name            string `json:"name" form:"name" gorm:"not null" example:"fair use"`
	OwnerUserID     *int   `json:"ownerUserId" gorm:"column:owner_user_id;index" example:"2"`
	Description     string `json:"description" gorm:"not null;default:''" example:"Monthly family plan"`
	Archived        bool   `json:"archived" gorm:"not null;default:false" example:"false"`
	StartOnFirstUse bool   `json:"startOnFirstUse" gorm:"not null;default:false" example:"true"`
	AutoRenewDays   int    `json:"autoRenewDays" gorm:"not null;default:0" example:"30"`
	ResetCycle      string `json:"resetCycle" gorm:"not null;default:'none'" example:"monthly"`
	OnDepletion     string `json:"onDepletion" gorm:"not null;default:'disable'" example:"disable"`
	Burst           bool   `json:"burst" gorm:"not null;default:false" example:"false"`
	InboundIDs      []int  `json:"inboundIds" gorm:"-" example:"[1,2]"`
	AssignedClients int64  `json:"assignedClients" gorm:"-" example:"12"`
	PendingClients  int64  `json:"pendingClients" gorm:"-" example:"2"`
	UsedBytes       int64  `json:"usedBytes" gorm:"-" example:"10737418240"`
	CommittedBytes  int64  `json:"committedBytes" gorm:"-" example:"644245094400"`
	// Tiers is a policy.Tier array. Sorted and de-duplicated on write, so the
	// evaluation on every pass is a scan and never a sort.
	Tiers string `json:"tiers" form:"tiers" gorm:"type:jsonb;not null;default:'[]'" example:"[{\"fromBytes\":0,\"upBps\":0,\"downBps\":0}]"`
	// NULL means the plan does not manage the field; 0 means managed and unlimited.
	// Both states are needed, or the first edit of a ladder-only plan wipes members.
	QuotaBytes   *int64 `json:"quotaBytes" form:"quotaBytes" gorm:"column:quota_bytes" example:"53687091200"`
	DurationDays *int   `json:"durationDays" form:"durationDays" gorm:"column:duration_days" example:"30"`
	LimitIP      *int   `json:"limitIp" form:"limitIp" gorm:"column:limit_ip" example:"2"`
	// Rev bumps only when a managed field or the inbound set changes, so editing a
	// ladder lights no pending badge. client_policies.applied_rev chases it.
	Rev       int   `json:"rev" gorm:"column:rev;not null;default:0" example:"3"`
	CreatedAt int64 `json:"createdAt" gorm:"autoCreateTime:milli" example:"1700000000000"`
	UpdatedAt int64 `json:"updatedAt" gorm:"autoUpdateTime:milli" example:"1700000000000"`
}

// An empty inbound set preserves the attachments of legacy ladder-only plans.
type PolicyInbound struct {
	PolicyId  int   `json:"policyId" gorm:"primaryKey;column:policy_id"`
	InboundId int   `json:"inboundId" gorm:"primaryKey;column:inbound_id;index"`
	CreatedAt int64 `json:"createdAt" gorm:"autoCreateTime:milli"`
}

func (PolicyInbound) TableName() string { return "policy_inbounds" }

func (Policy) TableName() string { return "policies" }

// MarshalJSON emits the ladder as the array it is rather than as the JSON text
// the column stores, which is the shape the OpenAPI schema already publishes.
func (p Policy) MarshalJSON() ([]byte, error) {
	type alias Policy
	return json.Marshal(struct {
		alias
		Tiers json.RawMessage `json:"tiers"`
	}{
		alias: alias(p),
		Tiers: jsonStringFieldToRaw(p.Tiers),
	})
}

// UnmarshalJSON accepts tiers as an array or as JSON-encoded text. Without the
// array branch every documented request body is rejected by the bind.
func (p *Policy) UnmarshalJSON(data []byte) error {
	type alias Policy
	aux := struct {
		*alias
		Tiers json.RawMessage `json:"tiers"`
	}{
		alias: (*alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	p.Tiers = jsonStringFieldFromRaw(aux.Tiers)
	return nil
}

// Assignments use email so a node re-sync that changes the client id preserves them.
type ClientPolicy struct {
	Email string `json:"email" form:"email" gorm:"primaryKey;column:email" example:"user1"`
	// PolicyId is nullable and the FK is ON DELETE SET NULL, not CASCADE: deleting
	// a plan must leave the assignment visible-and-unresolved for the UI to report.
	PolicyId *int `json:"policyId" form:"policyId" gorm:"column:policy_id;index" example:"1"`
	// The bit IS the override, and only explicit operator intent sets it. The value
	// itself lives on the client, so it cannot be inferred by comparing to the plan.
	OverrideQuota    bool `json:"overrideQuota" gorm:"column:override_quota;not null;default:false" example:"false"`
	OverrideExpiry   bool `json:"overrideExpiry" gorm:"column:override_expiry;not null;default:false" example:"false"`
	OverrideLimitIP  bool `json:"overrideLimitIp" gorm:"column:override_limit_ip;not null;default:false" example:"false"`
	OverrideInbounds bool `json:"overrideInbounds" gorm:"column:override_inbounds;not null;default:false" example:"false"`
	// AppliedRev is what the PANEL's own writes reached, never what a node holds.
	// Node convergence rides MarkNodeDirtyTx and the pending indicator that exists.
	AppliedRev          int    `json:"appliedRev" gorm:"column:applied_rev;not null;default:0" example:"3"`
	LastResetAt         int64  `json:"lastResetAt" gorm:"not null;default:0" example:"1700000000000"`
	AppliedDurationDays *int   `json:"appliedDurationDays" example:"30"`
	AppliedOnDepletion  string `json:"appliedOnDepletion" gorm:"not null;default:'disable'" example:"disable"`
	AppliedResetCycle   string `json:"appliedResetCycle" gorm:"not null;default:'none'" example:"weekly"`
	DepletionNotifiedAt int64  `json:"depletionNotifiedAt" gorm:"not null;default:0" example:"0"`
	UpdatedAt           int64  `json:"updatedAt" gorm:"autoUpdateTime:milli" example:"1700000000000"`
	// ResolvedTiers is the ladder as the master that owns this client evaluated
	// it, pushed to a node; NULL wherever the plan itself lives (policy_id).
	ResolvedTiers *string `json:"resolvedTiers" gorm:"column:resolved_tiers;type:jsonb" example:"[{\"fromBytes\":0,\"upBps\":0,\"downBps\":0}]"`
}

func (ClientPolicy) TableName() string { return "client_policies" }
