package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/anyquery/anyquery/internal/model"
)

var ErrNotFound = errors.New("not found")

// Store wraps MongoDB.
type Store struct {
	client *mongo.Client
	db     *mongo.Database
}

func New(ctx context.Context, uri, dbName string) (*Store, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetServerSelectionTimeout(10*time.Second))
	if err != nil {
		return nil, fmt.Errorf("connect MongoDB: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}
	s := &Store{client: client, db: client.Database(dbName)}
	if err := s.ensureIndexes(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

func (s *Store) Users() *mongo.Collection        { return s.db.Collection("users") }
func (s *Store) Tenants() *mongo.Collection      { return s.db.Collection("tenants") }
func (s *Store) Memberships() *mongo.Collection  { return s.db.Collection("memberships") }
func (s *Store) Invites() *mongo.Collection      { return s.db.Collection("invites") }
func (s *Store) Sessions() *mongo.Collection     { return s.db.Collection("sessions") }
func (s *Store) OAuthStates() *mongo.Collection  { return s.db.Collection("oauthStates") }
func (s *Store) DataSources() *mongo.Collection  { return s.db.Collection("datasources") }
func (s *Store) SavedQueries() *mongo.Collection { return s.db.Collection("savedQueries") }
func (s *Store) QueryRuns() *mongo.Collection    { return s.db.Collection("queryRuns") }
func (s *Store) AuditLogs() *mongo.Collection    { return s.db.Collection("auditLogs") }
func (s *Store) APIKeys() *mongo.Collection      { return s.db.Collection("apiKeys") }

func (s *Store) ensureIndexes(ctx context.Context) error {
	yes := true
	specs := []struct {
		coll string
		idx  []mongo.IndexModel
	}{
		{"users", []mongo.IndexModel{
			{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "oidcIssuer", Value: 1}, {Key: "oidcSubject", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		}},
		{"tenants", []mongo.IndexModel{
			{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetUnique(true)},
		}},
		{"memberships", []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "userId", Value: 1}}},
		}},
		{"invites", []mongo.IndexModel{
			{Keys: bson.D{{Key: "tokenHash", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "email", Value: 1}}},
		}},
		{"sessions", []mongo.IndexModel{
			{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
			{Keys: bson.D{{Key: "userId", Value: 1}}},
		}},
		{"oauthStates", []mongo.IndexModel{
			{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		}},
		{"datasources", []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)},
		}},
		{"savedQueries", []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "updatedAt", Value: -1}}},
		}},
		{"queryRuns", []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "createdAt", Value: -1}}},
		}},
		{"auditLogs", []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "createdAt", Value: -1}}},
		}},
		{"apiKeys", []mongo.IndexModel{
			{Keys: bson.D{{Key: "keyHash", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "createdAt", Value: -1}}},
		}},
	}
	for _, spec := range specs {
		if _, err := s.db.Collection(spec.coll).Indexes().CreateMany(ctx, spec.idx); err != nil {
			return fmt.Errorf("create indexes on %s: %w", spec.coll, err)
		}
	}
	_ = yes
	return nil
}

func NewID() string { return primitive.NewObjectID().Hex() }

func Now() time.Time { return time.Now().UTC() }

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ---- users -----------------------------------------------------------------

