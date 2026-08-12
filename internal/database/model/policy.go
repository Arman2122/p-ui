package model

import "encoding/json"

/*
Policy is one named ladder of speed tiers, the whole product rule as a value.

Tiers is a JSON column rather than a table of its own because a tier has no
identity any caller needs: the ladder is read, sorted and evaluated as one
thing, and splitting it would buy a join on every pass for nothing. A plain
speed limit is a one-tier ladder starting at zero, so there is no second shape.
*/
type Policy struct {
	Id   int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Name string `json:"name" form:"name" gorm:"unique;not null" example:"fair use"`
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

/*
PolicyInbound is the inbound set a plan provisions its members onto.

An empty set means the plan does not manage attachment, which is what every plan
predating templates has. The FK cascades: unlike an assignment, a membership row
naming a deleted inbound carries no evidence anyone needs.
*/
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

/*
ClientPolicy assigns one client a policy, keyed by EMAIL and not by client id.

client_credentials is client-id keyed and dies with its client, which is right
for a credential: a recreated client legitimately gets a fresh UUID. It is wrong
for a plan. Deleting a client hard-deletes the row and a node re-sync mints a new
id for the same email, so an id-keyed assignment would silently vanish and drop a
paying customer back to no plan. client_traffics is email-keyed for exactly this
reason — a quota survives a re-sync because of it — and an assignment has a
quota's lifetime, not a UUID's.
*/
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
	AppliedRev int   `json:"appliedRev" gorm:"column:applied_rev;not null;default:0" example:"3"`
	UpdatedAt  int64 `json:"updatedAt" gorm:"autoUpdateTime:milli" example:"1700000000000"`
}

func (ClientPolicy) TableName() string { return "client_policies" }
