package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/anyquery/anyquery/internal/api"
	"github.com/anyquery/anyquery/internal/auth"
	"github.com/anyquery/anyquery/internal/config"
	_ "github.com/anyquery/anyquery/internal/connectors/all"
	"github.com/anyquery/anyquery/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close(context.Background())
	authSvc, err := auth.New(ctx, cfg, st)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	srv, err := api.New(cfg, st, authSvc)
	if err != nil {
		log.Fatalf("api: %v", err)
	}
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("AnyQuery listening on %s (base URL %s, OIDC %v, dev auth %v)", cfg.Addr, cfg.BaseURL, cfg.OIDCEnabled(), cfg.DevAuth)
	if strings.HasPrefix(cfg.MongoDatabase, "anyquery") && cfg.DevAuth {
		log.Printf("tip: run `mise run seed` to load the demo workspace")
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-stop:
		log.Printf("received %s, shutting down", sig)
	case err := <-errCh:
		log.Fatalf("server: %v", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
