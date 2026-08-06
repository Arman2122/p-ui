package arch

import (
	"sort"
	"testing"
)

/*
The clients table must stop growing a column per protocol.

ClientRecord is still a union of Xray's protocol credentials — uuid, password,
auth, four WireGuard columns — and WireGuard alone cost five. The columns are not
the real cost; the hand-written per-field code is. MergeClientRecord is one
branch per field and runs on the node-sync path, so a field forgotten there does
not error: the merge drops it and the client works on the master but not on the
node. ToRecord and ToClient repeat the mapping twice more.

Per-core credentials belong in the client_credentials side table keyed by
(client_id, inbound_id, key); MTProto's secret and ad_tag already moved there.
This test freezes the remaining column set so adding `ocserv_password` requires
deleting a line here — which is the review signal the whole design depends on.
*/

// frozenClientRecordFields is the ClientRecord field set as measured. Adding a
// per-core credential field here is the thing this guard exists to stop.
var frozenClientRecordFields = []string{
	"AllowedIPs",
	"Auth",
	"Comment",
	"CreatedAt",
	"Email",
	"Enable",
	"ExpiryTime",
	"Flow",
	"Group",
	"Id",
	"KeepAlive",
	"LimitIP",
	"Password",
	"PreSharedKey",
	"PrivateKey",
	"PublicKey",
	"Reset",
	"Reverse",
	"Security",
	"SubID",
	"SyncOrphanedAt",
	"TgID",
	"TotalGB",
	"UUID",
	"UpdatedAt",
}

func TestClientRecordColumnsAreFrozen(t *testing.T) {
	root := repoRoot(t)
	got := structFields(t, root, "ClientRecord")
	sort.Strings(got)

	want := make([]string, len(frozenClientRecordFields))
	copy(want, frozenClientRecordFields)
	sort.Strings(want)

	wantSet := make(map[string]bool, len(want))
	for _, f := range want {
		wantSet[f] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, f := range got {
		gotSet[f] = true
	}

	for _, f := range got {
		if !wantSet[f] {
			t.Errorf("ClientRecord gained field %q — per-core credentials belong in the client_credentials side table keyed by (client_id, inbound_id, key), not in a column on every client; see docs/multi-core-architecture.md §8", f)
		}
	}
	for _, f := range want {
		if !gotSet[f] {
			t.Errorf("ClientRecord lost field %q — remove it from frozenClientRecordFields in this PR so the freeze keeps describing the real table", f)
		}
	}
}
