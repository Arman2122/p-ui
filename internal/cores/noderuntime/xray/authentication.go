package xrayruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/proxy/http"
	hysteriaaccount "github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/socks"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"google.golang.org/protobuf/proto"

	"github.com/Arman2122/p-ui/internal/node/admission"
)

type userConfiguration struct {
	user    *protocol.MemoryUser
	subject admission.Subject
	proof   *admission.BoundProof
	scoped  bool
}

func authenticationCredential(account protocol.Account) (string, error) {
	switch account := account.(type) {
	case *http.Account:
		return account.Username + "\x00" + account.Password, nil
	case *vless.MemoryAccount:
		id := vless.ProcessUUID(account.ID.UUID())
		return string(id[:]), nil
	case *vmess.MemoryAccount:
		id := account.ID.UUID()
		return string(id[:]), nil
	case *trojan.MemoryAccount:
		return string(account.Key), nil
	case *shadowsocks.MemoryAccount:
		return string(account.Key), nil
	case *shadowsocks_2022.MemoryAccount:
		return account.Key, nil
	case *hysteriaaccount.MemoryAccount:
		if id, err := uuid.Parse(account.Auth); err == nil {
			id[6], id[7] = 0, 0
			return string(id[:]), nil
		}
		return account.Auth, nil
	default:
		return "", errors.New("Xray authentication account is not qualified")
	}
}

func authenticationConfiguration(ctx context.Context, config *core.Config, identities []ClientIdentity) (map[string][]userConfiguration, *core.Config, error) {
	bindings := make(map[string]string, len(identities))
	for _, identity := range identities {
		bindings[identity.InboundTag+"\x00"+identity.Username] = identity.ClientID
	}
	binding := admission.BindingFromContext(ctx)
	structure := proto.Clone(config).(*core.Config)
	result := make(map[string][]userConfiguration, len(config.Inbound))
	for _, handler := range structure.Inbound {
		settings, err := handler.ProxySettings.GetInstance()
		if err != nil {
			return nil, nil, err
		}
		var users []*protocol.User
		switch settings := settings.(type) {
		case *http.ServerConfig:
			for name, password := range settings.Accounts {
				users = append(users, &protocol.User{Email: name, Level: settings.UserLevel, Account: serial.ToTypedMessage(&http.Account{Username: name, Password: password})})
			}
		case *socks.ServerConfig:
			if settings.AuthType != socks.AuthType_PASSWORD {
				return nil, nil, errors.New("governed Xray SOCKS requires password authentication")
			}
			for name, password := range settings.Accounts {
				users = append(users, &protocol.User{Email: name, Level: settings.UserLevel, Account: serial.ToTypedMessage(&http.Account{Username: name, Password: password})})
			}
		case interface{ GetUsers() []*protocol.User }:
			users = settings.GetUsers()
		case interface{ GetUser() []*protocol.User }:
			users = settings.GetUser()
		default:
			return nil, nil, errors.New("Xray inbound has no qualified immutable authentication")
		}
		message := settings.ProtoReflect()
		for _, name := range []string{"users", "user", "accounts"} {
			field := message.Descriptor().Fields().ByJSONName(name)
			if field != nil {
				message.Clear(field)
			}
		}
		handler.ProxySettings = serial.ToTypedMessage(settings)
		seen := make(map[string]bool, len(users))
		credentials := make(map[string]bool, len(users))
		for _, configured := range users {
			user, err := configured.ToMemoryUser()
			if err != nil {
				return nil, nil, err
			}
			name := user.Email
			if _, exact := user.Account.(*http.Account); !exact {
				name = strings.ToLower(name)
			}
			if user.Email == "" || seen[name] {
				return nil, nil, errors.New("duplicate or empty Xray authentication identity")
			}
			seen[name] = true
			credential, err := authenticationCredential(user.Account)
			if err != nil || credentials[credential] {
				return nil, nil, errors.New("duplicate Xray authentication credential")
			}
			credentials[credential] = true
			client := bindings[handler.Tag+"\x00"+user.Email]
			if client == "" {
				client = user.Email
			}
			proof, found := binding.Capture(client, handler.Tag)
			if binding != nil && !found {
				return nil, nil, admission.ErrUnavailable
			}
			result[handler.Tag] = append(result[handler.Tag], userConfiguration{user: user, subject: admission.Subject{ClientID: client, InboundTag: handler.Tag}, proof: proof, scoped: binding != nil})
		}
		if result[handler.Tag] == nil {
			result[handler.Tag] = []userConfiguration{}
		}
	}
	return result, structure, nil
}

func userManager(ctx context.Context, instance *core.Instance, tag string) (proxy.UserManager, error) {
	manager := instance.GetFeature(inbound.ManagerType()).(inbound.Manager)
	handler, err := manager.GetHandler(ctx, tag)
	if err != nil {
		return nil, err
	}
	getter, ok := handler.(proxy.GetInbound)
	if !ok {
		return nil, errors.New("Xray inbound does not expose its authentication handler")
	}
	users, ok := getter.GetInbound().(proxy.UserManager)
	if !ok {
		return nil, errors.New("Xray inbound does not support immutable user updates")
	}
	if required, ok := users.(interface{ RequireAuthentication() }); ok {
		required.RequireAuthentication()
	}
	return users, nil
}

func (x *Runtime) registerAuthentication(ctx context.Context, desired map[string][]userConfiguration) error {
	for tag, users := range desired {
		manager, err := userManager(ctx, x.instance, tag)
		if err != nil {
			return err
		}
		for _, configured := range users {
			user := manager.GetUser(ctx, configured.user.Email)
			if user == nil {
				return fmt.Errorf("Xray authentication missing for inbound %q", tag)
			}
			if err := x.authentication.Register(user, configured.subject, configured.proof, configured.scoped); err != nil {
				return err
			}
		}
	}
	return nil
}

func (x *Runtime) updateAuthentication(ctx context.Context, desired map[string][]userConfiguration) error {
	for tag, users := range desired {
		manager, err := userManager(ctx, x.instance, tag)
		if err != nil {
			return err
		}
		wanted := make(map[string]userConfiguration, len(users))
		for _, configured := range users {
			wanted[configured.user.Email] = configured
		}
		for _, old := range manager.GetUsers(ctx) {
			next, exists := wanted[old.Email]
			if exists && proto.Equal(protocol.ToProtoUser(old), protocol.ToProtoUser(next.user)) && x.authentication.Matches(old, next.subject, next.proof, next.scoped) {
				delete(wanted, old.Email)
				continue
			}
			x.authentication.RetireUsers(old)
			if err := manager.RemoveUser(ctx, old.Email); err != nil {
				return err
			}
		}
		for _, next := range wanted {
			if err := x.authentication.Register(next.user, next.subject, next.proof, next.scoped); err != nil {
				return err
			}
			if err := manager.AddUser(ctx, next.user); err != nil {
				x.authentication.RetireUsers(next.user)
				return err
			}
		}
	}
	return nil
}

func (x *Runtime) InvalidateAuthentication(ctx context.Context, subjects []admission.Subject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if x.authentication != nil {
		x.authentication.Retire(subjects)
	}
	return nil
}
