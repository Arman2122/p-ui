package singbox

import (
	"context"
	"encoding/json"
	"errors"

	anytls "github.com/anytls/sing-anytls"
	"github.com/google/uuid"
	"github.com/sagernet/sing-box/option"
	anytlsin "github.com/sagernet/sing-box/protocol/anytls"
	tuicin "github.com/sagernet/sing-box/protocol/tuic"
	"github.com/sagernet/sing-quic/tuic"

	"github.com/Arman2122/p-ui/internal/node/admission"
)

func managedAuthentication(data []byte, options option.Options) (map[string][]any, []byte, error) {
	if anytls.PenhoonAuthenticationPatchLevel != 1 || tuic.PenhoonAuthenticationPatchLevel != 1 {
		return nil, nil, errors.New("unsupported sing-box authentication patches")
	}
	desired := make(map[string][]any, len(options.Inbounds))
	for _, inbound := range options.Inbounds {
		if inbound.Tag == "" || desired[inbound.Tag] != nil {
			return nil, nil, errors.New("invalid sing-box authentication inbound tag")
		}
		desired[inbound.Tag] = []any{}
		names, credentials := make(map[string]bool), make(map[string]bool)
		switch settings := inbound.Options.(type) {
		case *option.TUICInboundOptions:
			for _, user := range settings.Users {
				id, err := uuid.Parse(user.UUID)
				if err != nil || id == uuid.Nil || user.Name == "" || user.Password == "" || names[user.Name] || credentials[id.String()] {
					return nil, nil, errors.New("duplicate or invalid TUIC authentication")
				}
				names[user.Name], credentials[id.String()] = true, true
				record := user
				desired[inbound.Tag] = append(desired[inbound.Tag], &record)
			}
		case *option.AnyTLSInboundOptions:
			for _, user := range settings.Users {
				if user.Name == "" || user.Password == "" || names[user.Name] || credentials[user.Password] {
					return nil, nil, errors.New("duplicate or invalid AnyTLS authentication")
				}
				names[user.Name], credentials[user.Password] = true, true
				desired[inbound.Tag] = append(desired[inbound.Tag], &anytls.User{Name: user.Name, Password: user.Password})
			}
		default:
			return nil, nil, errors.New("sing-box inbound has no qualified immutable authentication")
		}
	}
	var graph map[string]json.RawMessage
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, nil, err
	}
	var inbounds []map[string]json.RawMessage
	if len(graph["inbounds"]) != 0 {
		if err := json.Unmarshal(graph["inbounds"], &inbounds); err != nil {
			return nil, nil, err
		}
	}
	for _, inbound := range inbounds {
		delete(inbound, "users")
	}
	structuralInbounds, err := json.Marshal(inbounds)
	if err != nil {
		return nil, nil, err
	}
	graph["inbounds"] = structuralInbounds
	structure, err := json.Marshal(graph)
	return desired, structure, err
}

func (m *Managed) applyAuthentication(ctx context.Context, desired map[string][]any, initial bool) error {
	for tag, wanted := range desired {
		inbound, found := m.instance.Inbound().Get(tag)
		if !found {
			return errors.New("sing-box authentication inbound is missing")
		}
		var previous []any
		switch inbound := inbound.(type) {
		case *tuicin.Inbound:
			for _, user := range inbound.AuthenticationUsers() {
				previous = append(previous, user)
			}
		case *anytlsin.Inbound:
			for _, user := range inbound.AuthenticationUsers() {
				previous = append(previous, user)
			}
		default:
			return errors.New("sing-box inbound cannot publish immutable authentication")
		}
		next, err := m.authentication.reconcile(ctx, tag, previous, wanted, initial)
		if err != nil {
			return err
		}
		if initial {
			continue
		}
		switch inbound := inbound.(type) {
		case *tuicin.Inbound:
			users := make([]*option.TUICUser, 0, len(next))
			for _, user := range next {
				users = append(users, user.(*option.TUICUser))
			}
			err = inbound.SetAuthenticatedUsers(users)
		case *anytlsin.Inbound:
			users := make([]*anytls.User, 0, len(next))
			for _, user := range next {
				users = append(users, user.(*anytls.User))
			}
			err = inbound.SetAuthenticatedUsers(users)
		}
		if err != nil {
			retained := make(map[any]bool, len(previous))
			for _, old := range previous {
				retained[old] = true
			}
			for _, handle := range next {
				if !retained[handle] {
					m.authentication.retire(handle)
				}
			}
			return err
		}
	}
	return nil
}

func (m *Managed) InvalidateAuthentication(ctx context.Context, subjects []admission.Subject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.authentication != nil {
		m.authentication.invalidate(subjects)
	}
	return nil
}
