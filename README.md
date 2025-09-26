# ldap-dav

A CalDAV/CardDAV server in Go with LDAP-driven ACLs and sharing. Designed for minimal local state: users and groups are read from LDAP on demand. Supports Basic and Bearer auth, .well-known discovery, auto-listing of shared calendars, and personal address books with global LDAP-based address books.

## Key features

### CalDAV
- CalDAV (RFC 4791) on top of WebDAV (RFC 4918)
- Calendar sharing and ACLs via LDAP groups only
  - Each LDAP group declares which calendars it grants access to and which privileges (read, write-props, write-content, bind, unbind, read-acl)
- Auto-list shared calendars based on LDAP group ACLs
- iCalendar components: VEVENT, VTODO, VJOURNAL
- Recurrence expansion server-side for time-range queries (RRULE/RDATE/EXDATE)

### CardDAV
- CardDAV (RFC 6352) on top of WebDAV (RFC 4918)
- Personal address books: users can create and manage their own address books
- Global read-only address books via LDAP filters
  - Configure LDAP_ADDRESSBOOK_FILTER_X to create shared address books from LDAP directory
  - Each filter becomes a read-only address book accessible to all users
- No calendar-style sharing for personal address books

### Common features
- Users/groups are not replicated; resolved on-demand with short caching
- Auth: HTTP Basic (LDAP bind) and Bearer (JWT via JWKS; opaque via RFC 7662 introspection optional)
  - JWKS keys are cached; token verification results are also cached briefly
- Discovery: /.well-known/caldav, /.well-known/carddav, principals, calendar-home-set, addressbook-home-set
- WebDAV Sync (RFC 6578) with incremental tokens and change log (supports paging/limits)
- REPORTs:
  - **CalDAV**: calendar-query and calendar-multiget returning calendar-data, getetag, and getlastmodified
  - **CalDAV**: free-busy-query (basic VFREEBUSY generation; no recurrence expansion yet)
  - **CardDAV**: addressbook-query and addressbook-multiget returning address-data, getetag, and getlastmodified
- Storage: PostgreSQL (calendars, address books, objects, change log) with recommended indexes
- Read-only WebDAV ACL properties surfaced on collections to reflect effective privileges
- Configurable max ICS and VCF upload sizes
- HEAD is supported everywhere GET is, returning headers without body

## Quick start (Docker)

1) Prerequisites
- Docker and docker-compose
- Open ports 389 (LDAP), 5432 (Postgres), 8080 (App) or adjust mapping

2) Directory layout
- docker-compose.yml (provided example)

3) Bring up the stack

```bash
docker compose up -d
# Wait for services to be healthy
docker compose logs -f ldap-dav
```

- App: http://localhost:8080
- CalDAV base path: http://localhost:8080/dav
- CardDAV base path: http://localhost:8080/dav
- .well-known: http://localhost:8080/.well-known/caldav and http://localhost:8080/.well-known/carddav

4) Seed calendars
- Create a calendar for bob with URI "team" (owner bob)
- Create a calendar for alice with URI "personal" (optional for testing)

Use the bootstrap CLI (if you built it), or run a one-off psql insert:

```bash
docker exec -it postgres psql -U postgres -d caldav -c \
"insert into calendars (id, owner_user_id, owner_group, uri, display_name, description, ctag, sync_seq, sync_token)
 values (gen_random_uuid(), 'bob', '', 'team', 'Team', 'Team Calendar', 'ctag-init', 0, 'seq:0')
 on conflict do nothing;"
```

If gen_random_uuid() is unavailable, substitute a UUID and cast with ::uuid, or enable pgcrypto.

5) Test with CalDAV/CardDAV clients
- Username: alice
- Password: password
- Server: http://localhost:8080/dav
- **CalDAV**: Shared calendars at /dav/calendars/alice/shared/team/ should show bob's "team" calendar when groups are set as in the LDIF
- **CardDAV**: Personal address books at /dav/addressbooks/alice/{addressbook-uri}/
- **CardDAV**: Global address books at /dav/addressbooks/alice/ldap_{N}/ (read-only, populated from LDAP filters)

## Configuration

All configuration is via environment variables.

