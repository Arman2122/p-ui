package mtproto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arman2122/mtg-proxy/antireplay"
	"github.com/Arman2122/mtg-proxy/events"
	"github.com/Arman2122/mtg-proxy/ipblocklist"
	"github.com/Arman2122/mtg-proxy/logger"
	"github.com/Arman2122/mtg-proxy/mtglib"
	network "github.com/Arman2122/mtg-proxy/network/v2"
	"github.com/google/uuid"
	proxyproto "github.com/pires/go-proxyproto"
	"github.com/rs/zerolog"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type (
	Listener      = configuration.MTProtoListener
	Client        = configuration.MTProtoClient
	Inbound       = configuration.MTProtoInbound
	Configuration = configuration.MTProto
)

type running struct {
	proxy     *mtglib.Proxy
	listeners []net.Listener
	done      sync.WaitGroup
	structure [32]byte
	web       *webRuntime
	exited    atomic.Bool
}

type Managed struct {
	instances map[string]*running
	stateDir  string
	network   mtglib.Network
	store     *state.Store
}

func (m *Managed) SetStateDirectory(directory string) { m.stateDir = directory }
func (m *Managed) SetStateStore(directory string, store *state.Store) {
	m.stateDir, m.store = directory, store
}

type allowAll struct{ mtglib.IPBlocklist }

func (allowAll) Contains(net.IP) bool { return true }

func configurationOf(data []byte) (Configuration, error) {
	var config Configuration
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, errors.New("invalid MTProto runtime configuration")
	}
	if decoder.Decode(new(any)) != io.EOF || len(config.Inbounds) > 10000 {
		return config, errors.New("invalid MTProto runtime configuration")
	}
	seen := make(map[string]bool)
	binds := make(map[string]bool)
	for _, inbound := range config.Inbounds {
		if inbound.ID == "" || len(inbound.ID) > 128 || seen[inbound.ID] || len(inbound.Listeners) == 0 || len(inbound.Listeners) > 8 ||
			len(inbound.Clients) == 0 || inbound.Concurrency > 1000000 || inbound.ThrottleMaxConnections < 0 || inbound.FrontingPort < 0 || inbound.FrontingPort > 65535 ||
			inbound.AdvertisedPort < 0 || inbound.AdvertisedPort > 65535 {
			return config, errors.New("invalid MTProto inbound bounds")
		}
		seen[inbound.ID] = true
		if inbound.PreferIP != "" && inbound.PreferIP != "prefer-ipv4" && inbound.PreferIP != "prefer-ipv6" && inbound.PreferIP != "only-ipv4" && inbound.PreferIP != "only-ipv6" {
			return config, errors.New("invalid MTProto IP preference")
		}
		for _, ip := range []string{inbound.PublicIPv4, inbound.PublicIPv6} {
			if ip != "" {
				if _, err := netip.ParseAddr(ip); err != nil {
					return config, errors.New("invalid advertised MTProto address")
				}
			}
		}
		if inbound.SOCKS5 != "" {
			proxy, err := url.Parse(inbound.SOCKS5)
			if err != nil || proxy.Scheme != "socks5" || proxy.Hostname() == "" || proxy.Path != "" || proxy.RawQuery != "" || proxy.Fragment != "" {
				return config, errors.New("invalid MTProto egress address")
			}
		}
		if _, err := secretsOf(inbound); err != nil {
			return config, err
		}
		if err := validateWEB(inbound); err != nil {
			return config, err
		}
		if inbound.WEB != nil {
			if binds[inbound.WEB.Bind] {
				return config, errors.New("repeated WEB relay bind address")
			}
			binds[inbound.WEB.Bind] = true
		}
		for _, listener := range inbound.Listeners {
			host, port, err := net.SplitHostPort(listener.Bind)
			address, parseErr := netip.ParseAddr(host)
			number, portErr := strconv.Atoi(port)
			if err != nil || parseErr != nil || address.Is4In6() || address.Zone() != "" || address.IsMulticast() || portErr != nil || number < 1 || number > 65535 || listener.Bind != net.JoinHostPort(address.String(), strconv.Itoa(number)) || binds[listener.Bind] {
				return config, errors.New("invalid or repeated MTProto bind address")
			}
			binds[listener.Bind] = true
			modes, err := mtglib.ParseModeSet(listener.Accept)
			if err != nil || modes.Empty() || modes.Has(mtglib.SecretModeFakeTLS) && (modes.Has(mtglib.SecretModeDD) || modes.Has(mtglib.SecretModePlain)) {
				return config, errors.New("invalid MTProto listener modes")
			}
			if (modes.Has(mtglib.SecretModeDD) || modes.Has(mtglib.SecretModePlain)) && !address.IsLoopback() && !listener.Public {
				return config, errors.New("public obfuscated listener requires explicit public acknowledgement")
			}
			for _, trusted := range listener.TrustedProxies {
				if _, err := netip.ParsePrefix(trusted); err != nil {
					return config, errors.New("invalid MTProto trusted-proxy network")
				}
			}
		}
	}
	return config, nil
}

