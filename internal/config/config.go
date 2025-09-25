package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type HTTPConfig struct {
	Addr        string
	BasePath    string
	MaxICSBytes int64
	MaxVCFBytes int64
}

type LDAPAddressbookFilter struct {
	Name        string
	BaseDN      string
	Filter      string
	Enabled     bool
	Description string
}

type LDAPConfig struct {
	URL                string
	BindDN             string
	BindPassword       string
	UserBaseDN         string
	GroupBaseDN        string
	UserFilter         string
	GroupFilter        string
	MemberAttr         string
	CalendarIDsAttr    string
	PrivilegesAttr     string
	BindingsAttr       string
	TokenUserAttr      string
	EnableNestedGroups bool
	MaxGroupDepth      int
	Timeout            time.Duration
	CacheTTL           time.Duration
	InsecureSkipVerify bool
	RequireTLS         bool
	AddressbookFilters []LDAPAddressbookFilter
}

type AuthConfig struct {
	EnableBasic          bool
	EnableBearer         bool
	JWKSURL              string
	Issuer               string
	Audience             string
	AllowOpaque          bool
	IntrospectURL        string
	IntrospectAuthHeader string
}

type StorageConfig struct {
	Type        string
	PostgresURL string
	FileRoot    string
}

type Config struct {
	Timezone string
	HTTP     HTTPConfig
	LDAP     LDAPConfig
	Auth     AuthConfig
	Storage  StorageConfig
	ICS      ICSConfig
	LogLevel string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadAddressbookFilters() []LDAPAddressbookFilter {
	var filters []LDAPAddressbookFilter

	// Look for LDAP_ADDRESSBOOK_FILTER_0, LDAP_ADDRESSBOOK_FILTER_1, etc.
	for i := 0; i < 100; i++ { // reasonable limit to prevent infinite loop
		prefix := fmt.Sprintf("LDAP_ADDRESSBOOK_FILTER_%d", i)

		if os.Getenv(prefix) == "" && os.Getenv(prefix+"_NAME") == "" {
			if len(filters) == 0 {
				continue
			}
			break
		}

		filter := LDAPAddressbookFilter{
			Name:        getenv(prefix+"_NAME", fmt.Sprintf("Addressbook_%d", i)),
			BaseDN:      getenv(prefix+"_BASE_DN", getenv("LDAP_USER_BASE_DN", "")),
			Filter:      getenv(prefix+"_FILTER", "(objectClass=person)"),
			Enabled:     getenv(prefix+"_ENABLED", "true") == "true",
			Description: getenv(prefix+"_DESCRIPTION", ""),
		}

		// If NAME or BASE_DN is explicitly set, or if the base var exists, include this filter
		if os.Getenv(prefix+"_NAME") != "" || os.Getenv(prefix+"_BASE_DN") != "" || os.Getenv(prefix) != "" {
			filters = append(filters, filter)
		}
	}

	return filters
}

func Load() (*Config, error) {
	maxICS := func() int64 {
		v := getenv("HTTP_MAX_ICS_BYTES", "1048576")
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 1 << 20
		}
		return n
	}()

	maxVCF := func() int64 {
		v := getenv("HTTP_MAX_VCF_BYTES", "1048576")
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 1 << 20
		}
		return n
	}()

	return &Config{
		HTTP: HTTPConfig{
			Addr:        getenv("HTTP_ADDR", ":8080"),
			BasePath:    getenv("HTTP_BASE_PATH", "/dav"),
			MaxICSBytes: maxICS,
			MaxVCFBytes: maxVCF,
		},
		LDAP: LDAPConfig{
			URL:                getenv("LDAP_URL", "ldap://localhost:389"),
			BindDN:             getenv("LDAP_BIND_DN", ""),
			BindPassword:       getenv("LDAP_BIND_PASSWORD", ""),
			UserBaseDN:         getenv("LDAP_USER_BASE_DN", ""),
			GroupBaseDN:        getenv("LDAP_GROUP_BASE_DN", ""),
			UserFilter:         getenv("LDAP_USER_FILTER", "(|(uid=%s)(mail=%s))"),
			GroupFilter:        getenv("LDAP_GROUP_FILTER", "(cn=%s)"),
			MemberAttr:         getenv("LDAP_MEMBER_ATTR", "member"),
			CalendarIDsAttr:    getenv("LDAP_CAL_IDS_ATTR", "caldavCalendars"),
			PrivilegesAttr:     getenv("LDAP_PRIVS_ATTR", "caldavPrivileges"),
			BindingsAttr:       getenv("LDAP_BINDINGS_ATTR", "caldavBindings"),
			TokenUserAttr:      getenv("LDAP_TOKEN_USER_ATTR", "uid"),
			EnableNestedGroups: getenv("LDAP_NESTED", "false") == "true",
			InsecureSkipVerify: getenv("LDAP_SKIP_VERIFY", "false") == "true",
			RequireTLS:         getenv("LDAP_REQUIRE_TLS", "false") == "true",
			MaxGroupDepth:      3,
			Timeout:            5 * time.Second,
			CacheTTL:           60 * time.Second,
			AddressbookFilters: loadAddressbookFilters(),
		},
		Auth: AuthConfig{
			EnableBasic:          getenv("AUTH_BASIC", "true") == "true",
			EnableBearer:         getenv("AUTH_BEARER", "true") == "true",
			JWKSURL:              getenv("AUTH_JWKS_URL", ""),
			Issuer:               getenv("AUTH_ISSUER", ""),
			Audience:             getenv("AUTH_AUDIENCE", ""),
			AllowOpaque:          getenv("AUTH_ALLOW_OPAQUE", "false") == "true",
			IntrospectURL:        getenv("AUTH_INTROSPECT_URL", ""),
			IntrospectAuthHeader: getenv("AUTH_INTROSPECT_AUTH", ""),
		},
		Storage: StorageConfig{
			Type:        getenv("STORAGE_TYPE", "postgres"), // postgres | filestore
			PostgresURL: getenv("PG_URL", "postgres://postgres:postgres@localhost:5432/caldav?sslmode=disable"),
			FileRoot:    getenv("FILE_ROOT", "./data"),
		},
		ICS: ICSConfig{
			CompanyName: getenv("ICS_COMPANY_NAME", "LDAP DAV"),
			ProductName: getenv("ICS_PRODUCT_NAME", "CalDAV"),
			Version:     getenv("ICS_VERSION", "1.0.0"),
			Language:    getenv("ICS_LANGUAGE", "EN"),
		},
		Timezone: getenv("TZ", "UTC"),
		LogLevel: getenv("LOG_LEVEL", "info"),
	}, nil
}
