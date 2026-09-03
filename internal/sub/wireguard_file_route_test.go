package sub

import (
	"reflect"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
The subscription page assembled a WireGuard .conf from the share link in the
browser, while the sub server already renders the real file (hosts, AmneziaWG
parameters, the core's own view) under /file/<inbound id>. The page now gets
that URL for every file-shaped entry, in parallel with the links. Feature gap B3.
*/
func TestBuildPageDataCarriesTheServedFileURL(t *testing.T) {
	s := &SubService{}
	subs := []string{
		"wg://k@h1:51820#DE-john@x\nwg://k@h2:51820#DE-john@x",
		"vless://b@h:443?type=tcp#FR-john@x",
	}
	page := s.BuildPageData("s1", "", xray.ClientTraffic{}, 0, subs, []string{"john@x", "john@x"}, []int{7, 0},
		"https://h/sub/s1/", "", "", "/", "", "")

	want := []string{"https://h/sub/s1/file/7", "https://h/sub/s1/file/7", ""}
	if !reflect.DeepEqual(page.FileUrls, want) {
		t.Fatalf("FileUrls = %q, want %q (one per rendered link, empty for a link-only kind)", page.FileUrls, want)
	}
}

func TestFileShapedInboundId(t *testing.T) {
	for _, tt := range []struct {
		protocol model.Protocol
		want     int
	}{
		// Xray's own wireguard renders no file: the registry, not a protocol list, says so.
		{model.WGKernel, 9}, {model.AWGKernel, 9}, {model.WireGuard, 0}, {model.VLESS, 0}, {model.MTProto, 0},
	} {
		if got := fileShapedInboundId(&model.Inbound{Id: 9, Protocol: tt.protocol}); got != tt.want {
			t.Errorf("fileShapedInboundId(%s) = %d, want %d", tt.protocol, got, tt.want)
		}
	}
}
