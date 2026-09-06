package model

/*
ClientBan is one device over its cap, dropped on the firewall of the host that
serves the client until Until. Active rows only: the row is the memory that the
pair is banned and the source the firewall set is projected from, and it leaves
when its deadline passes or an operator releases it.

NodeGuid is "" for a ban this host holds itself. Ports is the CSV of local
inbound ports the ban covers, or "*" for every local inbound port, resolved
each pass, which is what an operator's own ban of an address means.
*/
type ClientBan struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	ClientEmail string `json:"clientEmail" gorm:"uniqueIndex:idx_client_bans_pair,priority:1;not null;default:''"`
	Source      string `json:"source" gorm:"uniqueIndex:idx_client_bans_pair,priority:2;not null"`
	NodeGuid    string `json:"nodeGuid" gorm:"uniqueIndex:idx_client_bans_pair,priority:3;not null;default:''"`
	Ports       string `json:"ports" gorm:"not null;default:''"`
	Reason      string `json:"reason" gorm:"not null;default:''"`
	// Unix seconds, the unit every synced timestamp in this panel carries.
	CreatedAt int64 `json:"createdAt"`
	Until     int64 `json:"until" gorm:"index"`
}
