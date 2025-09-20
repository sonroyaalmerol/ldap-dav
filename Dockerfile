# ====== Build stage ======
FROM golang:1.25-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git ca-certificates tzdata
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/ldap-dav ./cmd/ldap-dav
RUN go build -trimpath -ldflags="-s -w" -o /opt/ldap-dav-bootstrap ./cmd/ldap-dav-bootstrap

# ====== Runtime stage ======
FROM alpine:3.20
WORKDIR /srv

RUN apk add --no-cache ca-certificates tzdata && update-ca-certificates

RUN addgroup -S app && adduser -S app -G app

COPY --from=builder /out/ldap-dav /usr/local/bin/ldap-dav
COPY --from=builder /out/ldap-dav-bootstrap /usr/local/bin/ldap-dav-bootstrap

ENV HTTP_ADDR=:8080 \
    HTTP_BASE_PATH=/dav \
    HTTP_MAX_ICS_BYTES=1048576 \
    AUTH_BASIC=true \
    AUTH_BEARER=false \
    LOG_LEVEL=info

EXPOSE 8080

USER app

ENTRYPOINT ["/usr/local/bin/ldap-dav"]
