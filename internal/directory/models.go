package directory

type User struct {
	UID         string
	DN          string
	DisplayName string
	Mail        string
}

type GroupACL struct {
	CalendarID string
	// privilege bits
	Read         bool
	WriteProps   bool // edit
	WriteContent bool // event body write
	Bind         bool // create
	Unbind       bool // delete
}

type Group struct {
	CN      string
	DN      string
	Members []string // DNs or UIDs
	ACLs    []GroupACL
}
