package directory

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
