package xray

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-shadowsocks/shadowaead_2022"
	"github.com/sagernet/sing/common/auth"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/Arman2122/p-ui/internal/core"
)

type sourceClaimSSHandler struct{ users chan string }

//nolint:staticcheck // The pinned Shadowsocks service requires this deprecated handler signature.
func (h sourceClaimSSHandler) NewConnection(ctx context.Context, _ net.Conn, _ M.Metadata) error {
	user, found := auth.UserFromContext[string](ctx)
	if !found {
		return errors.New("missing authenticated Shadowsocks user")
	}
	h.users <- user
	return nil
}

//nolint:staticcheck // The pinned Shadowsocks service requires this deprecated handler signature.
func (sourceClaimSSHandler) NewPacketConnection(context.Context, N.PacketConn, M.Metadata) error {
	return errors.New("unexpected packet connection")
}

func (sourceClaimSSHandler) NewError(context.Context, error) {}

func TestSourceClaimsSS2022AliasesMatchActualAuthentication(t *testing.T) {
	for _, size := range []int{16, 32} {
		t.Run(fmt.Sprintf("key-%d", size), func(t *testing.T) {
			method := fmt.Sprintf("2022-blake3-aes-%d-gcm", size*8)
			serverKey, clientKey := bytes.Repeat([]byte{0x22}, size), bytes.Repeat([]byte{0x11}, size)
			password := base64.StdEncoding.EncodeToString(clientKey)
			alias := password[:4] + "\r\n" + password[4:]
			users := make(chan string, 1)
			service, err := shadowaead_2022.NewMultiService[string](method, serverKey, 500, sourceClaimSSHandler{users: users}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.UpdateUsersWithPasswords([]string{"original-owner", "alias-owner"}, []string{password, alias}); err != nil {
				t.Fatal(err)
			}
			client, err := shadowaead_2022.New(method, [][]byte{serverKey, clientKey}, nil)
			if err != nil {
				t.Fatal(err)
			}
			server, peer := net.Pipe()
			defer server.Close()
			defer peer.Close()
			deadline := time.Now().Add(3 * time.Second)
			_ = server.SetDeadline(deadline)
			_ = peer.SetDeadline(deadline)
			done := make(chan error, 1)
			go func() { done <- service.NewConnection(t.Context(), server, M.Metadata{}) }() //nolint:staticcheck // Required by the pinned service API.
			if _, err := client.DialConn(peer, M.ParseSocksaddr("example.test:443")); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			select {
			case user := <-users:
				if user != "alias-owner" {
					t.Fatalf("pinned service lookup=%s, expected the shared authentication key", user)
				}
			default:
				t.Fatal("no authenticated user reported")
			}
			settings := fmt.Sprintf(`{"method":%q}`, method)
			first := sourceClaim(t, "shadowsocks", settings, map[string]string{core.CredPassword: password})
			second := sourceClaim(t, "shadowsocks", settings, map[string]string{core.CredPassword: alias})
			if first != second {
				t.Fatal("wire-equivalent SS2022 keys reserved different identities")
			}
			other := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, size))
			if sourceClaim(t, "shadowsocks", settings, map[string]string{core.CredPassword: other}) == first {
				t.Fatal("different SS2022 key collided")
			}
		})
	}
}
