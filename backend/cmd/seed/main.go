package main

import (
	"context"
	"log"
	"time"

	"github.com/anyquery/anyquery/internal/config"
	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/demo"
	"github.com/anyquery/anyquery/internal/store"
)

func main() {
	log.SetFlags(0)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	st, err := store.New(ctx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close(context.Background())
	key, err := cryptox.NewKey(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("encryption: %v", err)
	}
	if err := demo.Seed(ctx, cfg, st, key); err != nil {
		log.Fatalf("seed: %v", err)
	}
}
