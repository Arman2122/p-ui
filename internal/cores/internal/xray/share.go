package xray

import (
	"errors"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/util/sharelink"
)

func (*Core) RenderClient(inst core.Instance, user core.User, host string) (core.Share, error) {
	if strings.TrimSpace(host) == "" || inst.Port < 1 || inst.Port > 65535 || user.Email == "" {
		return core.Share{}, errors.New("client share requires an explicit endpoint and identity")
	}
	key := core.CredString(user.Credentials, "subId")
	if key == "" {
		key = user.Email
	}
	template, err := sharelink.PrepareXray(inst, user, key)
	if err != nil {
		return core.Share{}, err
	}
	return core.Share{Kind: "link", URI: template.Render(host, inst.Port, inst.Tag+"-"+user.Email)}, nil
}

func (*Core) SharesFile(core.Kind) bool { return false }
