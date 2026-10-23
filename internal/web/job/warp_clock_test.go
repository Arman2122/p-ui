package job

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func TestWarpJobPreservesUnreadableClock(t *testing.T) {
	initTestDB(t)
	if err := (&service.SettingService{}).SetWarpUpdateInterval(1); err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.Setting{Key: "warpLastUpdate", Value: "corrupt"}).Error; err != nil {
		t.Fatal(err)
	}
	NewWarpIpJob().Run()
	var setting model.Setting
	if err := database.GetDB().Where("key = ?", "warpLastUpdate").First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if setting.Value != "corrupt" {
		t.Fatalf("failed clock read was treated as first run and overwritten: %q", setting.Value)
	}
}
