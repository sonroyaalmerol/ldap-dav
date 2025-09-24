package acl

type Priv uint32

const (
	PrivRead Priv = 1 << iota
	PrivWriteProps
	PrivWriteContent
	PrivBind
	PrivUnbind
	PrivAll = PrivRead | PrivWriteProps | PrivWriteContent | PrivBind | PrivUnbind
)

type Effective struct {
	Read                        bool
	WriteProps                  bool
	WriteContent                bool
	Bind                        bool
	Unbind                      bool
	Unlock                      bool
	ReadACL                     bool
	ReadCurrentUserPrivilegeSet bool
}

func (e Effective) CanRead() bool {
	return e.Read
}

func (e Effective) CanWrite() bool {
	return e.WriteProps || e.WriteContent
}

func (e Effective) CanCreate() bool {
	return e.Bind
}

func (e Effective) CanDelete() bool {
	return e.Unbind
}

func (e Effective) CanUnlock() bool {
	return e.Unlock
}

func (e Effective) CanReadACL() bool {
	return e.ReadACL
}

func (e Effective) CanReadCurrentUserPrivilegeSet() bool {
	return e.ReadCurrentUserPrivilegeSet || e.Read
}

func (e Effective) CanWriteACL() bool {
	return false
}