### Core Server
- `HTTP_ADDR`: Server address (default `":8080"`)
- `HTTP_BASE_PATH`: Base path for DAV endpoints (default `"/dav"`)
- `HTTP_MAX_ICS_BYTES`: Maximum ICS payload size in bytes (default `"1048576"` = 1 MiB)
- `HTTP_MAX_VCF_BYTES`: Maximum VCF payload size in bytes (default `"1048576"` = 1 MiB)
- `TZ`: Timezone (default `"UTC"`)
- `LOG_LEVEL`: Logging level — `debug|info|warn|error` (default `"info"`)

### LDAP
- `LDAP_URL`: LDAP server URL (default `"ldap://localhost:389"`)
- `LDAP_BIND_DN`: Service account DN for LDAP binding
- `LDAP_BIND_PASSWORD`: Service account password
- `LDAP_USER_BASE_DN`: Base DN for user searches (e.g., `"ou=People,dc=example,dc=com"`)
- `LDAP_GROUP_BASE_DN`: Base DN for group searches (e.g., `"ou=Groups,dc=example,dc=com"`)
- `LDAP_USER_FILTER`: User search filter (default `"(|(uid=%s)(mail=%s))"`)
- `LDAP_GROUP_FILTER`: Group search filter (default `"(cn=%s)"`)
- `LDAP_MEMBER_ATTR`: Group membership attribute — `member|uniqueMember|memberUid` (default `"member"`)
- `LDAP_CAL_IDS_ATTR`: Calendar IDs attribute for pair mode (default `"caldavCalendars"`)
- `LDAP_PRIVS_ATTR`: Privileges attribute for pair mode (default `"caldavPrivileges"`)
- `LDAP_BINDINGS_ATTR`: Compact bindings attribute (default `"caldavBindings"`) — recommended
- `LDAP_TOKEN_USER_ATTR`: User attribute for token mapping (default `"uid"`)
- `LDAP_NESTED`: Enable nested group resolution (default `"false"`)
- `LDAP_SKIP_VERIFY`: Skip TLS certificate verification (default `"false"`)
- `LDAP_REQUIRE_TLS`: Require TLS connection (default `"false"`)

LDAP timeouts and caching:
- Fixed defaults: `Timeout = 5s`, `Cache TTL = 60s`, `MaxGroupDepth = 3`

### LDAP Addressbook Filters (Shared Directories)

Define global read-only address books sourced directly from LDAP using numbered
variables. Each index `N` creates one shared address book accessible to all users.

