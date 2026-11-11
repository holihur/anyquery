// Package mongodb implements the MongoDB connector.
package mongodb

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "mongodb",
		Name:        "MongoDB",
		Category:    connector.CategoryNoSQL,
		Description: "MongoDB collections, queried with SQL and joins",
		Icon:        "mongodb",
		Fields: []connector.ConfigField{
			{Key: "uri", Label: "Connection URI", Kind: connector.FieldTextarea, Required: true, Secret: true, Placeholder: "mongodb://user:pass@host:27017/?authSource=admin", Help: "Standard MongoDB connection string. Prefer a read-only user."},
			{Key: "database", Label: "Database", Kind: connector.FieldText, Required: true, Placeholder: "analytics"},
			{Key: "sample_size", Label: "Documents sampled for schema", Kind: connector.FieldNumber, Default: "30", Help: "Introspection samples documents to infer column types."},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	uri := strings.TrimSpace(cfg["uri"])
	dbName := strings.TrimSpace(cfg["database"])
	if uri == "" || dbName == "" {
		return nil, fmt.Errorf("MongoDB URI and database are required")
	}
	opts := options.Client().ApplyURI(uri).SetServerSelectionTimeout(8 * time.Second)
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	c := &Conn{client: client, db: client.Database(dbName), sampleSize: 30}
	if n := cfg["sample_size"]; n != "" {
		var parsed int
		if _, err := fmt.Sscanf(n, "%d", &parsed); err == nil && parsed > 0 && parsed <= 500 {
			c.sampleSize = parsed
		}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return c, nil
}

type Conn struct {
	client     *mongo.Client
	db         *mongo.Database
	sampleSize int
}

func (c *Conn) Ping(ctx context.Context) error {
	if err := c.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("MongoDB ping failed: %w", err)
	}
	return nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true, ProjectionPushdown: true}
}

func (c *Conn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.client.Disconnect(ctx)
}

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	names, err := c.db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	sort.Strings(names)
	if len(names) > 500 {
		names = names[:500]
	}
	var tables []connector.Table
	for _, name := range names {
		if strings.HasPrefix(name, "system.") {
			continue
		}
		t := connector.Table{Name: name, DisplayName: name, Kind: "collection"}
		acc := map[string]*connector.Column{}
		pipeline := mongo.Pipeline{{{"$sample", bson.M{"size": c.sampleSize}}}}
		cur, err := c.db.Collection(name).Aggregate(ctx, pipeline)
		if err == nil {
			var docs []bson.M
			if err := cur.All(ctx, &docs); err == nil {
				for _, doc := range docs {
					norm, _ := normalizeDocument(doc).(map[string]any)
					for k, v := range norm {
						col, ok := acc[k]
						if !ok {
							acc[k] = &connector.Column{Name: k, Type: inferType(v), Nullable: v == nil}
							continue
						}
						if v == nil {
							col.Nullable = true
							continue
						}
						if col.Type == "null" {
							col.Type = inferType(v)
						}
					}
				}
			}
			_ = cur.Close(ctx)
		}
		for _, col := range acc {
			t.Columns = append(t.Columns, *col)
		}
		sort.Slice(t.Columns, func(i, j int) bool { return t.Columns[i].Name < t.Columns[j].Name })
		tables = append(tables, t)
	}
	connector.SortTables(tables)
	return tables, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 1000
	}
	if req.Limit > 100001 {
		req.Limit = 100001
	}
	opts := options.Find().SetLimit(int64(req.Limit))
	if len(req.Columns) > 0 {
		projection := bson.M{}
		for _, col := range req.Columns {
			projection[col] = 1
		}
		opts.SetProjection(projection)
	}
	cur, err := c.db.Collection(req.Table).Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, fmt.Errorf("find %s: %w", req.Table, err)
	}
	defer cur.Close(ctx)
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	result := &connector.FetchResult{}
	acc := map[string]*connector.Column{}
	for _, doc := range docs {
		norm, _ := normalizeDocument(doc).(map[string]any)
		row := make(map[string]any, len(norm))
		for k, v := range norm {
			row[k] = v
			col, ok := acc[k]
			if !ok {
				acc[k] = &connector.Column{Name: k, Type: inferType(v), Nullable: v == nil}
			} else if v == nil {
				col.Nullable = true
			}
		}
		result.Rows = append(result.Rows, row)
	}
	for _, col := range acc {
		result.Columns = append(result.Columns, *col)
	}
	sort.Slice(result.Columns, func(i, j int) bool { return result.Columns[i].Name < result.Columns[j].Name })
	if len(result.Rows) >= req.Limit {
		result.Truncated = true
	}
	return result, nil
}

func normalizeDocument(v any) any {
	switch x := v.(type) {
	case primitive.ObjectID:
		return x.Hex()
	case primitive.DateTime:
		return x.Time().UTC().Format(time.RFC3339Nano)
	case primitive.Timestamp:
		return time.Unix(int64(x.T), 0).UTC().Format(time.RFC3339Nano)
	case primitive.Decimal128:
		return x.String()
	case primitive.Binary:
		return base64.StdEncoding.EncodeToString(x.Data)
	case primitive.D:
		out := make(map[string]any, len(x))
		for _, e := range x {
			out[e.Key] = normalizeDocument(e.Value)
		}
		return out
	case bson.M:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalizeDocument(v)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalizeDocument(v)
		}
		return out
	case bson.A:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeDocument(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeDocument(v)
		}
		return out
	case primitive.Regex:
		return "/" + x.Pattern + "/" + x.Options
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return v
	}
}

func inferType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int32, int64, uint, uint32, uint64:
		return "integer"
	case float32, float64, primitive.Decimal128:
		return "number"
	case time.Time, primitive.DateTime, primitive.Timestamp:
		return "timestamp"
	case map[string]any, bson.M, primitive.D:
		return "object"
	case []any, bson.A:
		return "array"
	default:
		return "string"
	}
}
