package model

type FleetNodePlacementPolicy struct {
	NodeID           string `gorm:"type:uuid;primaryKey"`
	InstallationID   string `gorm:"type:uuid;not null"`
	Revision         int64  `gorm:"not null;check:fleet_node_placement_revision,revision > 0"`
	Enabled          bool   `gorm:"not null"`
	Labels           string `gorm:"type:jsonb;not null;index:idx_fleet_node_placement_labels,type:gin"`
	InboundIDs       string `gorm:"type:jsonb;not null"`
	Digest           []byte `gorm:"not null;check:fleet_node_placement_digest,octet_length(digest) = 32"`
	ActorID          int    `gorm:"not null"`
	UpdatedUnixMilli int64  `gorm:"not null"`
}

type FleetPlacementDefaults struct {
	ID               int     `gorm:"primaryKey;check:fleet_placement_defaults_singleton,id = 1"`
	Revision         int64   `gorm:"not null;check:fleet_placement_defaults_revision,revision > 0"`
	NodeID           *string `gorm:"type:uuid"`
	InstallationID   *string `gorm:"type:uuid;check:fleet_placement_defaults_scope,(node_id IS NULL AND installation_id IS NULL) OR (node_id IS NOT NULL AND installation_id IS NOT NULL)"`
	ActorID          int     `gorm:"not null"`
	UpdatedUnixMilli int64   `gorm:"not null"`
}

type FleetPlacementRule struct {
	ID               string `gorm:"type:uuid;primaryKey"`
	Revision         int64  `gorm:"not null;check:fleet_placement_rule_revision,revision > 0"`
	Scope            string `gorm:"size:16;not null;check:fleet_placement_rule_scope,(scope = 'plan' AND plan_id IS NOT NULL AND group_id IS NULL) OR (scope = 'group' AND group_id IS NOT NULL AND plan_id IS NULL)"`
	PlanID           *int   `gorm:"index:idx_fleet_placement_rule_plan"`
	GroupID          *int   `gorm:"index:idx_fleet_placement_rule_group"`
	Enabled          bool   `gorm:"not null"`
	NodeIDs          string `gorm:"type:jsonb;not null"`
	Labels           string `gorm:"type:jsonb;not null"`
	InboundIDs       string `gorm:"type:jsonb;not null"`
	Digest           []byte `gorm:"not null;check:fleet_placement_rule_digest,octet_length(digest) = 32"`
	ActorID          int    `gorm:"not null"`
	UpdatedUnixMilli int64  `gorm:"not null"`
}

type FleetNodeCapabilitySnapshot struct {
	NodeID            string `gorm:"type:uuid;primaryKey"`
	InstallationID    string `gorm:"type:uuid;not null"`
	Revision          int64  `gorm:"not null;check:fleet_capability_revision,revision > 0"`
	SessionGeneration int64  `gorm:"not null;check:fleet_capability_session,session_generation > 0"`
	Capabilities      string `gorm:"type:jsonb;not null"`
	Digest            []byte `gorm:"not null;check:fleet_capability_digest,octet_length(digest) = 32"`
	ReceivedUnixMilli int64  `gorm:"not null"`
}

type FleetPlacementIntent struct {
	ID                      string  `gorm:"type:uuid;primaryKey"`
	ClientID                string  `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_placement_intent_scope,priority:1;index"`
	NodeID                  string  `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_placement_intent_scope,priority:2;index:idx_fleet_placement_intent_due,priority:1,where:pending"`
	InstallationID          string  `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_placement_intent_scope,priority:3;index:idx_fleet_placement_intent_due,priority:2"`
	RuntimeID               string  `gorm:"size:48;not null;uniqueIndex:idx_fleet_placement_intent_scope,priority:4"`
	CoreID                  string  `gorm:"size:48;not null"`
	MaterializedPlacementID *string `gorm:"type:uuid;index"`
	Revision                int64   `gorm:"not null;check:fleet_placement_intent_revision,revision > 0"`
	SourceRevision          int64   `gorm:"not null;check:fleet_placement_intent_source,source_revision > 0"`
	CredentialGeneration    int64   `gorm:"not null;check:fleet_placement_intent_credentials,credential_generation > 0"`
	NodePolicyRevision      int64   `gorm:"not null;check:fleet_placement_intent_node_policy,node_policy_revision > 0"`
	DefaultRevision         int64   `gorm:"not null;default:0;check:fleet_placement_intent_default,default_revision >= 0"`
	RulePins                string  `gorm:"type:jsonb;not null;index:idx_fleet_placement_intent_rules,type:gin"`
	InboundIDs              string  `gorm:"type:jsonb;not null"`
	Digest                  []byte  `gorm:"not null;check:fleet_placement_intent_digest,octet_length(digest) = 32"`
	Assigned                bool    `gorm:"not null"`
	RequiresStop            bool    `gorm:"not null;index:idx_fleet_placement_intent_due,priority:3,sort:desc"`
	Pending                 bool    `gorm:"not null"`
	State                   string  `gorm:"size:32;not null"`
	AppliedStopRevision     int64   `gorm:"not null;default:0;check:fleet_placement_intent_stop,applied_stop_revision >= 0"`
	AppliedRevision         int64   `gorm:"not null;default:0;check:fleet_placement_intent_applied,applied_revision >= 0"`
	AvailableUnixMilli      int64   `gorm:"not null;index:idx_fleet_placement_intent_due,priority:4"`
	QueuedUnixMilli         int64   `gorm:"not null"`
}

type FleetPlacementFanout struct {
	Scope               string  `gorm:"size:16;primaryKey;check:fleet_placement_fanout_scope,scope IN ('node','rule','client','default')"`
	ScopeID             string  `gorm:"size:128;primaryKey"`
	Revision            int64   `gorm:"not null;check:fleet_placement_fanout_revision,revision > 0"`
	Cursor              string  `gorm:"type:jsonb;not null"`
	Pending             bool    `gorm:"not null"`
	AvailableUnixMilli  int64   `gorm:"not null;index:idx_fleet_placement_fanout_due,where:pending"`
	LeaseID             *string `gorm:"type:uuid"`
	LeaseUntilUnixMilli int64   `gorm:"not null;default:0"`
}
