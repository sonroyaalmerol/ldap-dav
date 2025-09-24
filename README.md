# ldap-dav

A CalDAV/WebDAV server in Go with LDAP-driven ACLs and sharing. Designed for minimal local state: users and groups are read from LDAP on demand. Supports Basic and Bearer auth, .well-known discovery, and auto-listing of shared calendars.

Production usage: Docker-only
- We recommend running ldap-dav exclusively via Docker (and docker-compose) for a reproducible environment with OpenLDAP and PostgreSQL.
- Native builds are possible (Go 1.25+), but Docker is the supported path.

Key features
- CalDAV (RFC 4791) on top of WebDAV (RFC 4918)
- Sharing and ACLs via LDAP groups only
  - Each LDAP group declares which calendars it grants access to and which privileges (read, write-props, write-content, bind, unbind, read-acl)
- Users/groups are not replicated; resolved on-demand with short caching
- Auth: HTTP Basic (LDAP bind) and Bearer (JWT via JWKS; opaque via RFC 7662 introspection optional)
  - JWKS keys are cached; token verification results are also cached briefly
- Discovery: /.well-known/caldav, principals, calendar-home-set
- Auto-list shared calendars based on LDAP group ACLs
- WebDAV Sync (RFC 6578) with incremental tokens and change log (supports paging/limits)
- REPORTs:
  - calendar-query and calendar-multiget returning calendar-data, getetag, and getlastmodified
  - free-busy-query (basic VFREEBUSY generation; no recurrence expansion yet)
- iCalendar components: VEVENT, VTODO, VJOURNAL
- Storage: PostgreSQL (calendars, objects, change log) with recommended indexes
- Read-only WebDAV ACL properties surfaced on collections to reflect effective privileges
- Configurable max ICS upload size
- Recurrence expansion server-side for time-range queries (RRULE/RDATE/EXDATE)
- HEAD is supported everywhere GET is, returning headers without body

## Quick start (Docker)

1) Prerequisites
- Docker and docker-compose
- Open ports 389 (LDAP), 5432 (Postgres), 8080 (App) or adjust mapping

2) Directory layout
- docker-compose.yml (provided)
- deploy/ldap/caldav-schema.ldif (custom schema)
- deploy/ldap/base.ldif (seed entries)
- internal/storage/postgres/schema.sql (app schema)

3) Bring up the stack

```bash
docker compose up -d
# Wait for services to be healthy
docker compose logs -f ldap-dav
```

- App: http://localhost:8080
- CalDAV base path: http://localhost:8080/dav
- .well-known: http://localhost:8080/.well-known/caldav

4) Seed calendars
- Create a calendar for bob with URI “team” (owner bob)
- Create a calendar for alice with URI “personal” (optional for testing)

Use the bootstrap CLI (if you built it), or run a one-off psql insert:

```bash
docker exec -it postgres psql -U postgres -d caldav -c \
"insert into calendars (id, owner_user_id, owner_group, uri, display_name, description, ctag, sync_seq, sync_token)
 values (gen_random_uuid(), 'bob', '', 'team', 'Team', 'Team Calendar', 'ctag-init', 0, 'seq:0')
 on conflict do nothing;"
```

If gen_random_uuid() is unavailable, substitute a UUID and cast with ::uuid, or enable pgcrypto.

5) Test with a CalDAV client
- Username: alice
- Password: password
- Server: http://localhost:8080/dav
- Shared: /dav/calendars/alice/shared/team/ should show bob’s “team” calendar when groups are set as in the LDIF.

## Configuration (env vars)

Core server
- HTTP_ADDR: default :8080
- HTTP_BASE_PATH: default /dav
- HTTP_MAX_ICS_BYTES: max ICS payload in bytes (default 1048576 = 1 MiB)
- LOG_LEVEL: debug|info|warn|error (default info)

