package service

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
)

func parseTokenNetworks(value string) ([]netip.Prefix, error) {
	result := []netip.Prefix{}
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if address, err := netip.ParseAddr(entry); err == nil {
			address = address.Unmap()
			result = append(result, netip.PrefixFrom(address, address.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid token source IP or CIDR: %q", entry)
		}
		result = append(result, prefix.Masked())
	}
	if len(result) > 64 {
		return nil, errors.New("a token may have at most 64 source networks")
	}
	return result, nil
}

// CheckAPITokenSource runs after bearer authentication, using the router's trusted ClientIP.
func CheckAPITokenSource(presented, source string) error {
	var token model.ApiToken
	if err := database.GetDB().Where("token = ?", crypto.HashTokenSHA256(presented)).First(&token).Error; err != nil {
		return err
	}
	setting, err := (&SettingService{}).getSetting("apiTokenAllowedIPs:" + strconv.Itoa(token.Id))
	if database.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	networks, err := parseTokenNetworks(setting.Value)
	if err != nil {
		return err
	}
	if len(networks) == 0 {
		return nil
	}
	address, err := netip.ParseAddr(source)
	if err != nil {
		return errors.New("token source address is not a valid IP")
	}
	for _, network := range networks {
		if network.Contains(address.Unmap()) {
			return nil
		}
	}
	return errors.New("token is not permitted from this source IP")
}

func NormalizeTokenSourceNetworks(value string) (string, error) {
	networks, err := parseTokenNetworks(value)
	if err != nil {
		return "", err
	}
	values := make([]string, len(networks))
	for i, prefix := range networks {
		values[i] = prefix.String()
	}
	return strings.Join(values, ","), nil
}
