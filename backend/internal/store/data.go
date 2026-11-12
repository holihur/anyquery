package store

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/model"
)

// ---- data sources ----------------------------------------------------------

func (s *Store) ListDataSources(ctx context.Context, tenantID string) ([]model.DataSource, error) {
	cur, err := s.DataSources().Find(ctx, bson.M{"tenantId": tenantID}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := make([]model.DataSource, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetDataSource(ctx context.Context, tenantID, id string) (*model.DataSource, error) {
	var ds model.DataSource
	err := s.DataSources().FindOne(ctx, bson.M{"_id": id, "tenantId": tenantID}).Decode(&ds)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ds, nil
}

func (s *Store) GetDataSourceByName(ctx context.Context, tenantID, name string) (*model.DataSource, error) {
	var ds model.DataSource
	err := s.DataSources().FindOne(ctx, bson.M{"tenantId": tenantID, "name": name}).Decode(&ds)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ds, nil
}

func (s *Store) CreateDataSource(ctx context.Context, ds *model.DataSource) error {
	_, err := s.DataSources().InsertOne(ctx, ds)
	return err
}

func (s *Store) UpdateDataSourceConfig(ctx context.Context, tenantID, id, name, typ, encryptedConfig string) error {
	res, err := s.DataSources().UpdateOne(ctx, bson.M{"_id": id, "tenantId": tenantID}, bson.M{"$set": bson.M{
		"name": name, "type": typ, "config": encryptedConfig, "updatedAt": Now(),
	}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateDataSourceStatus(ctx context.Context, tenantID, id string, tables []connector.Table, status, lastError string) error {
	now := Now()
	set := bson.M{"tables": tables, "status": status, "lastError": lastError, "updatedAt": now}
	if status == "ready" {
		set["lastSyncedAt"] = now
	}
	res, err := s.DataSources().UpdateOne(ctx, bson.M{"_id": id, "tenantId": tenantID}, bson.M{"$set": set})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteDataSource(ctx context.Context, tenantID, id string) error {
	_, err := s.DataSources().DeleteOne(ctx, bson.M{"_id": id, "tenantId": tenantID})
	return err
}

// ---- saved queries ---------------------------------------------------------

func (s *Store) ListSavedQueries(ctx context.Context, tenantID, userID string, seeAll bool) ([]model.SavedQuery, error) {
	filter := bson.M{"tenantId": tenantID}
	if !seeAll {
		filter["$or"] = bson.A{bson.M{"visibility": "team"}, bson.M{"createdBy": userID}}
	}
	cur, err := s.SavedQueries().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}}).SetLimit(model.MaxSavedQueriesPerWorkspace))
	if err != nil {
		return nil, err
	}
	out := make([]model.SavedQuery, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) CountSavedQueries(ctx context.Context, tenantID string) (int64, error) {
	return s.SavedQueries().CountDocuments(ctx, bson.M{"tenantId": tenantID})
}

func (s *Store) GetSavedQuery(ctx context.Context, tenantID, id string) (*model.SavedQuery, error) {
	var q model.SavedQuery
	err := s.SavedQueries().FindOne(ctx, bson.M{"_id": id, "tenantId": tenantID}).Decode(&q)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

func (s *Store) CreateSavedQuery(ctx context.Context, q *model.SavedQuery) error {
	_, err := s.SavedQueries().InsertOne(ctx, q)
	return err
}

func (s *Store) UpdateSavedQuery(ctx context.Context, tenantID, id string, set bson.M) error {
	set["updatedAt"] = Now()
	res, err := s.SavedQueries().UpdateOne(ctx, bson.M{"_id": id, "tenantId": tenantID}, bson.M{"$set": set})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSavedQuery(ctx context.Context, tenantID, id string) error {
	_, err := s.SavedQueries().DeleteOne(ctx, bson.M{"_id": id, "tenantId": tenantID})
	return err
}

// ---- query runs ------------------------------------------------------------

func (s *Store) CreateRun(ctx context.Context, run *model.QueryRun) error {
	_, err := s.QueryRuns().InsertOne(ctx, run)
	return err
}

func (s *Store) ListRuns(ctx context.Context, tenantID string, limit int64) ([]model.QueryRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	cur, err := s.QueryRuns().Find(ctx, bson.M{"tenantId": tenantID}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	out := make([]model.QueryRun, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
