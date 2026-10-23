package authority

import "context"

const MaxEntries = 1024

const MaxCreditUpdates = 128

type Credit struct {
	GrantID        string
	ClientID       string
	TermID         string
	PolicyRevision uint64
	CreditRevision uint64
	CreditCeiling  uint64
}

type Grant struct {
	GrantID             string
	ClientID            string
	TermID              string
	PolicyRevision      uint64
	AuthorityRevision   uint64
	CreditCeiling       uint64
	QuotaUnlimited      bool
	ReservedHighWater   uint64
	BurnedHighWater     uint64
	AdmissionHighWater  uint64
	AdmissionKnown      bool
	SpendableBytes      uint64
	SpendableKnown      bool
	AuthorizationActive bool
}

type Page struct {
	Grants []Grant
	Next   string
}

type Source interface {
	AuthorityJournalID() string
	Authority(context.Context, string, int) (Page, error)
}
