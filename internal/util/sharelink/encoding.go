package sharelink

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-json"

	"github.com/Arman2122/p-ui/internal/util/link"
	"github.com/Arman2122/p-ui/internal/util/random"
)

func NormalizeVmessSecurity(security string) string {
	switch security {
	case "", "none", "zero":
		return "auto"
	}
	return security
}

func EncodeUserinfo(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func JoinHostPort(host string, port int) string {
	host = strings.Trim(host, "[]")
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func SalamanderSettings(stream map[string]any) map[string]any {
	finalmask, _ := stream["finalmask"].(map[string]any)
	masks, _ := finalmask["udp"].([]any)
	var selected map[string]any
	for _, raw := range masks {
		mask, _ := raw.(map[string]any)
		if mask["type"] != "salamander" {
			continue
		}
		settings, _ := mask["settings"].(map[string]any)
		if password, _ := settings["password"].(string); password != "" {
			selected = settings
		}
	}
	return selected
}

func SalamanderPassword(stream map[string]any) string {
	password, _ := SalamanderSettings(stream)["password"].(string)
	return password
}

func ApplySalamanderParams(stream map[string]any, params map[string]string) {
	for _, key := range []string{"obfs", "obfs-password", "minPacketSize", "maxPacketSize"} {
		delete(params, key)
	}
	settings := SalamanderSettings(stream)
	password, _ := settings["password"].(string)
	if password == "" {
		return
	}
	params["obfs"] = "salamander"
	params["obfs-password"] = password
	packetSize, _ := settings["packetSize"].(string)
	if normalized := ParseHysteriaPacketSize(packetSize); normalized != "" {
		params["obfs"] = "gecko"
		params["minPacketSize"], params["maxPacketSize"], _ = strings.Cut(normalized, "-")
	}
}

func HysteriaHopPorts(stream map[string]any) string {
	finalmask, _ := stream["finalmask"].(map[string]any)
	masks, _ := finalmask["udp"].([]any)
	ports := ""
	for _, raw := range masks {
		mask, _ := raw.(map[string]any)
		if mask["type"] != "udphop" {
			continue
		}
		settings, _ := mask["settings"].(map[string]any)
		if value, _ := settings["remotePorts"].(string); strings.TrimSpace(value) != "" {
			ports = strings.TrimSpace(value)
		}
	}
	if ports != "" {
		return ports
	}
	quicParams, _ := finalmask["quicParams"].(map[string]any)
	udpHop, _ := quicParams["udpHop"].(map[string]any)
	ports, _ = udpHop["ports"].(string)
	return strings.TrimSpace(ports)
}

func UnmarshalStreamSettings(streamSettings string) map[string]any {
	var stream map[string]any
	_ = json.Unmarshal([]byte(streamSettings), &stream)
	return stream
}

func ApplyPathAndHostParams(settings map[string]any, params map[string]string) {
	params["path"], _ = settings["path"].(string)
	if host, ok := settings["host"].(string); ok && len(host) > 0 {
		params["host"] = host
	} else {
		headers, _ := settings["headers"].(map[string]any)
		params["host"] = SearchHost(headers)
	}
}

func ApplyPathAndHostObj(settings map[string]any, obj map[string]any) {
	obj["path"], _ = settings["path"].(string)
	if host, ok := settings["host"].(string); ok && len(host) > 0 {
		obj["host"] = host
	} else {
		headers, _ := settings["headers"].(map[string]any)
		obj["host"] = SearchHost(headers)
	}
}

func ApplyShareNetworkParams(stream map[string]any, streamNetwork string, params map[string]string) {
	switch streamNetwork {
	case "tcp":
		tcp, _ := stream["tcpSettings"].(map[string]any)
		header, _ := tcp["header"].(map[string]any)
		typeStr, _ := header["type"].(string)
		if typeStr == "http" {
			request, _ := header["request"].(map[string]any)
			requestPath, _ := request["path"].([]any)
			if len(requestPath) > 0 {
				params["path"], _ = requestPath[0].(string)
			}
			host := ""
			if response, ok := header["response"].(map[string]any); ok {
				if respHeaders, ok := response["headers"].(map[string]any); ok {
					host = SearchHost(respHeaders)
				}
			}
			if host == "" {
				headers, _ := request["headers"].(map[string]any)
				host = SearchHost(headers)
			}
			params["host"] = host
			params["headerType"] = "http"
		}
	case "kcp":
		ApplyKcpShareParams(stream, params)
	case "ws":
		ws, _ := stream["wsSettings"].(map[string]any)
		ApplyPathAndHostParams(ws, params)
	case "grpc":
		grpc, _ := stream["grpcSettings"].(map[string]any)
		params["serviceName"], _ = grpc["serviceName"].(string)
		params["authority"], _ = grpc["authority"].(string)
		if mm, _ := grpc["multiMode"].(bool); mm {
			params["mode"] = "multi"
		}
	case "httpupgrade":
		httpupgrade, _ := stream["httpupgradeSettings"].(map[string]any)
		ApplyPathAndHostParams(httpupgrade, params)
	case "xhttp":
		xhttp, _ := stream["xhttpSettings"].(map[string]any)
		ApplyXhttpExtraParams(xhttp, params)
	}
}

func ApplyXhttpExtraObj(xhttp map[string]any, obj map[string]any) {
	if xpb, ok := xhttp["xPaddingBytes"].(string); ok && len(xpb) > 0 {
		obj["x_padding_bytes"] = xpb
	}
	maps.Copy(obj, BuildXhttpExtra(xhttp))
}

func ApplyVmessNetworkParams(stream map[string]any, network string, obj map[string]any) {
	obj["net"] = network
	switch network {
	case "tcp":
		tcp, _ := stream["tcpSettings"].(map[string]any)
		header, _ := tcp["header"].(map[string]any)
		typeStr, _ := header["type"].(string)
		obj["type"] = typeStr
		if typeStr == "http" {
			request, _ := header["request"].(map[string]any)
			requestPath, _ := request["path"].([]any)
			if len(requestPath) > 0 {
				obj["path"], _ = requestPath[0].(string)
			}
			host := ""
			if response, ok := header["response"].(map[string]any); ok {
				if respHeaders, ok := response["headers"].(map[string]any); ok {
					host = SearchHost(respHeaders)
				}
			}
			if host == "" {
				headers, _ := request["headers"].(map[string]any)
				host = SearchHost(headers)
			}
			obj["host"] = host
		}
	case "kcp":
		ApplyKcpShareObj(stream, obj)
	case "ws":
		ws, _ := stream["wsSettings"].(map[string]any)
		ApplyPathAndHostObj(ws, obj)
	case "grpc":
		grpc, _ := stream["grpcSettings"].(map[string]any)
		obj["path"], _ = grpc["serviceName"].(string)
		obj["authority"], _ = grpc["authority"].(string)
		if mm, _ := grpc["multiMode"].(bool); mm {
			obj["type"] = "multi"
		}
	case "httpupgrade":
		httpupgrade, _ := stream["httpupgradeSettings"].(map[string]any)
		ApplyPathAndHostObj(httpupgrade, obj)
	case "xhttp":
		xhttp, _ := stream["xhttpSettings"].(map[string]any)
		ApplyPathAndHostObj(xhttp, obj)
		if mode, ok := xhttp["mode"].(string); ok {
			obj["mode"] = mode
			if mode != "" {
				obj["type"] = mode
			}
		}
		ApplyXhttpExtraObj(xhttp, obj)
	}
}

func ApplyShareTLSParams(stream map[string]any, params map[string]string) {
	params["security"] = "tls"
	tlsSetting, _ := stream["tlsSettings"].(map[string]any)
	alpns, _ := tlsSetting["alpn"].([]any)
	var alpn []string
	for _, a := range alpns {
		if s, ok := a.(string); ok {
			alpn = append(alpn, s)
		}
	}
	if len(alpn) > 0 {
		params["alpn"] = strings.Join(alpn, ",")
	}
	if sniValue, ok := SearchKey(tlsSetting, "serverName"); ok {
		params["sni"], _ = sniValue.(string)
	}

	tlsSettings, _ := SearchKey(tlsSetting, "settings")
	if tlsSetting != nil {
		if fpValue, ok := SearchKey(tlsSettings, "fingerprint"); ok {
			params["fp"], _ = fpValue.(string)
		}
		if echValue, ok := SearchKey(tlsSettings, "echConfigList"); ok {
			if ech, _ := echValue.(string); ech != "" {
				params["ech"] = ech
			}
		}
		if vcn, ok := VerifyPeerCertByNameValue(tlsSettings); ok {
			params["vcn"] = vcn
		}
		if pins, ok := PinnedSha256List(tlsSettings); ok {
			params["pcs"] = strings.Join(pins, ",")
		}
	}
}

func ApplyVmessTLSParams(stream map[string]any, obj map[string]any) {
	tlsSetting, _ := stream["tlsSettings"].(map[string]any)
	alpns, _ := tlsSetting["alpn"].([]any)
	if len(alpns) > 0 {
		var alpn []string
		for _, a := range alpns {
			if s, ok := a.(string); ok {
				alpn = append(alpn, s)
			}
		}
		obj["alpn"] = strings.Join(alpn, ",")
	}
	if sniValue, ok := SearchKey(tlsSetting, "serverName"); ok {
		obj["sni"], _ = sniValue.(string)
	}

	tlsSettings, _ := SearchKey(tlsSetting, "settings")
	if tlsSetting != nil {
		if fpValue, ok := SearchKey(tlsSettings, "fingerprint"); ok {
			obj["fp"], _ = fpValue.(string)
		}
		if echValue, ok := SearchKey(tlsSettings, "echConfigList"); ok {
			if ech, _ := echValue.(string); ech != "" {
				obj["ech"] = ech
			}
		}
		if vcn, ok := VerifyPeerCertByNameValue(tlsSettings); ok {
			obj["vcn"] = vcn
		}
		if pins, ok := PinnedSha256List(tlsSettings); ok {
			obj["pcs"] = strings.Join(pins, ",")
		}
	}
}

func VerifyPeerCertByNameValue(tlsClientSettings any) (string, bool) {
	raw, ok := SearchKey(tlsClientSettings, "verifyPeerCertByName")
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	if s = strings.TrimSpace(s); s == "" {
		return "", false
	}
	return s, true
}

func PinnedSha256List(tlsClientSettings any) ([]string, bool) {
	raw, ok := SearchKey(tlsClientSettings, "pinnedPeerCertSha256")
	if !ok {
		return nil, false
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		out = append(out, link.NormalizePinHex(s))
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func ApplyShareRealityParams(stream map[string]any, params map[string]string, clientKey string) {
	params["security"] = "reality"
	realitySetting, _ := stream["realitySettings"].(map[string]any)
	realitySettings, _ := SearchKey(realitySetting, "settings")
	if realitySetting != nil {
		if pinned, ok := SearchKey(realitySettings, "serverName"); ok {
			if name, _ := pinned.(string); name != "" {
				params["sni"] = name
			}
		}
		if _, pinned := params["sni"]; !pinned {
			if sniValue, ok := SearchKey(realitySetting, "serverNames"); ok {
				if sNames, _ := sniValue.([]any); len(sNames) > 0 {
					params["sni"], _ = sNames[DeriveListIndex("sni", clientKey, len(sNames))].(string)
				}
			}
		}
		if pbkValue, ok := SearchKey(realitySettings, "publicKey"); ok {
			params["pbk"], _ = pbkValue.(string)
		}
		if sidValue, ok := SearchKey(realitySetting, "shortIds"); ok {
			if shortIds, _ := sidValue.([]any); len(shortIds) > 0 {
				params["sid"], _ = shortIds[DeriveListIndex("sid", clientKey, len(shortIds))].(string)
			}
		}
		if fpValue, ok := SearchKey(realitySettings, "fingerprint"); ok {
			if fp, ok := fpValue.(string); ok && len(fp) > 0 {
				params["fp"] = fp
			}
		}
		if pqvValue, ok := SearchKey(realitySettings, "mldsa65Verify"); ok {
			if pqv, ok := pqvValue.(string); ok && len(pqv) > 0 {
				params["pqv"] = pqv
			}
		}
		Seed := ""
		if spxValue, ok := SearchKey(realitySettings, "spiderX"); ok {
			Seed, _ = spxValue.(string)
		}
		params["spx"] = DeriveSpiderX(Seed, clientKey)
	}
}

func DeriveSpiderX(Seed, clientKey string) string {
	if Seed == "" && clientKey == "" {
		return "/" + random.Seq(15)
	}
	sum := sha256.Sum256([]byte(Seed + "|" + clientKey))
	return "/" + hex.EncodeToString(sum[:])[:15]
}

func DeriveListIndex(field, clientKey string, n int) int {
	if n <= 1 {
		return 0
	}
	sum := sha256.Sum256([]byte(field + "|" + clientKey))
	return int(binary.BigEndian.Uint64(sum[:8]) % uint64(n))
}

func BuildVmessLink(obj map[string]any) string {
	jsonStr, _ := json.MarshalIndent(obj, "", "  ")
	return "vmess://" + base64.StdEncoding.EncodeToString(jsonStr)
}

func BuildLinkWithParams(link string, params map[string]string, fragment string) string {
	return AppendQueryAndFragment(link, params, fragment, "", false)
}

func BuildLinkWithParamsAndSecurity(link string, params map[string]string, fragment, security string, omitTLSFields bool) string {
	return AppendQueryAndFragment(link, params, fragment, security, omitTLSFields)
}

func AppendQueryAndFragment(link string, params map[string]string, fragment, securityOverride string, omitTLSFields bool) string {
	var sb strings.Builder
	sb.WriteString(link)

	if len(params) > 0 {
		q := url.Values{}
		for k, v := range params {
			if securityOverride != "" && k == "security" {
				v = securityOverride
			}
			if omitTLSFields && (k == "alpn" || k == "sni" || k == "fp" || k == "pcs") {
				continue
			}
			q.Set(k, v)
		}
		encoded := q.Encode()
		if encoded != "" {
			if strings.Contains(link, "?") {
				sb.WriteByte('&')
			} else {
				sb.WriteByte('?')
			}
			sb.WriteString(encoded)
		}
	}

	if fragment != "" {
		sb.WriteByte('#')
		sb.WriteString(strings.ReplaceAll(url.QueryEscape(fragment), "+", "%20"))
	}
	return sb.String()
}

func SearchKey(data any, key string) (any, bool) {
	switch val := data.(type) {
	case map[string]any:
		for k, v := range val {
			if k == key {
				return v, true
			}
			if result, ok := SearchKey(v, key); ok {
				return result, true
			}
		}
	case []any:
		for _, v := range val {
			if result, ok := SearchKey(v, key); ok {
				return result, true
			}
		}
	}
	return nil, false
}

func BuildXhttpExtra(xhttp map[string]any) map[string]any {
	if xhttp == nil {
		return nil
	}
	extra := map[string]any{}

	if mode, ok := xhttp["mode"].(string); ok && len(mode) > 0 {
		extra["mode"] = mode
	}

	if xpb, ok := xhttp["xPaddingBytes"].(string); ok && len(xpb) > 0 {
		extra["xPaddingBytes"] = xpb
	}
	if obfs, ok := xhttp["xPaddingObfsMode"].(bool); ok && obfs {
		extra["xPaddingObfsMode"] = true
		for _, field := range []string{"xPaddingKey", "xPaddingHeader", "xPaddingPlacement", "xPaddingMethod"} {
			if v, ok := xhttp[field].(string); ok && len(v) > 0 {
				extra[field] = v
			}
		}
	}

	stringFields := []string{
		"uplinkHTTPMethod",
		"sessionIDPlacement", "sessionIDKey", "sessionIDTable", "sessionIDLength",
		"seqPlacement", "seqKey",
		"uplinkDataPlacement", "uplinkDataKey",
		"scMaxEachPostBytes", "scMinPostsIntervalMs",
	}
	coreDefaults := map[string]string{
		"scMaxEachPostBytes":   "1000000",
		"scMinPostsIntervalMs": "30",
	}
	for _, field := range stringFields {
		if v, ok := xhttp[field].(string); ok && len(v) > 0 && v != coreDefaults[field] {
			extra[field] = v
		}
	}
	for legacy, renamed := range map[string]string{
		"sessionPlacement": "sessionIDPlacement",
		"sessionKey":       "sessionIDKey",
	} {
		if _, exists := extra[renamed]; !exists {
			if v, ok := xhttp[legacy].(string); ok && len(v) > 0 {
				extra[renamed] = v
			}
		}
	}
	if v, ok := extra["sessionIDPlacement"].(string); ok && len(v) > 0 {
		extra["sessionPlacement"] = v
	}
	if v, ok := extra["sessionIDKey"].(string); ok && len(v) > 0 {
		extra["sessionKey"] = v
	}

	for _, field := range []string{"uplinkChunkSize"} {
		if v, ok := NonZeroShareValue(xhttp[field]); ok {
			extra[field] = v
		}
	}

	for _, field := range []string{"noGRPCHeader"} {
		if v, ok := xhttp[field].(bool); ok && v {
			extra[field] = v
		}
	}

	for _, field := range []string{"xmux", "downloadSettings"} {
		v, ok := NonEmptyShareObject(xhttp[field])
		if !ok {
			continue
		}
		if field == "downloadSettings" && extra["mode"] == "stream-one" {
			continue
		}
		extra[field] = v
	}
	if rawHeaders, ok := xhttp["headers"].(map[string]any); ok && len(rawHeaders) > 0 {
		out := map[string]any{}
		for k, v := range rawHeaders {
			if strings.EqualFold(k, "host") {
				continue
			}
			out[k] = v
		}
		if len(out) > 0 {
			extra["headers"] = out
		}
	}

	if len(extra) == 0 {
		return nil
	}
	return extra
}

func NonZeroShareValue(v any) (any, bool) {
	switch value := v.(type) {
	case string:
		return value, value != ""
	case int:
		return value, value != 0
	case int32:
		return value, value != 0
	case int64:
		return value, value != 0
	case float32:
		return value, value != 0
	case float64:
		return value, value != 0
	default:
		return nil, false
	}
}

func NonEmptyShareObject(v any) (any, bool) {
	switch value := v.(type) {
	case map[string]any:
		return value, len(value) > 0
	case map[string]string:
		return value, len(value) > 0
	case []any:
		return value, len(value) > 0
	default:
		return nil, false
	}
}

func ApplyXhttpExtraParams(xhttp map[string]any, params map[string]string) {
	if xhttp == nil {
		return
	}
	ApplyPathAndHostParams(xhttp, params)
	if mode, ok := xhttp["mode"].(string); ok {
		params["mode"] = mode
	}

	if xpb, ok := xhttp["xPaddingBytes"].(string); ok && len(xpb) > 0 {
		params["x_padding_bytes"] = xpb
	}

	extra := BuildXhttpExtra(xhttp)
	if extra != nil {
		if b, err := json.Marshal(extra); err == nil {
			params["extra"] = string(b)
		}
	}
}

var kcpMaskToHeaderType = map[string]string{
	"dns":       "dns",
	"dtls":      "dtls",
	"srtp":      "srtp",
	"utp":       "utp",
	"wechat":    "wechat-video",
	"wireguard": "wireguard",
}

var validFinalMaskUDPTypes = map[string]struct{}{
	"salamander":    {},
	"mkcp-legacy":   {},
	"xdns":          {},
	"xicmp":         {},
	"noise":         {},
	"header-custom": {},
	"realm":         {},
}

var validFinalMaskTCPTypes = map[string]struct{}{
	"header-custom": {},
	"fragment":      {},
	"sudoku":        {},
	"xmc":           {},
}

func ApplyKcpShareParams(stream map[string]any, params map[string]string) {
	ExtractKcpShareFields(stream).ApplyToParams(params)
}

func ApplyKcpShareObj(stream map[string]any, obj map[string]any) {
	ExtractKcpShareFields(stream).ApplyToObj(obj)
}

type KcpShareFields struct {
	HeaderType string
	Seed       string
	MTU        int
	TTI        int
}

func (f KcpShareFields) ApplyToParams(params map[string]string) {
	if f.HeaderType != "" && f.HeaderType != "none" {
		params["headerType"] = f.HeaderType
	}
	SetStringParam(params, "seed", f.Seed)
	SetIntParam(params, "mtu", f.MTU)
	SetIntParam(params, "tti", f.TTI)
}

func (f KcpShareFields) ApplyToObj(obj map[string]any) {
	if f.HeaderType != "" && f.HeaderType != "none" {
		obj["type"] = f.HeaderType
	}
	SetStringField(obj, "path", f.Seed)
	SetIntField(obj, "mtu", f.MTU)
	SetIntField(obj, "tti", f.TTI)
}

func ExtractKcpShareFields(stream map[string]any) KcpShareFields {
	fields := KcpShareFields{HeaderType: "none"}

	if kcp, ok := stream["kcpSettings"].(map[string]any); ok {
		if header, ok := kcp["header"].(map[string]any); ok {
			if value, ok := header["type"].(string); ok && value != "" {
				fields.HeaderType = value
			}
		}
		if value, ok := kcp["seed"].(string); ok && value != "" {
			fields.Seed = value
		}
		if value, ok := ReadPositiveInt(kcp["mtu"]); ok {
			fields.MTU = value
		}
		if value, ok := ReadPositiveInt(kcp["tti"]); ok {
			fields.TTI = value
		}
	}

	for _, rawMask := range NormalizedFinalMaskUDPMasks(stream["finalmask"]) {
		mask, _ := rawMask.(map[string]any)
		if mask == nil {
			continue
		}
		if maskType, _ := mask["type"].(string); maskType != "mkcp-legacy" {
			continue
		}

		settings, _ := mask["settings"].(map[string]any)
		header, _ := settings["header"].(string)
		value, _ := settings["value"].(string)
		if header == "" {
			fields.Seed = value
			continue
		}
		if mapped, ok := kcpMaskToHeaderType[header]; ok {
			fields.HeaderType = mapped
		}
	}

	return fields
}

func ReadPositiveInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, number > 0
	case int32:
		return int(number), number > 0
	case int64:
		return int(number), number > 0
	case float32:
		parsed := int(number)
		return parsed, parsed > 0
	case float64:
		parsed := int(number)
		return parsed, parsed > 0
	default:
		return 0, false
	}
}

func SetStringParam(params map[string]string, key, value string) {
	if value == "" {
		delete(params, key)
		return
	}
	params[key] = value
}

func SetIntParam(params map[string]string, key string, value int) {
	if value <= 0 {
		delete(params, key)
		return
	}
	params[key] = fmt.Sprintf("%d", value)
}

func SetStringField(obj map[string]any, key, value string) {
	if value == "" {
		delete(obj, key)
		return
	}
	obj[key] = value
}

func SetIntField(obj map[string]any, key string, value int) {
	if value <= 0 {
		delete(obj, key)
		return
	}
	obj[key] = value
}

func ApplyFinalMaskParams(finalmask map[string]any, params map[string]string) {
	if fm, ok := MarshalFinalMask(finalmask); ok {
		params["fm"] = fm
	}
}

func ApplyFinalMaskObj(finalmask map[string]any, obj map[string]any) {
	if fm, ok := MarshalFinalMask(finalmask); ok {
		obj["fm"] = fm
	}
}

func MarshalFinalMask(finalmask map[string]any) (string, bool) {
	normalized := NormalizeFinalMask(finalmask)
	if !HasFinalMaskContent(normalized) {
		return "", false
	}
	b, err := json.Marshal(normalized)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return "", false
	}
	return string(b), true
}

func NormalizeFinalMask(finalmask map[string]any) map[string]any {
	tcpMasks := NormalizedFinalMaskTCPMasks(finalmask)
	udpMasks := NormalizedFinalMaskUDPMasks(finalmask)
	quicParams, hasQuicParams := finalmask["quicParams"].(map[string]any)

	if len(tcpMasks) == 0 && len(udpMasks) == 0 && !hasQuicParams {
		return nil
	}

	result := map[string]any{}
	if len(tcpMasks) > 0 {
		result["tcp"] = tcpMasks
	}
	if len(udpMasks) > 0 {
		result["udp"] = udpMasks
	}
	if hasQuicParams && len(quicParams) > 0 {
		result["quicParams"] = quicParams
	}
	return result
}

func NormalizedFinalMaskTCPMasks(value any) []any {
	finalmask, _ := value.(map[string]any)
	if finalmask == nil {
		return nil
	}
	rawMasks, _ := finalmask["tcp"].([]any)
	if len(rawMasks) == 0 {
		return nil
	}

	normalized := make([]any, 0, len(rawMasks))
	for _, rawMask := range rawMasks {
		mask, _ := rawMask.(map[string]any)
		if mask == nil {
			continue
		}
		maskType, _ := mask["type"].(string)
		if _, ok := validFinalMaskTCPTypes[maskType]; !ok || maskType == "" {
			continue
		}

		normalizedMask := map[string]any{"type": maskType}
		if settings, ok := mask["settings"].(map[string]any); ok && len(settings) > 0 {
			normalizedMask["settings"] = settings
		}
		normalized = append(normalized, normalizedMask)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func NormalizedFinalMaskUDPMasks(value any) []any {
	finalmask, _ := value.(map[string]any)
	if finalmask == nil {
		return nil
	}
	rawMasks, _ := finalmask["udp"].([]any)
	if len(rawMasks) == 0 {
		return nil
	}

	normalized := make([]any, 0, len(rawMasks))
	for _, rawMask := range rawMasks {
		mask, _ := rawMask.(map[string]any)
		if mask == nil {
			continue
		}
		maskType, _ := mask["type"].(string)
		if _, ok := validFinalMaskUDPTypes[maskType]; !ok || maskType == "" {
			continue
		}

		normalizedMask := map[string]any{"type": maskType}
		if settings, ok := mask["settings"].(map[string]any); ok && len(settings) > 0 {
			normalizedMask["settings"] = settings
		}
		normalized = append(normalized, normalizedMask)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func HasFinalMaskContent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return len(v) > 0
	case map[string]any:
		for _, item := range v {
			if HasFinalMaskContent(item) {
				return true
			}
		}
		return false
	case []any:
		return slices.ContainsFunc(v, HasFinalMaskContent)
	default:
		return true
	}
}

func SearchHost(headers any) string {
	data, _ := headers.(map[string]any)
	for k, v := range data {
		if strings.EqualFold(k, "host") {
			switch v.(type) {
			case []any:
				hosts, _ := v.([]any)
				if len(hosts) > 0 {
					h, _ := hosts[0].(string)
					return h
				}
				return ""
			case any:
				h, _ := v.(string)
				return h
			}
		}
	}

	return ""
}

func ParseHysteriaPacketSize(value string) string {
	minStr, maxStr, ok := strings.Cut(value, "-")
	if !ok || minStr == "" || maxStr == "" {
		return ""
	}
	for _, c := range minStr {
		if c < '0' || c > '9' {
			return ""
		}
	}
	for _, c := range maxStr {
		if c < '0' || c > '9' {
			return ""
		}
	}
	minVal, err1 := strconv.Atoi(minStr)
	maxVal, err2 := strconv.Atoi(maxStr)
	if err1 != nil || err2 != nil ||
		minVal < 1 || maxVal < minVal || maxVal > 2048 {
		return ""
	}
	return fmt.Sprintf("%d-%d", minVal, maxVal)
}
