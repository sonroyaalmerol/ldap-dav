package caldav

import (
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func WriteMultiStatus(w http.ResponseWriter, ms common.MultiStatus) {
	ms.XmlnsC = common.NSCalDAV
	ms.XmlnsCS = common.NSCS

	common.WriteMultiStatus(w, ms)
}
