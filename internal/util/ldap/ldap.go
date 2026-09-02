package ldaputil

import (
	"crypto/tls"
	"fmt"
	"net"
	"slices"
	"time"

	"github.com/go-ldap/ldap/v3"
)

type Config struct {
	Host               string
	Port               int
	UseTLS             bool
	InsecureSkipVerify bool
	BindDN             string
	Password           string
	BaseDN             string
	UserFilter         string
	UserAttr           string
	// AdminFilter additionally constrains who may authenticate as a PANEL
	// administrator. A complete parenthesised filter, ANDed with UserFilter.
	AdminFilter string
	FlagField   string
	TruthyVals  []string
	Invert      bool
}

func tlsConfig(cfg Config) *tls.Config {
	return &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}
}

/*
dialTimeout bounds the connection AND every request on it. DialURL alone bounds
neither, so a directory that accepts the TCP connection and never answers hung
the sync goroutine and its socket -- once a minute, forever, until the panel
ran out of file descriptors.
*/
var dialTimeout = 15 * time.Second

func dial(cfg Config) (*ldap.Conn, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	scheme := "ldap"
	opts := []ldap.DialOpt{ldap.DialWithDialer(&net.Dialer{Timeout: dialTimeout})}
	if cfg.UseTLS {
		scheme = "ldaps"
		opts = append(opts, ldap.DialWithTLSConfig(tlsConfig(cfg)))
	}

	conn, err := ldap.DialURL(fmt.Sprintf("%s://%s", scheme, addr), opts...)
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(dialTimeout)
	return conn, nil
}

// FetchVlessFlags returns map[email]enabled
/*
ldapSearchPageSize is a page, not a cap: SearchWithPaging keeps asking until the
directory says there is no more, so this only sets how many come back per round
trip. Active Directory's own MaxPageSize default is 1000.
*/
const ldapSearchPageSize = 500

func FetchVlessFlags(cfg Config) (map[string]bool, error) {
	conn, err := dial(cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.Password); err != nil {
			return nil, err
		}
	}

	if cfg.UserFilter == "" {
		cfg.UserFilter = "(objectClass=person)"
	}
	if cfg.UserAttr == "" {
		cfg.UserAttr = "mail"
	}
	// if field not set we fallback to legacy vless_enabled
	if cfg.FlagField == "" {
		cfg.FlagField = "vless_enabled"
	}

	req := ldap.NewSearchRequest(
		cfg.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		cfg.UserFilter,
		[]string{cfg.UserAttr, cfg.FlagField},
		nil,
	)

	// Paged, or a directory enforcing MaxPageSize answers Size Limit Exceeded and
	// the whole sync errors out — no flags, no creates, no deletes, ever.
	res, err := conn.SearchWithPaging(req, ldapSearchPageSize)
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool, len(res.Entries))
	for _, e := range res.Entries {
		user := e.GetAttributeValue(cfg.UserAttr)
		if user == "" {
			continue
		}
		val := e.GetAttributeValue(cfg.FlagField)
		enabled := slices.Contains(cfg.TruthyVals, val)
		if cfg.Invert {
			enabled = !enabled
		}
		result[user] = enabled
	}
	return result, nil
}

// AuthenticateUser searches user by cfg.UserAttr and attempts to bind with provided password.
func AuthenticateUser(cfg Config, username, password string) (bool, error) {
	conn, err := dial(cfg)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	// Optional initial bind for search
	if cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.Password); err != nil {
			return false, err
		}
	}

	if cfg.UserFilter == "" {
		cfg.UserFilter = "(objectClass=person)"
	}
	if cfg.UserAttr == "" {
		cfg.UserAttr = "mail"
	}

	// The admin filter is ANDed in, never substituted: an account must satisfy
	// the directory's own user filter AND whatever authorises it to administer.
	filter := fmt.Sprintf("(&%s(%s=%s)%s)", cfg.UserFilter, cfg.UserAttr, ldap.EscapeFilter(username), cfg.AdminFilter)
	req := ldap.NewSearchRequest(
		cfg.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false,
		filter,
		[]string{"dn"},
		nil,
	)
	// Not paged, unlike the sync's search: this asks for one user with a size
	// limit of 1, so no page limit can bite and paging alongside one is refused
	// by some servers.
	res, err := conn.Search(req)
	if err != nil {
		return false, err
	}
	if len(res.Entries) == 0 {
		return false, nil
	}
	userDN := res.Entries[0].DN
	// Try to bind as the user
	if err := conn.Bind(userDN, password); err != nil {
		return false, nil
	}
	return true, nil
}
