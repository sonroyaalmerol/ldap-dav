package directory

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/sonroyaalmerol/ldap-dav/internal/cache"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
)

type LDAPContactClient struct {
	cfg     config.LDAPAddressbookFilter
	tlsBase config.LDAPConfig
	conn    *ldap.Conn
	cache   *cache.Cache[string, []Contact]
}

func NewLDAPContactClient(filterCfg config.LDAPAddressbookFilter, base config.LDAPConfig) (*LDAPContactClient, error) {
	conn, err := dialLDAPFromFilter(filterCfg)
	if err != nil {
		return nil, err
	}
	return &LDAPContactClient{
		cfg:     filterCfg,
		tlsBase: base,
		conn:    conn,
		cache:   cache.New[string, []Contact](base.CacheTTL),
	}, nil
}

func (c *LDAPContactClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func dialLDAPFromFilter(f config.LDAPAddressbookFilter) (*ldap.Conn, error) {
	u := strings.TrimSpace(f.URL)
	isLDAPS := strings.HasPrefix(strings.ToLower(u), "ldaps://")
	isLDAP := strings.HasPrefix(strings.ToLower(u), "ldap://")
	if !isLDAP && !isLDAPS {
		return nil, errors.New("URL must start with ldap:// or ldaps://")
	}
	if isLDAPS {
		tlsConfig := &tls.Config{InsecureSkipVerify: f.InsecureSkipVerify}
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
	if f.RequireTLS {
		tlsConfig := &tls.Config{InsecureSkipVerify: f.InsecureSkipVerify}
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
	if f.BindDN != "" {
		if err := conn.Bind(f.BindDN, f.BindPassword); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

func (c *LDAPContactClient) ListAddressbooks(ctx context.Context) ([]Addressbook, error) {
	if !c.cfg.Enabled {
		return nil, nil
	}
	return []Addressbook{{
		ID:          "ldap_" + c.cfg.URI,
		Name:        c.cfg.Name,
		Description: c.cfg.Description,
		Enabled:     true,
		URI:         "ldap_" + c.cfg.URI,
	}}, nil
}

func (c *LDAPContactClient) attrsForFilter() []string {
	set := map[string]struct{}{
		"dn": {},
	}
	add := func(s string) {
		if s != "" {
			set[s] = struct{}{}
		}
	}
	add(c.cfg.MapUID)
	add(c.cfg.MapDisplayName)
	add(c.cfg.MapFirstName)
	add(c.cfg.MapLastName)
	add(c.cfg.MapEmail)
	add(c.cfg.MapPhone)
	add(c.cfg.MapOrganization)
	add(c.cfg.MapTitle)
	add(c.cfg.MapPhoto)

	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func (c *LDAPContactClient) ListContacts(ctx context.Context) ([]Contact, error) {
	if v, ok := c.cache.Get("all"); ok {
		return v, nil
	}
	search := ldap.NewSearchRequest(
		c.cfg.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 15, false,
		c.cfg.Filter,
		c.attrsForFilter(),
		nil,
	)
	res, err := c.conn.Search(search)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(res.Entries))
	for _, e := range res.Entries {
		out = append(out, c.mapEntry(e))
	}
	c.cache.Set("all", out, time.Now().Add(30*time.Second))
	return out, nil
}

func (c *LDAPContactClient) GetContact(ctx context.Context, uid string) (*Contact, error) {
	search := ldap.NewSearchRequest(
		c.cfg.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 15, false,
		fmt.Sprintf("(%s=%s)", c.cfg.MapUID, uid),
		c.attrsForFilter(),
		nil,
	)
	res, err := c.conn.Search(search)
	if err != nil {
		return nil, err
	}

	if len(res.Entries) != 1 {
		return nil, ldap.NewError(ldap.ErrorFilterCompile, errors.New("not found"))
	}

	out := c.mapEntry(res.Entries[0])
	return &out, nil
}

func (c *LDAPContactClient) mapEntry(e *ldap.Entry) Contact {
	get := func(attr string) string {
		if attr == "" {
			return ""
		}
		return e.GetAttributeValue(attr)
	}
	gets := func(attr string) []string {
		if attr == "" {
			return nil
		}
		vals := e.GetAttributeValues(attr)
		// Filter empty
		out := make([]string, 0, len(vals))
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				out = append(out, v)
			}
		}
		return out
	}

	contact := Contact{
		ID:           get(c.cfg.MapUID),
		DN:           e.DN,
		DisplayName:  get(c.cfg.MapDisplayName),
		FirstName:    get(c.cfg.MapFirstName),
		LastName:     get(c.cfg.MapLastName),
		Email:        gets(c.cfg.MapEmail),
		Phone:        gets(c.cfg.MapPhone),
		Organization: get(c.cfg.MapOrganization),
		Title:        get(c.cfg.MapTitle),
	}

	contact.VCardData = c.generateVCard(contact)

	return contact
}

func (c *LDAPContactClient) generateVCard(contact Contact) string {
	var vcard strings.Builder

	vcard.WriteString("BEGIN:VCARD\r\n")
	vcard.WriteString("VERSION:3.0\r\n")

	if contact.DisplayName != "" {
		vcard.WriteString(fmt.Sprintf("FN:%s\r\n", contact.DisplayName))
	}

	if contact.FirstName != "" || contact.LastName != "" {
		vcard.WriteString(fmt.Sprintf("N:%s;%s;;;\r\n", contact.LastName, contact.FirstName))
	}

	for _, email := range contact.Email {
		if email != "" {
			vcard.WriteString(fmt.Sprintf("EMAIL:%s\r\n", email))
		}
	}

	for _, phone := range contact.Phone {
		if phone != "" {
			vcard.WriteString(fmt.Sprintf("TEL:%s\r\n", phone))
		}
	}

	if contact.Organization != "" {
		vcard.WriteString(fmt.Sprintf("ORG:%s\r\n", contact.Organization))
	}

	if contact.Title != "" {
		vcard.WriteString(fmt.Sprintf("TITLE:%s\r\n", contact.Title))
	}

	if contact.ID != "" {
		vcard.WriteString(fmt.Sprintf("UID:%s\r\n", contact.ID))
	}

	vcard.WriteString("END:VCARD\r\n")

	return vcard.String()
}
