package xrayruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/Arman2122/p-ui/internal/core"
	xraycore "github.com/Arman2122/p-ui/internal/cores/internal/xray"
	"github.com/Arman2122/p-ui/internal/cores/internal/xray/enforcement"
)

type ClientIdentity = enforcement.ClientIdentity

type Metadata struct {
	Version          uint32           `json:"version"`
	ClientIdentities []ClientIdentity `json:"clientIdentities"`
}

func configurationIdentities(data []byte) ([]ClientIdentity, error) {
	var config struct {
		Metadata json.RawMessage `json:"penhoon"`
		Inbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			Settings struct {
				Accounts []struct {
					User string `json:"user"`
				} `json:"accounts"`
				Users []struct {
					User  string `json:"user"`
					Email string `json:"email"`
				} `json:"users"`
				Clients []struct {
					Email string `json:"email"`
				} `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if len(data) > 32<<20 || json.Unmarshal(data, &config) != nil {
		return nil, errors.New("invalid Xray configuration")
	}
	if len(config.Metadata) == 0 {
		return nil, nil
	}
	var metadata Metadata
	decoder := json.NewDecoder(bytes.NewReader(config.Metadata))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&metadata) != nil || decoder.Decode(new(any)) != io.EOF || metadata.Version != 1 {
		return nil, errors.New("invalid Xray node metadata")
	}
	if _, err := enforcement.WithClientIdentities(context.Background(), metadata.ClientIdentities); err != nil {
		return nil, err
	}
	users := make(map[string]map[string]bool)
	for _, inbound := range config.Inbounds {
		if _, duplicate := users[inbound.Tag]; duplicate {
			return nil, errors.New("duplicate Xray inbound tag")
		}
		users[inbound.Tag] = make(map[string]bool)
		var names []string
		if xraycore.UsesAccountUsernames(core.Kind(inbound.Protocol)) {
			if inbound.Settings.Accounts != nil {
				for _, account := range inbound.Settings.Accounts {
					names = append(names, account.User)
				}
			} else {
				for _, account := range inbound.Settings.Users {
					names = append(names, account.User)
				}
			}
		} else {
			if inbound.Settings.Clients != nil {
				for _, account := range inbound.Settings.Clients {
					names = append(names, account.Email)
				}
			} else {
				for _, account := range inbound.Settings.Users {
					names = append(names, account.Email)
				}
			}
		}
		for _, name := range names {
			if name == "" || users[inbound.Tag][name] {
				return nil, errors.New("invalid Xray authenticated username")
			}
			users[inbound.Tag][name] = true
		}
	}
	for _, identity := range metadata.ClientIdentities {
		if !users[identity.InboundTag][identity.Username] {
			return nil, errors.New("Xray identity does not match an authenticated account")
		}
		delete(users[identity.InboundTag], identity.Username)
	}
	for _, unbound := range users {
		if len(unbound) != 0 {
			return nil, errors.New("Xray authenticated account has no client identity")
		}
	}
	return metadata.ClientIdentities, nil
}
