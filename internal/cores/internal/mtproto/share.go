package mtproto

import (
	"errors"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/util/sharelink"
)

func (*Core) RenderClient(inst core.Instance, user core.User, host string) (core.Share, error) {
	secret := core.CredString(user.Credentials, core.CredSecret)
	if inst.Kind != Kind || strings.TrimSpace(host) == "" || inst.Port < 1 || inst.Port > 65535 || secret == "" {
		return core.Share{}, errors.New("Telegram share requires an explicit endpoint and client secret")
	}
	return core.Share{Kind: "link", URI: sharelink.MTProto(host, inst.Port, secret)}, nil
}

func (*Core) SharesFile(core.Kind) bool { return false }