LDAP
- LDAP_URL: ldap://ldap:389 or ldaps://host:636
- LDAP_BIND_DN / LDAP_BIND_PASSWORD: admin/service account
- LDAP_USER_BASE_DN: e.g., ou=People,dc=example,dc=com
- LDAP_GROUP_BASE_DN: e.g., ou=Groups,dc=example,dc=com
- LDAP_USER_FILTER: default (|(uid=%s)(mail=%s))
- LDAP_GROUP_FILTER: default (cn=%s)
- LDAP_MEMBER_ATTR: member | uniqueMember | memberUid (default member)
- LDAP_CAL_IDS_ATTR: caldavCalendars (pair mode)
- LDAP_PRIVS_ATTR: caldavPrivileges (pair mode)
- LDAP_BINDINGS_ATTR: caldavBindings (compact mode; recommended)
- LDAP_TOKEN_USER_ATTR: uid
- LDAP_NESTED: true/false (default false)

Auth
- AUTH_BASIC: true/false (default true)
- AUTH_BEARER: true/false (default false)
- AUTH_JWKS_URL: JWKS endpoint for JWTs (cached)
- AUTH_ISSUER, AUTH_AUDIENCE: optional JWT validations
- AUTH_ALLOW_OPAQUE: true/false (default false)
- AUTH_INTROSPECT_URL: RFC 7662 endpoint (optional)
- AUTH_INTROSPECT_AUTH: Authorization header for introspection

PostgreSQL
- PG_URL: e.g., postgres://postgres:postgres@postgres:5432/caldav?sslmode=disable

Storage
- STORAGE_TYPE: postgres | filestore (default postgres)
- FILE_ROOT: used only if filestore is selected (mounted volume recommended)

## LDAP group ACL model

- No app-managed ACLs. Effective permissions are computed from LDAP groups that contain the user.
- Each group either:
  - Lists one or more calendar IDs in caldavCalendars and privileges in caldavPrivileges
  - Or uses compact caldavBindings entries like:
    - calendar-id=team;priv=read,edit,write,bind,unbind

Privilege mapping:
- read -> PROPFIND/REPORT/GET
- write-props -> PROPPATCH (rename, displayname)
- write-content -> PUT event body
- bind -> PUT new object
- unbind -> DELETE object
- read-acl

## Endpoints

- Discovery
  - GET /.well-known/caldav -> 308 to /dav/
  - OPTIONS under /dav includes DAV: 1, 3, access-control, calendar-access

- Principals and homes
  - /dav/principals/users/{uid}
  - /dav/calendars/{uid}/
  - Shared: /dav/calendars/{uid}/shared/{calendar-uri}/

- Objects
  - /dav/calendars/{owner}/{calendar-uri}/{object-uid}.ics

Methods
- PROPFIND: principals, home, calendars; collection/object properties
- REPORT: calendar-query, calendar-multiget, sync-collection, free-busy-query
- GET/HEAD/PUT/DELETE: iCalendar objects
- MKCOL/MKCALENDAR: create collection/calendar
- PROPPATCH: updates displayname (requires edit privilege)

## Security

- Run behind TLS (reverse proxy like Caddy/Nginx/Traefik)
- Limit LDAP bind privileges (search/bind pattern)
- Cache JWKS and introspection results
- Enforce ICS size limits; server auto-generates DTSTAMP if missing
- Consider rate limiting write operations

## Roadmap

- Scheduling (RFC 6638): inbox/outbox resources, iTIP processing, delivery
- Nested LDAP groups resolution (configurable depth)
- CardDAV (/.well-known/carddav)
- Quotas

## License

MIT

## Contributing

Issues and PRs welcome. Please include:
- Client used (iOS, Apple Calendar, Thunderbird, others)
- Steps to reproduce
- Relevant logs (redact sensitive info)

## Repository

- GitHub: https://github.com/sonroyaalmerol/ldap-dav

## Credits

- Go, emersion/go-ical, go-ldap, jwx, PostgreSQL
