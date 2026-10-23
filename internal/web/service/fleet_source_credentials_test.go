package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

func sourceCipherTemplates() []model.Inbound {
	return []model.Inbound{
		{Id: 1, Protocol: model.Shadowsocks, Settings: `{"method":"2022-blake3-aes-128-gcm"}`},
		{Id: 2, Protocol: model.Shadowsocks, Settings: `{"method":"2022-blake3-aes-256-gcm"}`},
	}
}

func TestFleetSourceCredentialPreparationProjectsIncompatibleCipherKeysPerBinding(t *testing.T) {
	inbounds := sourceCipherTemplates()
	prepared, err := prepareFleetSourceCredentials(model.Client{Email: "cipher-client", Enable: true}, inbounds, nil)
	if err != nil || prepared.Client.Password != "" || !fleetID(prepared.Client.ID) {
		t.Fatalf("source credential preparation=%+v err=%v", prepared.Client, err)
	}
	rows := fleetSourceCredentialRows(7, prepared)
	if len(rows) != 2 {
		t.Fatalf("per-binding keys were lost or duplicated: %d", len(rows))
	}
	snapshot := fleetClientSnapshot{Client: *prepared.Client.ToRecord(), Credentials: rows}
	for index, inbound := range inbounds {
		user, err := fleetNetworkUser(snapshot, inbound.Id)
		if err != nil {
			t.Fatal(err)
		}
		password, ok := user.Credentials[core.CredPassword].(string)
		if !ok {
			t.Fatal("binding credential did not reach node user projection")
		}
		key, err := base64.StdEncoding.DecodeString(password)
		if err != nil || len(key) != []int{16, 32}[index] {
			t.Fatalf("node binding used another cipher's key: inbound=%d size=%d err=%v", inbound.Id, len(key), err)
		}
		authority, _ := cores.SourceClientCredentialAuthority(core.Kind(inbound.Protocol))
		if err := authority.ValidateSourceClientCredentials(core.Kind(inbound.Protocol), inbound.Settings, prepared.Client.Email, map[string]string{core.CredPassword: password}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFleetSourceCredentialPreparationRefusesSilentRotationAndPreservesExplicitOverrides(t *testing.T) {
	password := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	client := model.Client{Email: "explicit-cipher", Password: password, Enable: true}
	inbounds := sourceCipherTemplates()
	if _, err := prepareFleetSourceCredentials(client, inbounds, nil); !errors.Is(err, ErrFleetSourceCredentials) || !errors.Is(err, core.ErrSourceCredentialConflict) {
		t.Fatalf("incompatible supplied password was silently rotated=%v", err)
	}
	second := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	bindings := []entity.ClientCredentialBindingInput{{InboundID: 2, Values: map[string]string{core.CredPassword: second}}}
	before, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareFleetSourceCredentials(client, inbounds, bindings)
	if err != nil || prepared.Client.Password != password || prepared.Bindings[1][core.CredPassword] != password || prepared.Bindings[2][core.CredPassword] != second {
		t.Fatalf("explicit source credentials changed: err=%v", err)
	}
	after, err := json.Marshal(bindings)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("source normalization mutated the caller's credential request")
	}
	rows := fleetSourceCredentialRows(7, prepared)
	if len(rows) != 1 || rows[0].InboundId != 2 || rows[0].Key != core.CredPassword || rows[0].Value != second {
		t.Fatal("credential storage failed to retain only the explicit binding override")
	}
	for _, test := range []struct {
		name    string
		binding entity.ClientCredentialBindingInput
	}{
		{"foreign template", entity.ClientCredentialBindingInput{InboundID: 99, Values: map[string]string{core.CredPassword: second}}},
		{"quota is not a credential", entity.ClientCredentialBindingInput{InboundID: 2, Values: map[string]string{"totalGB": "0"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := inbounds
			if test.binding.InboundID == 99 {
				selected = inbounds[:1]
			}
			if _, err := prepareFleetSourceCredentials(client, selected, []entity.ClientCredentialBindingInput{test.binding}); !errors.Is(err, ErrFleetSourceCredentials) {
				t.Fatalf("out-of-scope binding accepted=%v", err)
			}
		})
	}
}
