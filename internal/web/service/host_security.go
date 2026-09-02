package service

import (
	"encoding/json"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

func streamSecurityOf(streamSettings string) string {
	var stream map[string]any
	if json.Unmarshal([]byte(streamSettings), &stream) != nil {
		return ""
	}
	security, _ := stream["security"].(string)
	return security
}

// hostSecurityMismatch names the one override no front can honour: REALITY is
// terminated by Xray itself, so a host cannot add it, remove it, or swap it for TLS.
func hostSecurityMismatch(hostSecurity string, ib *model.Inbound) error {
	served := streamSecurityOf(ib.StreamSettings)
	switch hostSecurity {
	case "reality":
		if served != "reality" {
			return common.NewErrorf("host security reality cannot match inbound %q, which serves %s", ib.Tag, servedName(served))
		}
	case "tls", "none":
		if served == "reality" {
			return common.NewErrorf("host security %s cannot match inbound %q, which serves reality", hostSecurity, ib.Tag)
		}
	}
	return nil
}

func servedName(security string) string {
	if security == "" {
		return "none"
	}
	return security
}

func validateHostSecurityAgainstInbounds(tx *gorm.DB, hostSecurity string, inboundIds []int) error {
	if hostSecurity == "" || hostSecurity == "same" || len(inboundIds) == 0 {
		return nil
	}
	var inbounds []model.Inbound
	if err := tx.Where("id IN ?", inboundIds).Find(&inbounds).Error; err != nil {
		return err
	}
	for i := range inbounds {
		if err := hostSecurityMismatch(hostSecurity, &inbounds[i]); err != nil {
			return err
		}
	}
	return nil
}
