package store

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/anyquery/anyquery/internal/model"
)

// ---- dashboards ------------------------------------------------------------

func (s *Store) ListDashboards(ctx context.Context, tenantID string) ([]model.Dashboard, error) {
	cur, err := s.Dashboards().Find(ctx, bson.M{"tenantId": tenantID}, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}}).SetLimit(500))
	if err != nil {
		return nil, err
	}
	out := make([]model.Dashboard, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Panels == nil {
			out[i].Panels = []model.DashboardPanel{}
		}
	}
	return out, nil
}

func (s *Store) CountDashboards(ctx context.Context, tenantID string) (int64, error) {
	return s.Dashboards().CountDocuments(ctx, bson.M{"tenantId": tenantID})
}

func (s *Store) GetDashboard(ctx context.Context, tenantID, id string) (*model.Dashboard, error) {
	var d model.Dashboard
	err := s.Dashboards().FindOne(ctx, bson.M{"_id": id, "tenantId": tenantID}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if d.Panels == nil {
		d.Panels = []model.DashboardPanel{}
	}
	return &d, nil
}

func (s *Store) CreateDashboard(ctx context.Context, d *model.Dashboard) error {
	_, err := s.Dashboards().InsertOne(ctx, d)
	return err
}

func (s *Store) UpdateDashboard(ctx context.Context, tenantID, id, name, description string, panels []model.DashboardPanel) error {
	res, err := s.Dashboards().UpdateOne(ctx, bson.M{"_id": id, "tenantId": tenantID}, bson.M{"$set": bson.M{
		"name": name, "description": description, "panels": panels, "updatedAt": Now(),
	}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteDashboard(ctx context.Context, tenantID, id string) error {
	_, err := s.Dashboards().DeleteOne(ctx, bson.M{"_id": id, "tenantId": tenantID})
	return err
}
