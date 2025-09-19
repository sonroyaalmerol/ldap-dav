package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/httpserver"
	"github.com/sonroyaalmerol/ldap-dav/internal/logging"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	logger := logging.New(cfg.LogLevel)

	srv, cleanup, err := httpserver.NewServer(cfg, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("server init failed")
	}
	defer cleanup()

	go func() {
		if err := srv.Start(); err != nil {
			logger.Fatal().Err(err).Msg("server stopped with error")
		}
	}()

	logger.Info().Msgf("listening on %s", cfg.HTTP.Addr)

	// graceful shutdown
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	if err := srv.Shutdown(context.Background()); err != nil {
		logger.Error().Err(err).Msg("shutdown error")
	}
	logger.Info().Msg("bye")
}
