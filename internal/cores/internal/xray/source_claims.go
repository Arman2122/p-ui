package xray

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"github.com/google/uuid"
	"github.com/xtls/xray-core/common/protocol"
	xuuid "github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/proxy/vless"
	"lukechampine.com/blake3"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

func (c *Core) SourceClientCredentialClaims(kind core.Kind, settings, email string, have map[string]string) ([]core.SourceCredentialClaim, error) {
	if err := c.ValidateSourceClientCredentials(kind, settings, email, have); err != nil {
		return nil, err
	}
	var claim core.SourceCredentialClaim
	switch kind {
	case "vmess":
		id, _ := uuid.Parse(have[core.CredUUID])
		claim = core.NewSourceCredentialClaim("xray-vmess-command-key-v1", protocol.NewID(xuuid.UUID(id)).CmdKey())
	case "vless":
		id, _ := uuid.Parse(have[core.CredUUID])
		key := vless.ProcessUUID(id)
		claim = core.NewSourceCredentialClaim("xray-vless-uuid-v1", key[:])
	case "trojan":
		key := sha256.Sum224([]byte(have[core.CredPassword]))
		claim = core.NewSourceCredentialClaim("xray-trojan-sha224-v1", []byte(hex.EncodeToString(key[:])))
	case "shadowsocks":
		method := engine.ShadowsocksMethodFromSettings(settings)
		if engine.ShadowsocksKeyBytes(method) != 0 {
			key, _ := base64.StdEncoding.DecodeString(have[core.CredPassword])
			lookup := blake3.Sum512(key)
			claim = core.NewSourceCredentialClaim("xray-ss2022-identity-v1", lookup[:16])
		} else {
			key, err := engine.ShadowsocksClientIdentity(method, have[core.CredPassword])
			if err != nil {
				return nil, err
			}
			claim = core.NewSourceCredentialClaim("xray-ss-aead-identity-v1", key)
		}
	case "hysteria":
		if id, err := uuid.Parse(have[core.CredAuth]); err == nil {
			id[6], id[7] = 0, 0
			claim = core.NewSourceCredentialClaim("xray-hysteria-uuid-v1", id[:])
		} else {
			claim = core.NewSourceCredentialClaim("xray-hysteria-auth-v1", []byte(have[core.CredAuth]))
		}
	case "http", "mixed":
		username := have[core.CredUsername]
		if username == "" {
			username = email
		}
		claim = core.NewSourceCredentialClaim("xray-account-username-v1", []byte(username))
	default:
		return nil, core.ErrKindHasNoUsers
	}
	return []core.SourceCredentialClaim{claim}, nil
}
