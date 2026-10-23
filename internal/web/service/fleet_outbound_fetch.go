package service

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/util/link"
	"github.com/Arman2122/p-ui/internal/util/netsafe"
)

var (
	ErrFleetProvider         = errors.New("invalid fleet outbound provider")
	ErrFleetProviderRevision = errors.New("fleet outbound provider revision changed")
	ErrFleetProviderFetch    = errors.New("fleet outbound provider refresh failed")
)

const (
	maxFleetProviderBytes      = 8 << 20
	maxFleetProviderEntries    = 4096
	maxFleetProviderIdentities = 32768
)

type FleetProviderFetchSpec struct {
	Version         uint32 `json:"version" example:"1"`
	URL             string `json:"url" example:"https://provider.example/feed"`
	Format          string `json:"format" example:"auto"`
	IntervalSeconds int    `json:"intervalSeconds" example:"60"`
	TagPrefix       string `json:"tagPrefix,omitempty"`
	AllowPrivate    bool   `json:"allowPrivate"`
	AllowInsecure   bool   `json:"allowInsecure"`
	HWID            string `json:"hwid,omitempty"`
}

type fleetProviderFailure struct{ Code string }

func (e fleetProviderFailure) Error() string        { return "provider refresh: " + e.Code }
func (e fleetProviderFailure) Is(target error) bool { return target == ErrFleetProviderFetch }

type fleetProviderNetwork struct {
	resolve func(context.Context, string) ([]net.IPAddr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

func providerURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return nil, ErrFleetProvider
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || strings.Contains(parsed.Hostname(), "%") {
		return nil, ErrFleetProvider
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return nil, ErrFleetProvider
		}
	}
	return parsed, nil
}

func validateFleetProviderFetch(spec FleetProviderFetchSpec) error {
	if spec.Version != 1 || spec.Format != "auto" && spec.Format != "links" && spec.Format != "base64" || spec.IntervalSeconds < 30 || spec.IntervalSeconds > 86400 ||
		len(spec.HWID) > 256 || strings.ContainsAny(spec.HWID, "\r\n\x00") || len(spec.TagPrefix) > 32 || spec.TagPrefix != "" && !fleetNetworkName.MatchString(spec.TagPrefix) {
		return ErrFleetProvider
	}
	_, err := providerURL(spec.URL)
	return err
}