func (s *Store) GetUser(ctx context.Context, id string) (*model.User, error) {
	var u model.User
	err := s.Users().FindOne(ctx, bson.M{"_id": id}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	var u model.User
	err := s.Users().FindOne(ctx, bson.M{"email": normalizeEmail(email)}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUsers(ctx context.Context, ids []string) ([]model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cur, err := s.Users().Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	out := make([]model.User, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertDevUser creates or updates a development-mode user.
func (s *Store) UpsertDevUser(ctx context.Context, email, name string) (*model.User, error) {
	email = normalizeEmail(email)
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	name = strings.TrimSpace(name)
	now := Now()
	set := bson.M{"email": email, "lastLoginAt": now}
	setOnInsert := bson.M{"_id": NewID(), "createdAt": now}
	if name != "" {
		set["name"] = name
	} else {
		setOnInsert["name"] = strings.Split(email, "@")[0]
	}
	update := bson.M{"$set": set, "$setOnInsert": setOnInsert}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var u model.User
	err := s.Users().FindOneAndUpdate(ctx, bson.M{"email": email}, update, opts).Decode(&u)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpsertOIDCUser links an OIDC identity to a user, refusing to hijack an
// existing email unless the identity is verified.
func (s *Store) UpsertOIDCUser(ctx context.Context, issuer, subject, email, name, avatar string, emailVerified bool) (*model.User, error) {
	email = normalizeEmail(email)
	if subject == "" {
		return nil, fmt.Errorf("OIDC subject is required")
	}
	var byOIDC model.User
	err := s.Users().FindOne(ctx, bson.M{"oidcIssuer": issuer, "oidcSubject": subject}).Decode(&byOIDC)
	if err == nil {
		now := Now()
		_, err = s.Users().UpdateOne(ctx, bson.M{"_id": byOIDC.ID}, bson.M{"$set": bson.M{
			"name": name, "avatarUrl": avatar, "lastLoginAt": now,
		}})
		if err != nil {
			return nil, err
		}
		byOIDC.Name, byOIDC.AvatarURL, byOIDC.LastLoginAt = name, avatar, now
		return &byOIDC, nil
	}
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}

	// Link to an existing account only when the provider verified the email.
	if email != "" {
		existing, err := s.GetUserByEmail(ctx, email)
		if err == nil {
			if existing.OIDCSubject != "" && (existing.OIDCIssuer != issuer || existing.OIDCSubject != subject) {
				return nil, fmt.Errorf("email is already linked to another identity")
			}
			if !emailVerified {
				return nil, fmt.Errorf("refusing to link an unverified email to an existing account")
			}
			now := Now()
			_, err = s.Users().UpdateOne(ctx, bson.M{"_id": existing.ID}, bson.M{"$set": bson.M{
				"oidcIssuer": issuer, "oidcSubject": subject, "name": name,
				"avatarUrl": avatar, "lastLoginAt": now, "emailVerified": true,
			}})
			if err != nil {
				return nil, err
			}
			existing.OIDCIssuer, existing.OIDCSubject, existing.LastLoginAt = issuer, subject, now
			return existing, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if email == "" {
		email = subject + "@" + strings.TrimPrefix(strings.TrimPrefix(issuer, "https://"), "http://")
	}
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	now := Now()
	u := model.User{
		ID: NewID(), Email: email, Name: name, AvatarURL: avatar,
		OIDCIssuer: issuer, OIDCSubject: subject, CreatedAt: now, LastLoginAt: now,
	}
	if _, err := s.Users().InsertOne(ctx, u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ---- sessions --------------------------------------------------------------

func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration, tokenHash string) error {
	now := Now()
	_, err := s.Sessions().InsertOne(ctx, model.Session{
		ID: tokenHash, UserID: userID, CreatedAt: now, ExpiresAt: now.Add(ttl), LastSeenAt: now,
	})
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (*model.Session, error) {
	var sess model.Session
	err := s.Sessions().FindOne(ctx, bson.M{"_id": tokenHash, "expiresAt": bson.M{"$gt": Now()}}).Decode(&sess)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) TouchSession(ctx context.Context, tokenHash string) {
	_, _ = s.Sessions().UpdateOne(ctx, bson.M{"_id": tokenHash}, bson.M{"$set": bson.M{"lastSeenAt": Now()}})
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.Sessions().DeleteOne(ctx, bson.M{"_id": tokenHash})
	return err
}

func (s *Store) CreateOAuthState(ctx context.Context, st model.OAuthState) error {
	_, err := s.OAuthStates().InsertOne(ctx, st)
	return err
}

func (s *Store) TakeOAuthState(ctx context.Context, state string) (*model.OAuthState, error) {
	var st model.OAuthState
	err := s.OAuthStates().FindOneAndDelete(ctx, bson.M{"_id": state, "expiresAt": bson.M{"$gt": Now()}}).Decode(&st)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}
