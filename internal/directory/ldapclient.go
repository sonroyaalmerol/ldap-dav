package directory

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/cache"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"

	"github.com/go-ldap/ldap/v3"
	"github.com/rs/zerolog"
)

type Directory interface {
	Close()
	BindUser(ctx context.Context, username, password string) (*User, error)
	LookupUserByAttr(ctx context.Context, attr, value string) (*User, error)
	UserGroupsACL(ctx context.Context, user *User) ([]GroupACL, error)
	IntrospectToken(ctx context.Context, token, url, authHeader string) (bool, string, error)
}

type LDAPClient struct {
	cfg    config.LDAPConfig
	logger zerolog.Logger
	conn   *ldap.Conn
	cache  *cache.Cache[string, []GroupACL]
}

func NewLDAPClient(cfg config.LDAPConfig, logger zerolog.Logger) (*LDAPClient, error) {
	l, err := dialLDAPAuto(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.BindDN != "" {
		if err := l.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			l.Close()
			return nil, err
		}
	}
	c := cache.New[string, []GroupACL](cfg.CacheTTL)
	return &LDAPClient{cfg: cfg, logger: logger, conn: l, cache: c}, nil
}

func (l *LDAPClient) Close() {
	if l.conn != nil {
		l.conn.Close()
	}
}

func (l *LDAPClient) BindUser(ctx context.Context, username, password string) (*User, error) {
	searchReq := ldap.NewSearchRequest(
		l.cfg.UserBaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, int(l.cfg.Timeout.Seconds()), false,
		fmt.Sprintf(l.cfg.UserFilter, ldap.EscapeFilter(username), ldap.EscapeFilter(username)),
		[]string{"dn", "uid", "cn", "displayName", "mail"},
		nil,
	)
	res, err := l.conn.SearchWithPaging(searchReq, 1)
	if err != nil || len(res.Entries) == 0 {
		return nil, errors.New("user not found")
	}
	entry := res.Entries[0]
	userDN := entry.DN

	userConn, err := dialLDAPAuto(l.cfg)
	if err != nil {
		return nil, err
	}
	defer userConn.Close()
	if err := userConn.Bind(userDN, password); err != nil {
		return nil, err
	}

	u := &User{
		UID:         firstNonEmpty(entry.GetAttributeValue("uid"), entry.GetAttributeValue("mail")),
		DN:          userDN,
		DisplayName: firstNonEmpty(entry.GetAttributeValue("displayName"), entry.GetAttributeValue("cn")),
		Mail:        entry.GetAttributeValue("mail"),
	}
	return u, nil
}

func (l *LDAPClient) LookupUserByAttr(ctx context.Context, attr, value string) (*User, error) {
	attr = safeAttr(attr)
	searchReq := ldap.NewSearchRequest(
		l.cfg.UserBaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, int(l.cfg.Timeout.Seconds()), false,
		fmt.Sprintf("(%s=%s)", attr, ldap.EscapeFilter(value)),
		[]string{"dn", "uid", "cn", "displayName", "mail"},
		nil,
	)
	res, err := l.conn.Search(searchReq)
	if err != nil || len(res.Entries) == 0 {
		return nil, errors.New("user not found")
	}
	e := res.Entries[0]
	return &User{
		UID:         firstNonEmpty(e.GetAttributeValue("uid"), e.GetAttributeValue("mail")),
		DN:          e.DN,
		DisplayName: firstNonEmpty(e.GetAttributeValue("displayName"), e.GetAttributeValue("cn")),
		Mail:        e.GetAttributeValue("mail"),
	}, nil
}