Common variables per `N`:
- `LDAP_ADDRESSBOOK_FILTER_{N}_URL`: LDAP URL for this filter (default falls back to `LDAP_URL`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_BIND_DN`: DN for this filter (default `LDAP_BIND_DN`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_BIND_PASSWORD`: Password (default `LDAP_BIND_PASSWORD`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_SKIP_VERIFY`: `"true"`/`"false"` (default `LDAP_SKIP_VERIFY`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_REQUIRE_TLS`: `"true"`/`"false"` (default `LDAP_REQUIRE_TLS`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_NAME`: Name (default `"Addressbook_{N}"`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_BASE_DN`: Base DN (default `LDAP_USER_BASE_DN`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_FILTER`: LDAP filter (default `"(objectClass=person)"`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_ENABLED`: `"true"`/`"false"` (default `"true"`)
- `LDAP_ADDRESSBOOK_FILTER_{N}_DESCRIPTION`: Optional description
- `LDAP_ADDRESSBOOK_FILTER_{N}_URI`: Slug/URI for address book (default slug of `NAME`)

Attribute mappings (optional, with defaults):
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_UID`: default `"uid"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_DISPLAY_NAME`: default `"displayName"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_FIRST_NAME`: default `"givenName"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_LAST_NAME`: default `"sn"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_EMAIL`: default `"mail"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_PHONE`: default `"telephoneNumber"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_ORGANIZATION`: default `"o"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_TITLE`: default `"title"`
- `LDAP_ADDRESSBOOK_FILTER_{N}_MAP_PHOTO`: default `"jpegPhoto"`

Notes:
- Filters are discovered via `LDAP_ADDRESSBOOK_FILTER_0`, `_1`, ... up to `99`.
- A filter is included when either the base variable, `NAME`, or `BASE_DN` is set.
- `URI` defaults to a slug of `NAME` if not provided.

### Authentication
- `AUTH_BASIC`: Enable HTTP Basic auth (default `"true"`)
- `AUTH_BEARER`: Enable Bearer token auth (default `"true"`)
- `AUTH_JWKS_URL`: JWKS endpoint URL for JWT validation (cached)
- `AUTH_ISSUER`: Expected JWT issuer (optional)
- `AUTH_AUDIENCE`: Expected JWT audience (optional)
- `AUTH_ALLOW_OPAQUE`: Allow opaque token introspection (default `"false"`)
- `AUTH_INTROSPECT_URL`: RFC 7662 token introspection endpoint (optional)
- `AUTH_INTROSPECT_AUTH`: Authorization header for introspection requests

### Storage
- `STORAGE_TYPE`: `postgres|sqlite` (default `"postgres"`)
- `PG_URL`: PostgreSQL connection string (default `"postgres://postgres:postgres@localhost:5432/caldav?sslmode=disable"`)
- `SQLITE_PATH`: SQLite file path when `STORAGE_TYPE=sqlite` (default `"/data/db.sql"`)

### ICS Generation
- `ICS_COMPANY_NAME`: Company name in generated ICS files (default `"LDAP DAV"`)
- `ICS_PRODUCT_NAME`: Product name in generated ICS files (default `"CalDAV"`)
- `ICS_VERSION`: Version string in generated ICS files (default `"1.0.0"`)
- `ICS_LANGUAGE`: Language code for generated ICS files (default `"EN"`)

## LDAP group ACL model (CalDAV only)

- No app-managed ACLs for calendars. Effective permissions are computed from LDAP groups that contain the user.
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

**Note**: CardDAV address books do not use LDAP group ACLs. Users have full control over their personal address books, while global address books from LDAP filters are read-only for all users.

## Endpoints

### Discovery
- `GET /.well-known/caldav` -> 308 to `/dav/`
- `GET /.well-known/carddav` -> 308 to `/dav/`
- `OPTIONS` under `/dav` includes `DAV: 1, 3, access-control, calendar-access, addressbook`

### Principals and homes
- `/dav/principals/users/{uid}`
- **CalDAV**: `/dav/calendars/{uid}/`
- **CardDAV**: `/dav/addressbooks/{uid}/`

### Collections
- **CalDAV**: Shared calendars at `/dav/calendars/{uid}/shared/{calendar-uri}/`
- **CardDAV**: Personal address books at `/dav/addressbooks/{uid}/{addressbook-uri}/`
- **CardDAV**: Global address books at `/dav/addressbooks/{uid}/shared/{filter-name}/` (read-only)

### Objects
- **CalDAV**: `/dav/calendars/{owner}/{calendar-uri}/{object-uid}.ics`
- **CardDAV**: `/dav/addressbooks/{owner}/{addressbook-uri}/{object-uid}.vcf`

### Methods
- `PROPFIND`: principals, homes, collections; collection/object properties
- `REPORT`: calendar-query, calendar-multiget, addressbook-query, addressbook-multiget, sync-collection, free-busy-query
- `GET/HEAD/PUT/DELETE`: iCalendar objects (.ics) and vCard objects (.vcf)
- `MKCOL`: create collection (CalDAV/CardDAV)
- `MKCALENDAR`: create calendar (CalDAV)
- `PROPPATCH`: updates displayname and other properties

## Security

- Run behind TLS (reverse proxy like Caddy/Nginx/Traefik)
- Limit LDAP bind privileges (search/bind pattern)
- Cache JWKS and introspection results
- Enforce ICS and VCF size limits; server auto-generates DTSTAMP/REV if missing
- Consider rate limiting write operations
- Global address books from LDAP are read-only by design

## Roadmap

- **CalDAV**: Scheduling (RFC 6638): inbox/outbox resources, iTIP processing, delivery
- **CardDAV**: Address book sharing capabilities similar to calendar sharing
- Nested LDAP groups resolution (configurable depth)
- Quotas for both calendars and address books

## License

MIT

## Contributing

Issues and PRs welcome. Please include:
- Client used (iOS Calendar/Contacts, Apple Calendar/Contacts, Thunderbird, others)
- Protocol (CalDAV or CardDAV)
- Steps to reproduce
- Relevant logs (redact sensitive info)

## Repository

- GitHub: https://github.com/sonroyaalmerol/ldap-dav

## Credits

- Go, emersion/go-ical, emersion/go-vcard, go-ldap, jwx, PostgreSQL
