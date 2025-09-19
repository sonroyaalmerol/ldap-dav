package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/router"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage/filestore"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage/postgres"
)

type Server struct {
	http   *http.Server
	logger zerolog.Logger
}

func NewServer(cfg *config.Config, logger zerolog.Logger) (*Server, func(), error) {
	// init storage
	var store storage.Store
	var err error

	switch cfg.Storage.Type {
	case "postgres":
		store, err = postgres.New(cfg.Storage.PostgresURL, logger)
	case "filestore":
		fslog := func(msg string, kv ...any) {
			ev := logger.Debug().Str("component", "filestore").Str("msg", msg)
			for i := 0; i+1 < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					continue
				}
				ev = ev.Interface(k, kv[i+1])
			}
			ev.Send()
		}
		store, err = filestore.New(cfg.Storage.FileRoot, fslog)
	default:
		err = errors.New("unknown storage type: " + cfg.Storage.Type)
	}
	if err != nil {
		return nil, nil, err
	}

	dir, err := directory.NewLDAPClient(cfg.LDAP, logger)
	if err != nil {
		store.Close()
		return nil, nil, err
	}

	authn := auth.NewChain(cfg, dir, logger)
	davh := dav.NewHandlers(cfg, store, dir, authn, logger)
	mux := router.New(cfg, davh, authn, logger)

	srv := &Server{
		http: &http.Server{
			Addr:         cfg.HTTP.Addr,
			Handler:      mux,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		logger: logger,
	}
	cleanup := func() {
		store.Close()
		dir.Close()
	}
	logger.Info().Msgf("listening on %s (storage=%s)", cfg.HTTP.Addr, cfg.Storage.Type)
	return srv, cleanup, nil
}

func (s *Server) Start() error {
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
