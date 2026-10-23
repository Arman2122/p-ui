package enforcement

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/xtls/xray-core/common/session"
)

type ClientIdentity struct {
	InboundTag string `json:"inboundTag"`
	Username   string `json:"username"`
	ClientID   string `json:"clientId"`
}

type (
	identityKey           struct{}
	normalizedIdentityKey struct{}
	normalizedIdentity    struct {
		dispatcher     *guardedDispatcher
		client         string
		authentication *authentication
	}
)

func (d *guardedDispatcher) identityContext(ctx context.Context, client string, authenticated *authentication) context.Context {
	inbound := session.InboundFromContext(ctx)
	copyInbound, copyUser := *inbound, *inbound.User
	copyUser.Email = client
	copyInbound.User = &copyUser
	ctx = session.ContextWithInbound(ctx, &copyInbound)
	return context.WithValue(ctx, normalizedIdentityKey{}, normalizedIdentity{dispatcher: d, client: client, authentication: authenticated})
}

func WithClientIdentities(ctx context.Context, identities []ClientIdentity) (context.Context, error) {
	if len(identities) > 1_000_000 {
		return nil, errors.New("too many Xray client identities")
	}
	bindings := make(map[string]map[string]string)
	for _, identity := range identities {
		id, err := uuid.Parse(identity.ClientID)
		if err != nil || id == uuid.Nil || id.String() != identity.ClientID || identity.InboundTag == "" || len(identity.InboundTag) > 128 || identity.Username == "" || len(identity.Username) > 256 {
			return nil, errors.New("invalid Xray client identity")
		}
		if bindings[identity.InboundTag] == nil {
			bindings[identity.InboundTag] = make(map[string]string)
		}
		if _, exists := bindings[identity.InboundTag][identity.Username]; exists {
			return nil, errors.New("duplicate Xray client identity")
		}
		bindings[identity.InboundTag][identity.Username] = identity.ClientID
	}
	return context.WithValue(ctx, identityKey{}, bindings), nil
}
