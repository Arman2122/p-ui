package model

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
	Tiers     string `json:"tiers" form:"tiers" gorm:"type:jsonb;not null;default:'[]'" example:"[{\"fromBytes\":0,\"upBps\":0,\"downBps\":0}]"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime:milli" example:"1700000000000"`
	UpdatedAt int64  `json:"updatedAt" gorm:"autoUpdateTime:milli" example:"1700000000000"`
}

func (Policy) TableName() string { return "policies" }

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
	PolicyId  *int  `json:"policyId" form:"policyId" gorm:"column:policy_id;index" example:"1"`
	UpdatedAt int64 `json:"updatedAt" gorm:"autoUpdateTime:milli" example:"1700000000000"`
}

func (ClientPolicy) TableName() string { return "client_policies" }
