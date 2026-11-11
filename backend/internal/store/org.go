package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/anyquery/anyquery/internal/model"
)

type TenantMembership struct {
	Tenant model.Tenant
	Role   model.Role
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "workspace"
	}
	return s
}

// CreateTenant creates a tenant and makes userID its owner.
func (s *Store) CreateTenant(ctx context.Context, name, slug, userID string) (*model.Tenant, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("workspace name is required")
	}
	if slug == "" {
		slug = Slugify(name)
	}
	base := Slugify(slug)
	now := Now()
	for i := 0; i < 50; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		t := model.Tenant{ID: NewID(), Slug: candidate, Name: name, CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
		_, err := s.Tenants().InsertOne(ctx, t)
		if err == nil {
			m := model.Membership{ID: NewID(), TenantID: t.ID, UserID: userID, Role: model.RoleOwner, CreatedAt: now}
			if _, err := s.Memberships().InsertOne(ctx, m); err != nil {
				_, _ = s.Tenants().DeleteOne(ctx, bson.M{"_id": t.ID})
				return nil, err
			}
			return &t, nil
		}
		if !mongo.IsDuplicateKeyError(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a unique workspace slug")
}

func (s *Store) GetTenant(ctx context.Context, ref string) (*model.Tenant, error) {
	var t model.Tenant
	err := s.Tenants().FindOne(ctx, bson.M{"$or": bson.A{bson.M{"_id": ref}, bson.M{"slug": strings.ToLower(ref)}}}).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) UpdateTenant(ctx context.Context, id, name string) error {
	_, err := s.Tenants().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"name": strings.TrimSpace(name), "updatedAt": Now()}})
	return err
}

func (s *Store) ListTenantsForUser(ctx context.Context, userID string) ([]TenantMembership, error) {
	cur, err := s.Memberships().Find(ctx, bson.M{"userId": userID})
	if err != nil {
		return nil, err
	}
	var memberships []model.Membership
	if err := cur.All(ctx, &memberships); err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, nil
	}
	ids := make([]string, len(memberships))
	roleByTenant := map[string]model.Role{}
	for i, m := range memberships {
		ids[i] = m.TenantID
		roleByTenant[m.TenantID] = m.Role
	}
	tcur, err := s.Tenants().Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var tenants []model.Tenant
	if err := tcur.All(ctx, &tenants); err != nil {
		return nil, err
	}
	out := make([]TenantMembership, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, TenantMembership{Tenant: t, Role: roleByTenant[t.ID]})
	}
	return out, nil
}

func (s *Store) GetMembership(ctx context.Context, tenantID, userID string) (*model.Membership, error) {
	var m model.Membership
	err := s.Memberships().FindOne(ctx, bson.M{"tenantId": tenantID, "userId": userID}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) ListMemberships(ctx context.Context, tenantID string) ([]model.Membership, error) {
	cur, err := s.Memberships().Find(ctx, bson.M{"tenantId": tenantID}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := make([]model.Membership, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpsertMember(ctx context.Context, tenantID, userID string, role model.Role) error {
	_, err := s.Memberships().UpdateOne(ctx,
		bson.M{"tenantId": tenantID, "userId": userID},
		bson.M{"$set": bson.M{"role": role}, "$setOnInsert": bson.M{"_id": NewID(), "tenantId": tenantID, "userId": userID, "createdAt": Now()}},
		options.Update().SetUpsert(true))
	return err
}

func (s *Store) UpdateMemberRole(ctx context.Context, tenantID, userID string, role model.Role) error {
	res, err := s.Memberships().UpdateOne(ctx, bson.M{"tenantId": tenantID, "userId": userID}, bson.M{"$set": bson.M{"role": role}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RemoveMember(ctx context.Context, tenantID, userID string) error {
	_, err := s.Memberships().DeleteOne(ctx, bson.M{"tenantId": tenantID, "userId": userID})
	return err
}

func (s *Store) CountOwners(ctx context.Context, tenantID string) (int64, error) {
	return s.Memberships().CountDocuments(ctx, bson.M{"tenantId": tenantID, "role": model.RoleOwner})
}

// ---- invites ---------------------------------------------------------------

func (s *Store) CreateInvite(ctx context.Context, inv model.Invite) (*model.Invite, error) {
	// Invalidate older pending invites for the same email.
	_, _ = s.Invites().DeleteMany(ctx, bson.M{"tenantId": inv.TenantID, "email": inv.Email, "acceptedAt": bson.M{"$exists": false}})
	if _, err := s.Invites().InsertOne(ctx, inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *Store) ListInvites(ctx context.Context, tenantID string) ([]model.Invite, error) {
	cur, err := s.Invites().Find(ctx, bson.M{"tenantId": tenantID, "acceptedAt": bson.M{"$exists": false}})
	if err != nil {
		return nil, err
	}
	out := make([]model.Invite, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetInviteByToken(ctx context.Context, tokenHash string) (*model.Invite, error) {
	var inv model.Invite
	err := s.Invites().FindOne(ctx, bson.M{"tokenHash": tokenHash, "acceptedAt": bson.M{"$exists": false}}).Decode(&inv)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *Store) MarkInviteAccepted(ctx context.Context, id string) error {
	now := Now()
	_, err := s.Invites().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"acceptedAt": now}})
	return err
}

func (s *Store) DeleteInvite(ctx context.Context, tenantID, id string) error {
	_, err := s.Invites().DeleteOne(ctx, bson.M{"_id": id, "tenantId": tenantID})
	return err
}

// ---- audit & api keys ------------------------------------------------------

func (s *Store) Audit(ctx context.Context, entry model.AuditLog) {
	if entry.ID == "" {
		entry.ID = NewID()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = Now()
	}
	_, _ = s.AuditLogs().InsertOne(ctx, entry)
}

func (s *Store) ListAudit(ctx context.Context, tenantID string, limit int64) ([]model.AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	cur, err := s.AuditLogs().Find(ctx, bson.M{"tenantId": tenantID}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListAPIKeys(ctx context.Context, tenantID string) ([]model.APIKey, error) {
	cur, err := s.APIKeys().Find(ctx, bson.M{"tenantId": tenantID, "revokedAt": bson.M{"$exists": false}}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		return nil, err
	}
	out := make([]model.APIKey, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetAPIKeyByHash(ctx context.Context, hash string) (*model.APIKey, error) {
	var k model.APIKey
	err := s.APIKeys().FindOne(ctx, bson.M{"keyHash": hash, "revokedAt": bson.M{"$exists": false}}).Decode(&k)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *Store) TouchAPIKey(ctx context.Context, id string) {
	_, _ = s.APIKeys().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"lastUsedAt": Now()}})
}

func (s *Store) RevokeAPIKey(ctx context.Context, tenantID, id string) error {
	now := Now()
	res, err := s.APIKeys().UpdateOne(ctx, bson.M{"_id": id, "tenantId": tenantID}, bson.M{"$set": bson.M{"revokedAt": now}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}
