// Package demo seeds a self-contained workspace with MongoDB + SQLite data so
// a fresh install can try federated queries immediately.
package demo

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	_ "modernc.org/sqlite"

	"github.com/anyquery/anyquery/internal/config"
	"github.com/anyquery/anyquery/internal/connector"
	_ "github.com/anyquery/anyquery/internal/connectors/all"
	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

type customer struct {
	ID        int
	Name      string
	Email     string
	Segment   string
	Country   string
	CreatedAt time.Time
}

type product struct {
	ID       int
	Name     string
	Category string
	Price    float64
}

func Seed(ctx context.Context, cfg config.Config, st *store.Store, key *cryptox.Key) error {
	admin, err := st.UpsertDevUser(ctx, "admin@anyquery.dev", "Maya Chen")
	if err != nil {
		return err
	}
	sam, err := st.UpsertDevUser(ctx, "sam@anyquery.dev", "Sam Rivera")
	if err != nil {
		return err
	}
	if cfg.PasswordAuth && cfg.DemoPassword != "" {
		hash, err := cryptox.HashPassword(cfg.DemoPassword)
		if err != nil {
			return err
		}
		if err := st.SetUserPassword(ctx, admin.ID, hash); err != nil {
			return err
		}
	}
	tenant, err := ensureTenant(ctx, st, admin.ID)
	if err != nil {
		return err
	}
	if err := st.UpsertMember(ctx, tenant.ID, sam.ID, model.RoleMember); err != nil {
		return err
	}
	if err := ensureInvite(ctx, cfg, st, tenant, admin); err != nil {
		return err
	}
	if err := seedMongoDemo(ctx, cfg); err != nil {
		return fmt.Errorf("seed MongoDB demo: %w", err)
	}
	sqlitePath, err := seedSQLiteDemo()
	if err != nil {
		return fmt.Errorf("seed SQLite demo: %w", err)
	}
	if err := ensureDataSource(ctx, st, key, tenant, admin, "mongo", "mongodb", map[string]string{
		"uri": cfg.MongoURI, "database": "anyquery_demo", "sample_size": "30",
	}); err != nil {
		return err
	}
	if err := ensureDataSource(ctx, st, key, tenant, admin, "shop", "sqlite", map[string]string{
		"path": sqlitePath,
	}); err != nil {
		return err
	}
	queries := []struct {
		Name        string
		Description string
		SQL         string
	}{
		{
			Name:        "Revenue by product category",
			Description: "Federated join between MongoDB orders and the SQLite product catalog.",
			SQL: `SELECT p.category,
       count(*) AS orders,
       round(sum(o.total), 2) AS revenue
FROM mongo.orders o
JOIN shop.products p ON o.product_id = p.id
WHERE o.status = 'paid'
GROUP BY p.category
ORDER BY revenue DESC`,
		},
		{
			Name:        "Paid revenue by region (pipeline)",
			Description: "The same data expressed with composable pipeline stages.",
			SQL: `FROM mongo.orders
|> WHERE status = 'paid'
|> AGGREGATE region, count(*) AS orders, round(sum(total), 2) AS revenue GROUP BY region
|> ORDER BY revenue DESC`,
		},
		{
			Name:        "Recent enterprise orders",
			Description: "Join customer segments with recent order activity.",
			SQL: `SELECT c.name, c.segment, o.total, o.status, o.created_at
FROM mongo.orders o
JOIN mongo.customers c ON o.customer_id = c.id
WHERE c.segment = 'enterprise'
ORDER BY o.created_at DESC
LIMIT 20`,
		},
	}
	for _, q := range queries {
		if err := ensureSavedQuery(ctx, st, tenant, admin, q.Name, q.Description, q.SQL); err != nil {
			return err
		}
	}
	fmt.Printf("\nDemo workspace ready\n")
	if cfg.PasswordAuth && cfg.DemoPassword != "" {
		fmt.Printf("  Sign in:       admin@anyquery.dev / %s\n", cfg.DemoPassword)
	} else {
		fmt.Printf("  Sign in (dev): admin@anyquery.dev\n")
	}
	fmt.Printf("  Workspace:     %s (%s)\n", tenant.Name, tenant.Slug)
	fmt.Printf("  Sources:       mongo.orders, mongo.customers, shop.products\n")
	fmt.Printf("  Data file:     %s\n\n", sqlitePath)
	return nil
}

