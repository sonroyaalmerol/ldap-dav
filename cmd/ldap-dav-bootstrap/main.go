package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/logging"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage/filestore"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage/postgres"
)

func main() {
	var (
		ownerUser   string
		calURI      string
		displayName string
		ownerGroup  string
		desc        string
	)
	flag.StringVar(&ownerUser, "owner", "", "Owner user ID (required)")
	flag.StringVar(&calURI, "uri", "", "Calendar URI (required, unique per owner)")
	flag.StringVar(&displayName, "display", "", "Calendar display name (optional; defaults to uri)")
	flag.StringVar(&ownerGroup, "group", "", "Owner group (optional)")
	flag.StringVar(&desc, "desc", "", "Description (optional)")
	flag.Parse()

	if ownerUser == "" || calURI == "" {
		fmt.Fprintln(os.Stderr, "usage: ldap-dav-bootstrap -owner <uid> -uri <calendar-uri> [-display <name>] [-group <group>] [-desc <description>]")
		os.Exit(2)
	}
	if displayName == "" {
		displayName = calURI
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.LogLevel)
	logger = logger.With().Str("component", "bootstrap").Logger()

	// Create storage based on cfg.Storage.Type
	var store storage.Store
	switch cfg.Storage.Type {
	case "postgres":
		store, err = postgres.New(cfg.Storage.PostgresURL, logger)
	case "filestore":
		fslog := func(msg string, kv ...any) {
			ev := logger.Debug().Str("component", "filestore").Str("msg", msg)
			for i := 0; i+1 < len(kv); i += 2 {
				if k, ok := kv[i].(string); ok {
					ev = ev.Interface(k, kv[i+1])
				}
			}
			ev.Send()
		}
		store, err = filestore.New(cfg.Storage.FileRoot, fslog)
	default:
		err = fmt.Errorf("unknown storage type: %s", cfg.Storage.Type)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage init: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Use type assertion to call backend-specific CreateCalendar helper.
	now := time.Now().UTC()
	cal := storage.Calendar{
		ID:          "", // let backend generate if needed
		OwnerUserID: ownerUser,
		OwnerGroup:  ownerGroup,
		URI:         calURI,
		DisplayName: displayName,
		Description: desc,
		CTag:        "", // backend will generate a ctag
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	switch s := store.(type) {
	case interface {
		CreateCalendar(c storage.Calendar, ownerGroup string, description string) error
	}:
		// filestore and postgres both implement this helper signature as requested
		if err := s.CreateCalendar(cal, ownerGroup, desc); err != nil {
			fmt.Fprintf(os.Stderr, "create calendar: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "backend does not support CreateCalendar helper\n")
		os.Exit(1)
	}

	logger.Info().
		Str("owner", ownerUser).
		Str("uri", calURI).
		Str("display", displayName).
		Str("group", ownerGroup).
		Msg("calendar created")

	fmt.Printf("Created calendar owner=%s uri=%s display=%q\n", ownerUser, calURI, displayName)
}
