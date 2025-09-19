.PHONY: up down test test-pg test-fs

up:
	docker compose up -d --wait

down:
	docker compose down -v

test: up test-pg test-fs down

test-pg:
	# Build test runner
	docker build -f Dockerfile.test -t ldap-dav-test .
	# Bootstrap calendars in Postgres
	docker run --rm --network host \
	  -e STORAGE_TYPE=postgres \
	  -e PG_URL="postgres://postgres:postgres@localhost:5432/caldav?sslmode=disable" \
	  -e HTTP_BASE_PATH="/dav" \
	  -e AUTH_BASIC=true \
	  -e AUTH_BEARER=false \
	  ldap-dav-test \
	  ldap-dav-bootstrap -owner alice -uri personal -display "Alice Personal"
	docker run --rm --network host \
	  -e STORAGE_TYPE=postgres \
	  -e PG_URL="postgres://postgres:postgres@localhost:5432/caldav?sslmode=disable" \
	  -e HTTP_BASE_PATH="/dav" \
	  -e AUTH_BASIC=true \
	  -e AUTH_BEARER=false \
	  ldap-dav-test \
	  ldap-dav-bootstrap -owner bob -uri team -display "Team Calendar"
	# Run integration tests against postgres backend
	docker run --rm --network host \
	  -e STORAGE_TYPE=postgres \
	  -e PG_URL="postgres://postgres:postgres@localhost:5432/caldav?sslmode=disable" \
	  -e LDAP_URL="ldap://localhost:389" \
	  -e LDAP_BIND_DN="cn=admin,dc=example,dc=com" \
	  -e LDAP_BIND_PASSWORD="admin" \
	  -e LDAP_USER_BASE_DN="ou=People,dc=example,dc=com" \
	  -e LDAP_GROUP_BASE_DN="ou=Groups,dc=example,dc=com" \
	  -e AUTH_BASIC=true \
	  -e AUTH_BEARER=false \
	  -e HTTP_BASE_PATH="/dav" \
	  -e HTTP_ADDR=":8081" \
	  ldap-dav-test \
	  go test ./test/integration -v -run TestIntegration

test-fs:
	docker run --rm --network host \
	  -e STORAGE_TYPE=filestore \
	  -e FILE_ROOT="/tmp/ldap-dav-data" \
	  -e HTTP_BASE_PATH="/dav" \
	  -e AUTH_BASIC=true \
	  -e AUTH_BEARER=false \
	  -v /tmp/ldap-dav-data:/tmp/ldap-dav-data \
	  ldap-dav-test \
	  ldap-dav-bootstrap -owner alice -uri personal -display "Alice Personal"
	docker run --rm --network host \
	  -e STORAGE_TYPE=filestore \
	  -e FILE_ROOT="/tmp/ldap-dav-data" \
	  -e HTTP_BASE_PATH="/dav" \
	  -e AUTH_BASIC=true \
	  -e AUTH_BEARER=false \
	  -v /tmp/ldap-dav-data:/tmp/ldap-dav-data \
	  ldap-dav-test \
	  ldap-dav-bootstrap -owner bob -uri team -display "Team Calendar"
	docker run --rm --network host \
	  -e STORAGE_TYPE=filestore \
	  -e FILE_ROOT="/tmp/ldap-dav-data" \
	  -e LDAP_URL="ldap://localhost:389" \
	  -e LDAP_BIND_DN="cn=admin,dc=example,dc=com" \
	  -e LDAP_BIND_PASSWORD="admin" \
	  -e LDAP_USER_BASE_DN="ou=People,dc=example,dc=com" \
	  -e LDAP_GROUP_BASE_DN="ou=Groups,dc=example,dc=com" \
	  -e AUTH_BASIC=true \
	  -e AUTH_BEARER=false \
	  -e HTTP_BASE_PATH="/dav" \
	  -e HTTP_ADDR=":8082" \
	  -v /tmp/ldap-dav-data:/tmp/ldap-dav-data \
	  ldap-dav-test \
	  go test ./test/integration -v -run TestIntegration
