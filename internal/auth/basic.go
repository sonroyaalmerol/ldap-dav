package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/directory"

	"github.com/rs/zerolog"
)

type BasicAuth struct {
	Dir    directory.Directory
	Logger zerolog.Logger
}

func (b *BasicAuth) Authenticate(ctx context.Context, header string) (*Principal, error) {
	// header may be empty; browser will prompt; handle both cases
	if header == "" {
		return nil, errors.New("no auth")
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "basic" {
		return nil, errors.New("not basic")
	}
	dec, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	creds := strings.SplitN(string(dec), ":", 2)
	if len(creds) != 2 {
		return nil, errors.New("malformed basic")
	}
	username, password := creds[0], creds[1]
	user, err := b.Dir.BindUser(ctx, username, password)
	if err != nil {
		return nil, err
	}
	return &Principal{
		UserID:  user.UID,
		UserDN:  user.DN,
		Display: user.DisplayName,
	}, nil
}
