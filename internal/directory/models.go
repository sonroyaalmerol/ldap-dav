package directory

import "context"

type ContactDirectory interface {
	ListAddressbooks(ctx context.Context) ([]Addressbook, error)
	ListContacts(ctx context.Context, propFilter []string) ([]Contact, error)
	GetContact(ctx context.Context, uid string) (*Contact, error)
}

type Addressbook struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	URI         string
}

type Contact struct {
	ID           string
	DN           string
	DisplayName  string
	FirstName    string
	LastName     string
	Email        []string
	Phone        []string
	Organization string
	Title        string
	VCardData    string
}

type User struct {
	UID         string
	DN          string
	DisplayName string
	Mail        string
}

type GroupACL struct {
	CalendarID                  string
	Read                        bool
	WriteProps                  bool
	WriteContent                bool
	Bind                        bool
	Unbind                      bool
	Unlock                      bool
	ReadACL                     bool
	ReadCurrentUserPrivilegeSet bool
}

type Group struct {
	CN      string
	DN      string
	Members []string // DNs or UIDs
	ACLs    []GroupACL
}
