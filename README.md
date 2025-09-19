# ldap-dav

A CalDAV/WebDAV server in Go with LDAP-driven ACLs and sharing. Designed for minimal local state: users and groups are read from LDAP on demand. Supports Basic and Bearer auth, .well-known discovery, and auto-listing of shared calendars.

Key features
- CalDAV (RFC 4791) on top of WebDAV (RFC 4918)
- Sharing and ACLs via LDAP groups only
  - Each LDAP group declares which calendars it grants access to and which privileges (read, edit/write-props, write-content, create/bind, delete/unbind)
- Users/groups are not replicated; resolved on-demand with short caching
- Auth: HTTP Basic (LDAP bind) and Bearer (JWT via JWKS; opaque via RFC 7662 introspection optional)
  - JWKS keys are cached; token verification results are also cached briefly
- Discovery: /.well-known/caldav, principals, calendar-home-set
- Auto-list shared calendars for a user based on LDAP group ACLs
- WebDAV Sync (RFC 6578) with incremental tokens and change log (supports paging/limits)
- REPORTs:
  - calendar-query and calendar-multiget returning calendar-data, getetag, and getlastmodified
  - free-busy-query (basic VFREEBUSY generation; no recurrence expansion yet)
- iCalendar components: VEVENT, VTODO, VJOURNAL
- Storage: PostgreSQL (calendars, objects, change log) with recommended indexes
- Read-only WebDAV ACL properties surfaced on collections to reflect effective privileges
- Configurable max ICS upload size
- HEAD is supported everywhere GET is, returning headers without body

Project layout
- cmd/ldap-dav/main.go
- internal/
  - auth/        Basic/Bearer middlewares (with JWKS + token caches)
  - acl/         Effective privileges (from LDAP groups)
  - cache/       Small TTL cache for LDAP and auth
  - config/      Environment-based configuration
  - dav/
    - handler.go       DAV entry, routing switch, helpers
    - propfind.go      PROPFIND (principals, home, collections, objects)
    - reports.go       REPORTs (calendar-query, calendar-multiget, sync-collection, free-busy)
    - methods.go       GET/HEAD/PUT/DELETE/MKCOL/PROPPATCH/ACL
    - discovery.go     .well-known and OPTIONS
    - props.go         XML property structs and serializer
  - directory/   LDAP client (users, groups, ACL attributes, optional introspection)
  - logging/     zerolog setup
  - router/      HTTP router setup (chi)
  - storage/
    - storage.go    Store interface
    - postgres/     PostgreSQL impl + schema.sql
- pkg/
  - ical/        ICS normalization helper (emersion/go-ical)

Quick start

1) Requirements
- Go 1.22+
- PostgreSQL 13+
- An LDAP server (e.g., OpenLDAP), with:
  - Users under LDAP_USER_BASE_DN
  - Groups under LDAP_GROUP_BASE_DN
  - Groups listing members (member/uniqueMember/memberUid)
  - Group attributes for calendar ACLs:
    - Option A (separate attributes)
      - caldavCalendars: calendar identifiers (strings)
      - caldavPrivileges: any of read, write, edit, delete, bind, unbind
    - Option B (compact)
      - caldavBindings: entries like calendar-id=team;priv=read,write,bind,unbind

2) Database
- Create a database and run the schema:
  - psql -d yourdb -f internal/storage/postgres/schema.sql
- Ensure the app can connect via PG_URL environment variable.

3) Configuration (env vars)
Core server
- HTTP_ADDR: default :8080
- HTTP_BASE_PATH: default /dav
- HTTP_MAX_ICS_BYTES: max ICS payload in bytes (default 1048576 = 1 MiB)

LDAP
- LDAP_URL: ldap://host:389 or ldaps://host:636
- LDAP_BIND_DN, LDAP_BIND_PASSWORD: service account
- LDAP_USER_BASE_DN: e.g., ou=People,dc=example,dc=com
- LDAP_GROUP_BASE_DN: e.g., ou=Groups,dc=example,dc=com
- LDAP_USER_FILTER: default (|(uid=%s)(mail=%s))
- LDAP_GROUP_FILTER: default (cn=%s)
- LDAP_MEMBER_ATTR: member | uniqueMember | memberUid (default member)
- LDAP_CAL_IDS_ATTR: default caldavCalendars
- LDAP_PRIVS_ATTR: default caldavPrivileges
- LDAP_BINDINGS_ATTR: default empty (set to caldavBindings for compact form)
- LDAP_TOKEN_USER_ATTR: attr to map token subject to user (default uid)
- LDAP_NESTED: true/false (default false)
- LDAP Cache/Timeout defaults are set in code

Auth
- AUTH_BASIC: true/false (default true)
- AUTH_BEARER: true/false (default true)
- AUTH_JWKS_URL: JWKS endpoint for JWTs (cached)
- AUTH_ISSUER, AUTH_AUDIENCE: optional JWT validations
- AUTH_ALLOW_OPAQUE: true/false (default false)
- AUTH_INTROSPECT_URL: RFC 7662 endpoint (optional; used if opaque allowed)
- AUTH_INTROSPECT_AUTH: Authorization header for introspection (e.g., Basic ...)

PostgreSQL
- PG_URL: e.g., postgres://postgres:postgres@localhost:5432/caldav?sslmode=disable

Logging
- LOG_LEVEL: debug|info|warn|error (default info)

4) Run
- go run ./cmd/ldap-dav
- The server listens on HTTP_ADDR; .well-known is at /.well-known/caldav and redirects to HTTP_BASE_PATH.

LDAP group ACL model