func tagOf(value string) (*[mtglib.AdTagLength]byte, error) {
	if value == "" {
		return nil, nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != mtglib.AdTagLength {
		return nil, errors.New("invalid MTProto advertising tag")
	}
	var tag [mtglib.AdTagLength]byte
	copy(tag[:], decoded)
	return &tag, nil
}

func secretsOf(inbound Inbound) (mtglib.SecretConfig, error) {
	config := mtglib.SecretConfig{Secrets: make(map[string]mtglib.Secret), SecretAdTags: make(map[string][mtglib.AdTagLength]byte), Limits: make(map[string]mtglib.SecretLimits)}
	keys := make(map[[mtglib.SecretKeyLength]byte]bool, len(inbound.Clients))
	var err error
	config.GlobalAdTag, err = tagOf(inbound.AdTag)
	if err != nil {
		return config, err
	}
	for id, client := range inbound.Clients {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id || client.IPLimit < 0 {
			return config, errors.New("invalid MTProto client identity")
		}
		var secret mtglib.Secret
		if err := secret.Set(client.Secret); err != nil {
			return config, errors.New("invalid MTProto client credential")
		}
		if keys[secret.Key] {
			return config, errors.New("ambiguous MTProto client credential")
		}
		keys[secret.Key] = true
		config.Secrets[id] = secret
		config.Limits[id] = mtglib.SecretLimits{IPLimit: client.IPLimit}
		tag, err := tagOf(client.AdTag)
		if err != nil {
			return config, err
		}
		if tag != nil {
			config.SecretAdTags[id] = *tag
		}
	}
	return config, nil
}

func (*Managed) Validate(data []byte) error { _, err := configurationOf(data); return err }

func (m *Managed) Apply(ctx context.Context, data []byte, lookup func(string) (*limits.Account, bool)) error {
	config, err := configurationOf(data)
	if err != nil {
		return err
	}
	if m.instances == nil {
		m.instances = make(map[string]*running)
	}
	wanted := make(map[string]bool)
	var failures []error
	for _, inbound := range config.Inbounds {
		if inbound.WEB != nil && m.store == nil {
			return errors.New("WEB relay requires a bound runtime journal")
		}
		wanted[inbound.ID] = true
		secrets, err := secretsOf(inbound)
		if err != nil {
			return err
		}
		if binding := admission.BindingFromContext(ctx); binding != nil {
			secrets.Authentication = make(map[string]mtglib.Authentication, len(inbound.Clients))
			for client := range inbound.Clients {
				proof, ok := binding.Capture(client, inbound.ID)
				if !ok {
					return admission.ErrUnavailable
				}
				secrets.Authentication[client] = proof
			}
		}
		structure := inbound
		structure.Clients = nil
		structure.AdTag = ""
		directTLS := false
		if structure.WEB != nil {
			web := *structure.WEB
			directTLS = web.ServerCert != ""
			web.ServerCert, web.ServerKey = "", ""
			structure.WEB = &web
		}
		encoded, _ := json.Marshal(struct {
			Inbound   Inbound
			DirectTLS bool
		}{structure, directTLS})
		digest := sha256.Sum256(encoded)
		if existing := m.instances[inbound.ID]; existing != nil {
			if existing.structure == digest && !existing.exited.Load() && (existing.web == nil || !existing.web.exited.Load()) {
				failures = append(failures, existing.proxy.ReplaceSecrets(secrets))
				if existing.web != nil {
					failures = append(failures, existing.web.replace(secrets, inbound.WEB))
				}
				continue
			}
			existing.close()
			delete(m.instances, inbound.ID)
		}
		process, err := start(ctx, inbound, secrets, lookup, m.stateDir, m.network, m.store)
		if err != nil {
			failures = append(failures, fmt.Errorf("MTProto inbound %s failed to start: %w", inbound.ID, err))
			continue
		}
		process.structure = digest
		m.instances[inbound.ID] = process
	}
	for id, process := range m.instances {
		if !wanted[id] {
			process.close()
			delete(m.instances, id)
		}
	}
	return errors.Join(failures...)
}

func start(ctx context.Context, inbound Inbound, secrets mtglib.SecretConfig, lookup Lookup, stateDir string, override mtglib.Network, store *state.Store) (*running, error) {
	ntw := network.New(nil, "penhoon-node", 10*time.Second, 20*time.Second, time.Minute, net.KeepAliveConfig{Enable: true}, 0)
	if override != nil {
		ntw = override
	}
	if inbound.SOCKS5 != "" {
		address, _ := url.Parse(inbound.SOCKS5)
		proxied, err := network.NewProxyNetwork(ntw, address)
		if err != nil {
			return nil, err
		}
		ntw = proxied
	}
	level := zerolog.WarnLevel
	if inbound.Debug {
		level = zerolog.DebugLevel
	}
	var modes mtglib.ModeSet
	for _, listener := range inbound.Listeners {
		accepted, _ := mtglib.ParseModeSet(listener.Accept)
		modes |= accepted
	}
	if inbound.WEB != nil {
		modes |= mtglib.ModeSetOf(mtglib.SecretModeDD)
	}
	options := mtglib.ProxyOpts{
		Network: ntw, Logger: logger.NewZeroLogger(zerolog.New(os.Stdout).Level(level)),
		AntiReplayCache: antireplay.NewStableBloomFilter(1<<20, 0.01), IPBlocklist: ipblocklist.NewNoop(), IPAllowlist: allowAll{ipblocklist.NewNoop()}, EventStream: events.NewNoopStream(),
		Secrets: secrets.Secrets, SecretAdTags: secrets.SecretAdTags, SecretLimits: secrets.Limits, GlobalAdTag: secrets.GlobalAdTag,
		Authentication: secrets.Authentication,
		ListenerModes:  modes, Concurrency: inbound.Concurrency, ThrottleMaxConnections: uint(inbound.ThrottleMaxConnections),
		DomainFrontingHost: inbound.FrontingHost, DomainFrontingPort: uint(inbound.FrontingPort), DomainFrontingProxyProtocol: inbound.FrontingProxyProtocol,
		PreferIP: inbound.PreferIP, PublicIPv4: net.ParseIP(inbound.PublicIPv4), PublicIPv6: net.ParseIP(inbound.PublicIPv6), AdvertisedPort: inbound.AdvertisedPort,
		TrackClientIPs: true,
	}
	proxy, err := newWithDemand(options, lookup, admission.FromContext(ctx), inbound.ID)
	if err != nil {
		return nil, err
	}
	process := &running{proxy: proxy}
	if inbound.WEB != nil {
		process.web, err = startWEB(ctx, inbound, secrets, proxy, options.Network, options.Logger, stateDir, store)
		if err != nil {
			process.close()
			return nil, err
		}
	}
	for _, spec := range inbound.Listeners {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", spec.Bind)
		if err != nil {
			process.close()
			return nil, err
		}
		if len(spec.TrustedProxies) != 0 {
			trusted := make([]netip.Prefix, len(spec.TrustedProxies))
			for i, value := range spec.TrustedProxies {
				trusted[i] = netip.MustParsePrefix(value)
			}
			listener = &proxyproto.Listener{Listener: listener, ReadHeaderTimeout: 5 * time.Second, ConnPolicy: func(options proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
				host, _, err := net.SplitHostPort(options.Upstream.String())
				address, parseErr := netip.ParseAddr(host)
				if err == nil && parseErr == nil {
					for _, prefix := range trusted {
						if prefix.Contains(address) {
							return proxyproto.REQUIRE, nil
						}
					}
				}
				return proxyproto.REJECT, errors.New("untrusted MTProto proxy peer")
			}}
		}
		process.listeners = append(process.listeners, listener)
	}
	for i, listener := range process.listeners {
		spec := inbound.Listeners[i]
		modes, _ := mtglib.ParseModeSet(spec.Accept)
		process.done.Go(func() {
			defer process.exited.Store(true)
			_ = proxy.ServeWithPolicy(listener, mtglib.ListenerPolicy{Modes: modes, Public: spec.Public, HandshakesPerIP: 10})
		})
	}
	return process, nil
}

func (r *running) close() {
	if r.web != nil {
		r.web.close()
	}
	for _, listener := range r.listeners {
		_ = listener.Close()
	}
	r.proxy.Shutdown()
	r.done.Wait()
}

func (m *Managed) Close() error {
	for id, process := range m.instances {
		process.close()
		delete(m.instances, id)
	}
	return nil
}

func (m *Managed) Check() error {
	for _, process := range m.instances {
		if process.exited.Load() || process.web != nil && process.web.exited.Load() {
			return errors.New("MTProto listener stopped unexpectedly")
		}
	}
	return nil
}
