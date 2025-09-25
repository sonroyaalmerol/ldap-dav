package directory

type Addressbook struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
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
	VCardData    string // Raw vCard data
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
