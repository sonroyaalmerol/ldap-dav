package directory

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
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
	cfg          config.LDAPConfig
	logger       zerolog.Logger
	conn         *ldap.Conn
	cache        *cache.Cache[string, []GroupACL]
	contactCache *cache.Cache[string, []Contact]
}

func NewLDAPClient(cfg config.LDAPConfig, logger zerolog.Logger) (*LDAPClient, error) {
	l, err := dialLDAPAuto(cfg)
	if err != nil {
		logger.Error().Err(err).Str("url", cfg.URL).Msg("failed to dial LDAP")
		return nil, err
	}
	if cfg.BindDN != "" {
		if err := l.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			logger.Error().Err(err).Str("bind_dn", cfg.BindDN).Msg("initial bind failed")
			l.Close()
			return nil, err
		}
	}
	aclCache := cache.New[string, []GroupACL](cfg.CacheTTL)
	contactCache := cache.New[string, []Contact](cfg.CacheTTL)
	return &LDAPClient{
		cfg:          cfg,
		logger:       logger,
		conn:         l,
		cache:        aclCache,
		contactCache: contactCache,
	}, nil
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
		userAttrList(l.cfg),
		nil,
	)
	res, err := l.conn.SearchWithPaging(searchReq, 1)
	if err != nil {
		l.logger.Error().Err(err).
			Str("user_base_dn", l.cfg.UserBaseDN).
			Str("username", username).
			Msg("LDAP search failed in BindUser")
		return nil, errors.New("user not found")
	}
	if len(res.Entries) == 0 {
		l.logger.Debug().Str("username", username).Msg("user not found in BindUser search")
		return nil, errors.New("user not found")
	}
	entry := res.Entries[0]
	userDN := entry.DN

	userConn, err := dialLDAPAuto(l.cfg)
	if err != nil {
		l.logger.Error().Err(err).Msg("failed to dial LDAP for user bind")
		return nil, err
	}
	defer userConn.Close()
	if err := userConn.Bind(userDN, password); err != nil {
		l.logger.Debug().Err(err).Str("user_dn", userDN).Msg("user bind failed")
		return nil, err
	}

	u := &User{
		UID:         firstNonEmpty(entry.GetAttributeValue(l.cfg.TokenUserAttr), entry.GetAttributeValue("mail")),
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
	if err != nil {
		l.logger.Error().Err(err).
			Str("attr", attr).
			Str("value", value).
			Str("user_base_dn", l.cfg.UserBaseDN).
			Msg("LDAP search failed in LookupUserByAttr")
		return nil, errors.New("user not found")
	}
	if len(res.Entries) == 0 {
		l.logger.Debug().Str("attr", attr).Str("value", value).Msg("user not found in LookupUserByAttr")
		return nil, errors.New("user not found")
	}
	e := res.Entries[0]
	return &User{
		UID:         firstNonEmpty(e.GetAttributeValue(l.cfg.TokenUserAttr), e.GetAttributeValue("mail")),
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
		l.logger.Error().Err(err).
			Str("group_base_dn", l.cfg.GroupBaseDN).
			Str("member_attr", l.cfg.MemberAttr).
			Str("user_dn", user.DN).
			Msg("LDAP search failed in UserGroupsACL")
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
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader("token="+token))
	if err != nil {
		l.logger.Error().Err(err).Msg("failed to build introspection request")
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.logger.Error().Err(err).Str("url", url).Msg("introspection HTTP request failed")
		return false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		l.logger.Debug().Int("status", resp.StatusCode).Msg("token introspection not active")
		return false, "", nil
	}
	var out struct {
		Active bool   `json:"active"`
		Sub    string `json:"sub"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		l.logger.Error().Err(err).Msg("failed to decode introspection response")
		return false, "", err
	}

	username := strings.SplitN(out.Sub, "@", 2)[0]
	return out.Active, username, nil
}

func privilegesFromList(calID string, privs []string) GroupACL {
	m := map[string]bool{}
	for _, p := range privs {
		m[strings.ToLower(strings.TrimSpace(p))] = true
	}
	return GroupACL{
		CalendarID:                  calID,
		Read:                        m["read"],
		WriteProps:                  m["edit"] || m["writeprops"] || m["write-properties"],
		WriteContent:                m["write"] || m["writecontent"] || m["write-content"],
		Bind:                        m["create"] || m["bind"],
		Unbind:                      m["delete"] || m["unbind"],
		Unlock:                      m["unlock"],
		ReadACL:                     m["readacl"] || m["read-acl"],
		ReadCurrentUserPrivilegeSet: m["readprivs"] || m["read-current-user-privilege-set"] || m["read-privileges"],
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
				case "unlock":
					acl.Unlock = true
				case "readacl", "read-acl":
					acl.ReadACL = true
				case "readprivs", "read-current-user-privilege-set", "read-privileges":
					acl.ReadCurrentUserPrivilegeSet = true
				}
			}
		}
	}
	return acl
}

func userAttrList(cfg config.LDAPConfig) []string {
	attrs := []string{"dn", "displayName", "mail", "uid", "cn"}
	if cfg.TokenUserAttr != "" && !slices.Contains(attrs, cfg.TokenUserAttr) {
		attrs = append(attrs, cfg.TokenUserAttr)
	}
	return attrs
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
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-' || r == '_' {
			return r
		}
		return -1
	}, a)
}

func dialLDAPAuto(cfg config.LDAPConfig) (*ldap.Conn, error) {
	u := strings.TrimSpace(cfg.URL)
	if u == "" {
		return nil, errors.New("LDAP URL is empty")
	}

	isLDAPS := strings.HasPrefix(strings.ToLower(u), "ldaps://")
	isLDAP := strings.HasPrefix(strings.ToLower(u), "ldap://")

	if !isLDAP && !isLDAPS {
		return nil, errors.New("URL must start with ldap:// or ldaps://")
	}

	if isLDAPS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		}
		hostPort := strings.TrimPrefix(u, "ldaps://")
		if host, _, err := net.SplitHostPort(hostPort); err == nil && host != "" {
			tlsConfig.ServerName = host
		} else {
			tlsConfig.ServerName = hostPort
		}
		return ldap.DialURL(u, ldap.DialWithTLSConfig(tlsConfig))
	}

	conn, err := ldap.DialURL(u)
	if err != nil {
		return nil, err
	}

	if cfg.RequireTLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		}
		hostPort := strings.TrimPrefix(u, "ldap://")
		if host, _, err := net.SplitHostPort(hostPort); err == nil && host != "" {
			tlsConfig.ServerName = host
		} else {
			tlsConfig.ServerName = hostPort
		}
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			return nil, fmt.Errorf("StartTLS failed: %w", err)
		}
	}

	return conn, nil
}
