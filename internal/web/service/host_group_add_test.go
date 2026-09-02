package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

/*
POST /panel/api/hosts/add takes groupId from the body. Naming a group that
already exists appended the new rows INTO it instead of forming a new group,
silently, and the next edit of that group rewrote both sets together. The
panel's own form never sends one; an API caller can, and did. F149.
*/
func TestAddHostGroupRefusesAGroupThatAlreadyExists(t *testing.T) {
	setupBulkDB(t)
	svc := &HostService{}
	ib := mkInbound(t, 46002, model.VLESS, `{"clients":[]}`)

	first := &entity.HostGroup{GroupId: "fixed-group", InboundIds: []int{ib.Id}, Remark: "one", Hosts: []string{"one.example.com"}, Port: 8443}
	if _, err := svc.AddHostGroup(first); err != nil {
		t.Fatalf("first add: %v", err)
	}

	second := &entity.HostGroup{GroupId: "fixed-group", InboundIds: []int{ib.Id}, Remark: "two", Hosts: []string{"two.example.com"}, Port: 8443}
	_, err := svc.AddHostGroup(second)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second add with the same groupId: err = %v, want a refusal naming the group", err)
	}

	var rows int64
	if err := database.GetDB().Model(&model.Host{}).Where("group_id = ?", "fixed-group").Count(&rows).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("group carries %d rows, want the first add's 1: the second was merged in", rows)
	}
}