func ensureTenant(ctx context.Context, st *store.Store, adminID string) (*model.Tenant, error) {
	t, err := st.GetTenant(ctx, "acme")
	if err == nil {
		return t, nil
	}
	if err != store.ErrNotFound {
		return nil, err
	}
	return st.CreateTenant(ctx, "Acme Analytics", "acme", adminID)
}

func ensureInvite(ctx context.Context, cfg config.Config, st *store.Store, tenant *model.Tenant, admin *model.User) error {
	invites, err := st.ListInvites(ctx, tenant.ID)
	if err != nil {
		return err
	}
	for _, inv := range invites {
		if inv.Email == "taylor@anyquery.dev" {
			return nil
		}
	}
	token, err := cryptox.RandomToken(32)
	if err != nil {
		return err
	}
	inv, err := st.CreateInvite(ctx, model.Invite{
		ID: store.NewID(), TenantID: tenant.ID, Email: "taylor@anyquery.dev", Role: model.RoleViewer,
		TokenHash: cryptox.HashToken(token), InvitedBy: admin.ID, CreatedAt: store.Now(),
		ExpiresAt: store.Now().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		return err
	}
	fmt.Printf("  Pending invite: taylor@anyquery.dev -> %s/invite/%s (%s)\n", cfg.BaseURL, token, inv.Role)
	return nil
}

func seedMongoDemo(ctx context.Context, cfg config.Config) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI).SetServerSelectionTimeout(10*time.Second))
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db := client.Database("anyquery_demo")
	customers := []customer{
		{1, "Acme Corp", "ops@acme.example", "enterprise", "United States", time.Now().AddDate(-2, 0, 0)},
		{2, "Globex", "data@globex.example", "mid-market", "Germany", time.Now().AddDate(-1, -3, 0)},
		{3, "Initech", "bi@initech.example", "enterprise", "United States", time.Now().AddDate(-1, 0, 0)},
		{4, "Umbrella", "analytics@umbrella.example", "smb", "United Kingdom", time.Now().AddDate(0, -8, 0)},
		{5, "Hooli", "growth@hooli.example", "enterprise", "United States", time.Now().AddDate(-3, 0, 0)},
		{6, "Soylent", "team@soylent.example", "smb", "Canada", time.Now().AddDate(0, -5, 0)},
		{7, "Vandelay", "imports@vandelay.example", "mid-market", "United States", time.Now().AddDate(-1, -6, 0)},
		{8, "Wonka", "insights@wonka.example", "enterprise", "United Kingdom", time.Now().AddDate(-2, -2, 0)},
		{9, "Stark Industries", "data@stark.example", "enterprise", "United States", time.Now().AddDate(-4, 0, 0)},
		{10, "Wayne Enterprises", "analytics@wayne.example", "enterprise", "United States", time.Now().AddDate(-5, 0, 0)},
		{11, "Cyberdyne", "systems@cyberdyne.example", "mid-market", "Japan", time.Now().AddDate(-1, -9, 0)},
		{12, "Tyrell Corp", "replicants@tyrell.example", "enterprise", "Singapore", time.Now().AddDate(-2, -6, 0)},
	}
	products := []product{
		{1, "Atlas Analytics", "Analytics", 499},
		{2, "Orbit CRM", "CRM", 299},
		{3, "Pulse BI", "Analytics", 199},
		{4, "Forge ETL", "Data Engineering", 799},
		{5, "Relay API", "Infrastructure", 149},
		{6, "Sentinel Audit", "Security", 399},
		{7, "Prism Warehouse", "Data Engineering", 899},
		{8, "Beacon Alerts", "Observability", 249},
	}
	regionByCountry := map[string]string{
		"United States": "NA", "Canada": "NA", "Germany": "EU", "United Kingdom": "EU",
		"Japan": "APAC", "Singapore": "APAC",
	}

	if err := ensureCollection(ctx, db, "customers", func() error {
		var docs []any
		for _, c := range customers {
			docs = append(docs, bson.M{
				"id": c.ID, "name": c.Name, "email": c.Email, "segment": c.Segment,
				"country": c.Country, "created_at": c.CreatedAt.UTC(),
			})
		}
		_, err := db.Collection("customers").InsertMany(ctx, docs)
		return err
	}); err != nil {
		return err
	}
	if err := ensureCollection(ctx, db, "orders", func() error {
		var docs []any
		now := time.Now().UTC()
		for i := 0; i < 36; i++ {
			c := customers[i%len(customers)]
			p := products[(i*3)%len(products)]
			qty := i%4 + 1
			status := "paid"
			switch {
			case i%9 == 0:
				status = "refunded"
			case i%7 == 0:
				status = "pending"
			}
			docs = append(docs, bson.M{
				"id": i + 1, "customer_id": c.ID, "product_id": p.ID, "quantity": qty,
				"total": float64(qty) * p.Price, "status": status,
				"region": regionByCountry[c.Country], "created_at": now.AddDate(0, 0, -(i * 2)),
			})
		}
		_, err := db.Collection("orders").InsertMany(ctx, docs)
		return err
	}); err != nil {
		return err
	}
	return nil
}

