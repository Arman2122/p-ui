package service

// BulkRow is one client a bulk operation did not carry out, and why.
type BulkRow struct {
	Email  string `json:"email" example:"user1"`
	Reason string `json:"reason" example:"client not found"`
}

// BulkOutcome is the per-row account every bulk endpoint returns beside its
// legacy counts: who it changed, who it left alone, who it could not change.
type BulkOutcome struct {
	OK      []string  `json:"ok"`
	Skipped []BulkRow `json:"skipped"`
	Failed  []BulkRow `json:"failed"`
}

func (o *BulkOutcome) ok(email string) {
	o.OK = append(o.OK, email)
}

func (o *BulkOutcome) skip(email, reason string) {
	o.Skipped = append(o.Skipped, BulkRow{Email: email, Reason: reason})
}

func (o *BulkOutcome) fail(email, reason string) {
	o.Failed = append(o.Failed, BulkRow{Email: email, Reason: reason})
}

func (o *BulkOutcome) failEach(emails []string, reason string) {
	for _, email := range emails {
		o.fail(email, reason)
	}
}

// finalize makes the slices non-nil so the JSON carries [] rather than null.
func (o *BulkOutcome) finalize() {
	if o.OK == nil {
		o.OK = []string{}
	}
	if o.Skipped == nil {
		o.Skipped = []BulkRow{}
	}
	if o.Failed == nil {
		o.Failed = []BulkRow{}
	}
}
