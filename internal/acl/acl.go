package acl

import (
	"context"

	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
)

type Provider interface {
	// Compute effective privileges for user on a given calendar ID from LDAP group ACLs
	Effective(ctx context.Context, user *directory.User, calendarID string) (Effective, error)
	// List calendars the user can at least read
	VisibleCalendars(ctx context.Context, user *directory.User) (map[string]Effective, error)
}

type LDAPACL struct {
	Dir directory.Directory
}

func NewLDAPACL(dir directory.Directory) *LDAPACL {
	return &LDAPACL{Dir: dir}
}

func (p *LDAPACL) Effective(ctx context.Context, user *directory.User, calendarID string) (Effective, error) {
	acls, err := p.Dir.UserGroupsACL(ctx, user)
	if err != nil {
		return Effective{}, err
	}
	e := Effective{}
	for _, a := range acls {
		if a.CalendarID != calendarID {
			continue
		}
		if a.Read {
			e.Read = true
		}
		if a.WriteProps {
			e.WriteProps = true
		}
		if a.WriteContent {
			e.WriteContent = true
		}
		if a.Bind {
			e.Bind = true
		}
		if a.Unbind {
			e.Unbind = true
		}
	}
	return e, nil
}

func (p *LDAPACL) VisibleCalendars(ctx context.Context, user *directory.User) (map[string]Effective, error) {
	acls, err := p.Dir.UserGroupsACL(ctx, user)
	if err != nil {
		return nil, err
	}
	m := map[string]Effective{}
	for _, a := range acls {
		e := m[a.CalendarID]
		if a.Read {
			e.Read = true
		}
		if a.WriteProps {
			e.WriteProps = true
		}
		if a.WriteContent {
			e.WriteContent = true
		}
		if a.Bind {
			e.Bind = true
		}
		if a.Unbind {
			e.Unbind = true
		}
		m[a.CalendarID] = e
	}
	return m, nil
}