func ensureCollection(ctx context.Context, db *mongo.Database, name string, seed func() error) error {
	count, err := db.Collection(name).CountDocuments(ctx, bson.M{})
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return seed()
}

func seedSQLiteDemo() (string, error) {
	dir, err := filepath.Abs("data")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "demo.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return "", err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS products (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		price REAL NOT NULL
	)`); err != nil {
		return "", err
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM products`).Scan(&n); err != nil {
		return "", err
	}
	products := []product{
		{1, "Atlas Analytics", "Analytics", 499},
		{2, "Orbit CRM", "CRM", 299},
		{3, "Pulse BI", "Analytics", 199},
		{4, "Forge ETL", "Data Engineering", 799},
		{5, "Relay API", "Infrastructure", 149},
		{6, "Sentinel Audit", "Security", 399},
		{7, "Prism Warehouse", "Data Engineering", 899},
		{8, "Beacon Alerts", "Observability", 249},
	}
	if n == 0 {
		tx, err := db.Begin()
		if err != nil {
			return "", err
		}
		for _, p := range products {
			if _, err := tx.Exec(`INSERT INTO products (id, name, category, price) VALUES (?, ?, ?, ?)`, p.ID, p.Name, p.Category, p.Price); err != nil {
				_ = tx.Rollback()
				return "", err
			}
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
	}
	return path, nil
}

func ensureDataSource(ctx context.Context, st *store.Store, key *cryptox.Key, tenant *model.Tenant, admin *model.User, name, typ string, cfg map[string]string) error {
	if _, err := st.GetDataSourceByName(ctx, tenant.ID, name); err == nil {
		return nil
	} else if err != store.ErrNotFound {
		return err
	}
	encrypted, err := key.EncryptJSON(cfg)
	if err != nil {
		return err
	}
	now := store.Now()
	ds := &model.DataSource{
		ID: store.NewID(), TenantID: tenant.ID, Name: name, Type: typ, Config: encrypted,
		Status: "error", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := connector.Open(openCtx, typ, cfg)
	if err == nil {
		tables, ierr := conn.Introspect(openCtx)
		_ = conn.Close()
		if ierr == nil {
			ds.Tables = tables
			ds.Status = "ready"
			ds.LastSyncedAt = &now
		} else {
			ds.LastError = ierr.Error()
		}
	} else {
		ds.LastError = err.Error()
	}
	if err := st.CreateDataSource(ctx, ds); err != nil {
		return err
	}
	fmt.Printf("  Data source:   %s (%s, %d tables)\n", name, typ, len(ds.Tables))
	return nil
}

func ensureSavedQuery(ctx context.Context, st *store.Store, tenant *model.Tenant, admin *model.User, name, desc, query string) error {
	list, err := st.ListSavedQueries(ctx, tenant.ID, admin.ID, true)
	if err != nil {
		return err
	}
	for _, q := range list {
		if q.Name == name {
			return nil
		}
	}
	now := store.Now()
	return st.CreateSavedQuery(ctx, &model.SavedQuery{
		ID: store.NewID(), TenantID: tenant.ID, Name: name, Description: desc, SQL: query,
		Visibility: "team", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	})
}