func (l *LDAPClient) UserGroupsACL(ctx context.Context, user *User) ([]GroupACL, error) {
	if v, ok := l.cache.Get(user.DN); ok {
		return v, nil
	}
	memFilter := fmt.Sprintf("(%s=%s)", safeAttr(l.cfg.MemberAttr), ldap.EscapeFilter(user.DN))
	search := ldap.NewSearchRequest(
		l.cfg.GroupBaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, int(l.cfg.Timeout.Seconds()), false,
		fmt.Sprintf("(&%s%s)", "(objectClass=groupOfNames)", memFilter),
		attrList(l.cfg),
		nil,
	)
	res, err := l.conn.Search(search)
	if err != nil {
		return nil, err
	}
	var acls []GroupACL
	for _, e := range res.Entries {
		if l.cfg.BindingsAttr != "" {
			for _, line := range e.GetAttributeValues(l.cfg.BindingsAttr) {
				acl := parseBindingLine(line)
				if acl.CalendarID != "" {
					acls = append(acls, acl)
				}
			}
		} else {
			cals := e.GetAttributeValues(l.cfg.CalendarIDsAttr)
			privs := e.GetAttributeValues(l.cfg.PrivilegesAttr)
			for _, cal := range cals {
				acl := privilegesFromList(cal, privs)
				acls = append(acls, acl)
			}
		}
	}
	l.cache.Set(user.DN, acls, time.Now().Add(l.cfg.CacheTTL))
	return acls, nil
}

func (l *LDAPClient) IntrospectToken(ctx context.Context, token, url, authHeader string) (bool, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader("token="+ldap.EscapeFilter(token)))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false, "", nil
	}
	var out struct {
		Active bool   `json:"active"`
		Sub    string `json:"sub"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, "", err
	}
	return out.Active, out.Sub, nil
}

func privilegesFromList(calID string, privs []string) GroupACL {
	m := map[string]bool{}
	for _, p := range privs {
		m[strings.ToLower(strings.TrimSpace(p))] = true
	}
	return GroupACL{
		CalendarID:   calID,
		Read:         m["read"],
		WriteProps:   m["edit"] || m["writeprops"] || m["write-properties"],
		WriteContent: m["write"] || m["writecontent"] || m["write-content"],
		Bind:         m["create"] || m["bind"],
		Unbind:       m["delete"] || m["unbind"],
	}
}

func parseBindingLine(s string) GroupACL {
	acl := GroupACL{}
	parts := strings.Split(s, ";")
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v := strings.TrimSpace(kv[1])
		switch k {
		case "calendar-id":
			acl.CalendarID = v
		case "priv", "privileges":
			for _, t := range strings.Split(v, ",") {
				switch strings.ToLower(strings.TrimSpace(t)) {
				case "read":
					acl.Read = true
				case "edit", "writeprops", "write-properties":
					acl.WriteProps = true
				case "write", "writecontent", "write-content":
					acl.WriteContent = true
				case "bind", "create":
					acl.Bind = true
				case "unbind", "delete":
					acl.Unbind = true
				}
			}
		}
	}
	return acl
}

func attrList(cfg config.LDAPConfig) []string {
	attrs := []string{"dn", "cn", cfg.MemberAttr}
	if cfg.BindingsAttr != "" {
		attrs = append(attrs, cfg.BindingsAttr)
	} else {
		attrs = append(attrs, cfg.CalendarIDsAttr, cfg.PrivilegesAttr)
	}
	return attrs
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func safeAttr(a string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, a)
}

func dialLDAPAuto(cfg config.LDAPConfig) (*ldap.Conn, error) {
	u := cfg.URL
	useStartTLS := false
	if strings.HasPrefix(strings.ToLower(u), "ldap://") {
		useStartTLS = true
	}

	tlsConfig := &tls.Config{}

	hostPort := strings.TrimPrefix(strings.TrimPrefix(u, "ldaps://"), "ldap://")
	host, _, err := net.SplitHostPort(hostPort)
	if err == nil && host != "" {
		tlsConfig.ServerName = host
	}
	if cfg.InsecureSkipVerify { // allow plain or self-signed in tests/dev if cfg supports it
		tlsConfig.InsecureSkipVerify = true
	}

	conn, err := ldap.DialURL(u)
	if err != nil {
		return nil, err
	}

	if useStartTLS {
		if !cfg.RequireTLS {
			if err := conn.StartTLS(tlsConfig); err != nil {
				return conn, nil
			}
			return conn, nil
		}
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}

	return conn, nil
}
