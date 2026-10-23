package singbox

import (
	"encoding/json"

	"github.com/Arman2122/p-ui/internal/core"
)

func NodeSettings(kind core.Kind, settings Settings) (Settings, error) {
	settings.CertFile, settings.KeyFile = "node-certificate", "node-private-key"
	data, err := json.Marshal(settings)
	if err != nil {
		return Settings{}, err
	}
	return parseSettings(kind, string(data))
}

func NodeClient(kind core.Kind, user core.User) (map[string]any, error) {
	credential, err := credentialOf(kind, user)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"name": user.Email, "password": credential.password}
	if kind == KindTUIC {
		result["uuid"] = credential.uuid
	}
	return result, nil
}