func (network fleetProviderNetwork) guardedDial(allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	resolve, dial := network.resolve, network.dial
	if resolve == nil {
		resolve = net.DefaultResolver.LookupIPAddr
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	return func(ctx context.Context, kind, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fleetProviderFailure{"INVALID_TARGET"}
		}
		ips := []net.IPAddr{}
		if ip := net.ParseIP(host); ip != nil {
			ips = append(ips, net.IPAddr{IP: ip})
		} else {
			ips, err = resolve(ctx, host)
			if err != nil || len(ips) == 0 || len(ips) > 64 {
				return nil, fleetProviderFailure{"DNS_FAILED"}
			}
		}
		for _, resolved := range ips {
			if resolved.IP == nil || resolved.IP.IsMulticast() || resolved.IP.IsUnspecified() || resolved.Zone != "" || !allowPrivate && netsafe.IsBlockedIP(resolved.IP) {
				return nil, fleetProviderFailure{"PRIVATE_TARGET"}
			}
		}
		for _, resolved := range ips {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			conn, err := dial(ctx, kind, net.JoinHostPort(resolved.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fleetProviderFailure{"DIAL_FAILED"}
	}
}

func fetchFleetProvider(ctx context.Context, spec FleetProviderFetchSpec, network fleetProviderNetwork) ([]byte, error) {
	if err := validateFleetProviderFetch(spec); err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: network.guardedDial(spec.AllowPrivate), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: spec.AllowInsecure}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	client.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 10 {
			return fleetProviderFailure{"REDIRECT_LIMIT"}
		}
		if _, err := providerURL(request.URL.String()); err != nil || previous[0].URL.Scheme == "https" && request.URL.Scheme != "https" {
			return fleetProviderFailure{"REDIRECT_DENIED"}
		}
		if request.URL.Scheme != previous[0].URL.Scheme || !strings.EqualFold(request.URL.Host, previous[0].URL.Host) {
			for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-HWID"} {
				request.Header.Del(header)
			}
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return nil, ErrFleetProvider
	}
	request.Header.Set("User-Agent", "penhoon-provider/1")
	if spec.HWID != "" {
		request.Header.Set("X-HWID", spec.HWID)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var failure fleetProviderFailure
		if errors.As(err, &failure) {
			return nil, failure
		}
		return nil, fleetProviderFailure{"FETCH_FAILED"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return nil, fleetProviderFailure{"HTTP_STATUS_" + strconv.Itoa(response.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFleetProviderBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fleetProviderFailure{"BODY_INTERRUPTED"}
	}
	if len(body) > maxFleetProviderBytes {
		return nil, fleetProviderFailure{"BODY_TOO_LARGE"}
	}
	return body, nil
}

type fleetProviderContent struct {
	Outbounds        []byte
	Identities       []byte
	OutboundsDigest  []byte
	IdentitiesDigest []byte
	Count            int
}

func parseFleetProvider(providerID string, spec FleetProviderFetchSpec, body []byte, previous map[string]string) (fleetProviderContent, error) {
	var result fleetProviderContent
	if !fleetID(providerID) || len(body) > maxFleetProviderBytes || len(previous) > maxFleetProviderIdentities {
		return result, ErrFleetProvider
	}
	text := strings.TrimSpace(string(body))
	lines := strings.Split(strings.ReplaceAll(text, `\n`, "\n"), "\n")
	var feed []string
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			feed = append(feed, line)
		}
	}
	text = strings.Join(feed, "\n")
	if spec.Format == "base64" || spec.Format == "auto" && text != "" && !strings.Contains(text, "://") {
		compact := strings.Join(strings.Fields(text), "")
		var decoded []byte
		var err error
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			decoded, err = encoding.DecodeString(compact)
			if err == nil {
				break
			}
		}
		if err != nil {
			return result, fleetProviderFailure{"INVALID_FEED"}
		}
		text = strings.TrimSpace(string(decoded))
	}
	if !utf8.ValidString(text) {
		return result, fleetProviderFailure{"INVALID_FEED"}
	}
	identities := maps.Clone(previous)
	if identities == nil {
		identities = map[string]string{}
	}
	used := map[string]bool{}
	for identity, tag := range identities {
		if decoded, err := hex.DecodeString(identity); err != nil || len(decoded) != sha256.Size || !fleetNetworkName.MatchString(tag) || used[tag] {
			return result, fleetProviderFailure{"CACHE_CORRUPT"}
		}
		used[tag] = true
	}
	seen := map[string]bool{}
	entries := []link.Outbound{}
	text = strings.ReplaceAll(text, `\n`, "\n")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parsed, err := link.ParseLink(line)
		if err != nil || parsed == nil || len(entries) == maxFleetProviderEntries {
			return result, fleetProviderFailure{"INVALID_FEED"}
		}
		if seen[parsed.Identity] {
			return result, fleetProviderFailure{"AMBIGUOUS_IDENTITY"}
		}
		seen[parsed.Identity] = true
		delete(parsed.Outbound, "tag")
		_, digest, err := canonicalFleetArtifact(parsed.Outbound, 1<<20)
		if err != nil {
			return result, fleetProviderFailure{"INVALID_FEED"}
		}
		identity := hex.EncodeToString(digest)
		tag := identities[identity]
		if tag == "" {
			if len(identities) == maxFleetProviderIdentities {
				return result, fleetProviderFailure{"IDENTITY_CAPACITY"}
			}
			tag = spec.TagPrefix + "p" + strings.ReplaceAll(providerID, "-", "")[:12] + "-" + uuid.NewString()
			if used[tag] || !fleetNetworkName.MatchString(tag) {
				return result, fleetProviderFailure{"TAG_CONFLICT"}
			}
			identities[identity], used[tag] = tag, true
		}
		parsed.Outbound["tag"] = tag
		entries = append(entries, parsed.Outbound)
	}
	result.Outbounds, result.OutboundsDigest, _ = canonicalFleetArtifact(entries, maxFleetProviderBytes)
	result.Identities, result.IdentitiesDigest, _ = canonicalFleetArtifact(identities, maxFleetProviderBytes)
	if len(result.Outbounds) == 0 || len(result.Identities) == 0 {
		return fleetProviderContent{}, fleetProviderFailure{"CACHE_CAPACITY"}
	}
	if err := nodeconfig.ValidateXrayProviderOutbounds(result.Outbounds); err != nil {
		if errors.Is(err, nodeconfig.ErrUnsupported) {
			return fleetProviderContent{}, fleetProviderFailure{"UNQUALIFIED_PROTOCOL"}
		}
		return fleetProviderContent{}, fleetProviderFailure{"INVALID_OUTBOUND"}
	}
	result.Count = len(entries)
	return result, nil
}
