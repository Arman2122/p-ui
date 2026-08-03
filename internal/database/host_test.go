package database

import (
	"testing"

	"github.com/Arman2122/p-ui/v3/internal/database/model"
)

func hostColumns() []string {
	return []string{
		"id", "group_id", "inbound_id", "sort_order", "remark", "server_description", "is_disabled", "is_hidden", "tags",
		"address", "port",
		"security", "sni", "host_header", "path", "alpn", "fingerprint",
		"override_sni_from_address", "keep_sni_blank", "pinned_peer_cert_sha256",
		"verify_peer_cert_by_name", "allow_insecure", "ech_config_list",
		"mux_params", "sockopt_params", "final_mask", "vless_route",
		"exclude_from_sub_types", "mihomo_ip_version", "mihomo_x25519", "shuffle_host", "node_guids",
		"created_at", "updated_at",
	}
}

func assertHostSchema(t *testing.T) {
	t.Helper()
	m := GetDB().Migrator()
	if !m.HasTable("hosts") {
		t.Fatalf("hosts table not created by initModels")
	}
	for _, col := range hostColumns() {
		if !m.HasColumn(&model.Host{}, col) {
			t.Fatalf("hosts table missing column %q", col)
		}
	}
}

// TestHostAutoMigrateCreatesColumns verifies the hosts table and every expected
// column exist after initModels.
func TestHostAutoMigrateCreatesColumns(t *testing.T) {
	initTestDB(t)
	assertHostSchema(t)
}

// TestPruneOrphanedHosts verifies a host whose inbound_id has no matching inbound
// is removed by the prune step.
func TestPruneOrphanedHosts(t *testing.T) {
	initTestDB(t)
	db := GetDB()

	orphan := &model.Host{InboundId: 99999, Remark: "orphan"}
	if err := db.Create(orphan).Error; err != nil {
		t.Fatalf("create orphan host: %v", err)
	}
	if err := pruneOrphanedHosts(); err != nil {
		t.Fatalf("pruneOrphanedHosts: %v", err)
	}
	var cnt int64
	if err := db.Model(&model.Host{}).Where("id = ?", orphan.Id).Count(&cnt).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("orphan host not pruned, count=%d", cnt)
	}
}