- No app-managed ACLs. Effective permissions are computed from LDAP groups that contain the user.
- Each group either:
  - Lists one or more calendar IDs in caldavCalendars and privileges in caldavPrivileges
  - Or uses compact caldavBindings lines (calendar-id=...,priv=read,write,bind,unbind)
- Privilege mapping:
  - read -> PROPFIND/REPORT/GET
  - edit/write-props -> PROPPATCH (rename, displayname)
  - write-content -> PUT event body
  - bind (create) -> PUT new object
  - unbind (delete) -> DELETE object

HTTP endpoints overview

- Discovery
  - GET /.well-known/caldav -> 308 to /dav/
  - OPTIONS on /dav/... sets DAV: 1, 3, access-control, calendar-access

- Principals and homes
  - /dav/principals/users/{uid}
  - /dav/calendars/{uid}/
  - Shared auto-listing under /dav/calendars/{uid}/shared/

- Calendars and objects
  - /dav/calendars/{uid}/{calendar-id}/
  - /dav/calendars/{uid}/{calendar-id}/{object-uid}.ics

- Methods
  - PROPFIND: principals, home, calendars; collection/object properties
    - Collections include read-only DAV:acl showing the current user’s effective privileges
  - REPORT:
    - calendar-query: returns hrefs with calendar-data, getetag, getlastmodified
    - calendar-multiget: returns specified hrefs with calendar-data, getetag, getlastmodified
    - sync-collection: change log-based token seq:n (supports limit); includes number-of-matches-within-limits hint when limited
    - free-busy-query: returns a VFREEBUSY summary (no RRULE expansion yet)
  - GET/HEAD/PUT/DELETE: iCalendar objects
    - GET supports If-None-Match (304) and returns Last-Modified
    - PUT respects If-None-Match/If-Match and auto-detects VEVENT/VTODO/VJOURNAL; ICS size is limited by HTTP_MAX_ICS_BYTES
  - MKCOL: currently disabled; provision calendars via DB
  - PROPPATCH: updates displayname (requires edit privilege)
  - ACL: always 403 (ACLs are managed in LDAP)

CalDAV and WebDAV behavior notes

- Supported components: VEVENT, VTODO, VJOURNAL
- REPORT calendar-data: full VCALENDAR text returned by default for compatibility with major clients
- Sync tokens: per-calendar sequence-based token “seq:<n>”; changes recorded in calendar_changes allow incremental sync
- Free/busy: builds VFREEBUSY from stored start/end timestamps (no recurrence expansion yet)

Data model (PostgreSQL)

- calendars
  - id (uuid), owner_user_id (text), owner_group (text), uri (text unique per owner_user), display_name, description, ctag, sync_seq (bigint), sync_token (text), created_at, updated_at
- calendar_objects
  - id (uuid), calendar_id (uuid), uid (text), etag (text), data (text), component (text: VEVENT|VTODO|VJOURNAL), start_at, end_at, updated_at
- calendar_changes
  - calendar_id (uuid), seq (bigint), uid (text), deleted (bool), changed_at

Indexes recommended
- calendars(owner_user_id, uri)
- calendar_objects(calendar_id, uid) unique
- calendar_objects(calendar_id, component, start_at, end_at)
- calendar_changes(calendar_id, seq)
- Optional: calendar_objects(calendar_id, updated_at)

Provisioning example

- Insert a user calendar:
  - owner_user_id = alice
  - uri = personal
  - display_name = Personal
  - ctag = random token
- Insert a shared team calendar:
  - owner_user_id = bob
  - uri = team
  - display_name = Team

LDAP group example

- Group CN: team-cal-readers
  - member: uid=alice,ou=People,dc=example,dc=com
  - caldavCalendars: team
  - caldavPrivileges: read
- Group CN: team-cal-editors
  - member: uid=alice,ou=People,dc=example,dc=com
  - caldavBindings: calendar-id=team;priv=read,edit,write,bind,unbind

Client setup

- iOS/Apple Calendar
  - Account type: CalDAV
  - Server: https://your-host
  - Username/password or Bearer token (if using an app/proxy that injects Authorization)
  - Discovery should find principal and calendar-home-set; shared calendars appear under Shared.
- Thunderbird/Lightning
  - Use CalDAV URL: https://your-host/dav/calendars/alice/
  - Or subscribe to a specific collection: https://your-host/dav/calendars/alice/personal/

Security considerations

- Always deploy behind TLS (reverse proxy like Caddy/Nginx)
- Limit LDAP bind privileges; prefer search+bind; do not store passwords
- JWKS and token verification results are cached to reduce latency
- Enforce ICS size limits and validate/normalize ICS to prevent injection
- Consider rate limiting write operations

Roadmap

- Scheduling (RFC 6638): inbox/outbox resources, iTIP processing, delivery
- Recurrence expansion server-side for time-range queries (RRULE/RDATE/EXDATE)
- Nested LDAP groups resolution (optional; configurable depth)
- More complete number-of-matches-within-limits (exact totals/count if needed)
- CardDAV, /.well-known/carddav
- Admin APIs (calendar provisioning), quotas and related properties

Development

- Run server
  - go run ./cmd/ldap-dav
- Logs
  - Set LOG_LEVEL=debug for verbose logs
- Testing
  - Manual with curl or CalDAV clients (Apple Calendar, Thunderbird)
  - Consider running caldavtester for compliance checks

License

- MIT

Contributing

- Issues and PRs welcome. Please include:
  - Client used (iOS, Apple Calendar, Thunderbird, others)
  - Steps to reproduce
  - Relevant logs (redact sensitive info)

Repository

- GitHub: https://github.com/sonroyaalmerol/ldap-dav

Credits

- Built with Go, chi, go-ldap, jwx, and PostgreSQL
- iCalendar normalization via emersion/go-ical
