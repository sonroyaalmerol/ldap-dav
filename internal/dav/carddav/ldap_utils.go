package carddav

import (
	"fmt"
	"hash/fnv"

	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
)

func computeStableETag(ct *directory.Contact) string {
	h := fnv.New64a()
	write := func(s string) { _, _ = h.Write([]byte(s)); _, _ = h.Write([]byte{0}) }
	write(ct.DN)
	write(ct.DisplayName)
	write(ct.FirstName)
	write(ct.LastName)
	for _, e := range ct.Email {
		write(e)
	}
	for _, p := range ct.Phone {
		write(p)
	}
	write(ct.Organization)
	write(ct.Title)
	return fmt.Sprintf("%x", h.Sum(nil))
}
