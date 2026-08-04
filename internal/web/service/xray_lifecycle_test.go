package service

import (
	"sync"
	"testing"

	"github.com/Arman2122/p-ui/internal/xray"
)

func TestXrayLifecycleConcurrentStatusResultAndTrafficReads(t *testing.T) {
	previousProcess, previousResult := xrayState().Snapshot()
	t.Cleanup(func() {
		xrayState().Replace(previousProcess)
		xrayState().StoreResult(previousProcess, previousResult)
	})

	first := xray.NewProcess(&xray.Config{})
	second := xray.NewProcess(&xray.Config{})
	service := XrayService{}
	var wg sync.WaitGroup

	wg.Go(func() {
		for range 200 {
			xrayState().Replace(first)
			xrayState().Replace(second)
		}
	})

	for range 4 {
		wg.Go(func() {
			for range 200 {
				_ = service.IsXrayRunning()
				_ = service.GetXrayResult()
				_, _, err := service.GetXrayTraffic()
				if err == nil || err.Error() != "xray is not running" {
					t.Errorf("GetXrayTraffic error = %v, want xray is not running", err)
				}
			}
		})
	}

	wg.Wait()
}
