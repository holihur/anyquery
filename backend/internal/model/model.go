// Package model contains persisted entities. IDs are 24-char ObjectID hex
// strings, stored as strings so API code never has to deal with bson types.
package model

import (
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

// MaxSavedQueriesPerWorkspace caps the shared query library. The API returns a
// clear conflict when the limit is reached.
const MaxSavedQueriesPerWorkspace = 100

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

func (r Role) CanQuery() bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleMember || r == RoleViewer
}
func (r Role) CanEdit() bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleMember
}
func (r Role) CanAdmin() bool {
	return r == RoleOwner || r == RoleAdmin
}
func (r Role) CanManageOwners() bool { return r == RoleOwner }

func ValidRole(r Role) bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleMember, RoleViewer:
		return true
	}
	return false
}

type User struct {
	ID          string    `bson:"_id" json:"id"`
	Email       string    `bson:"email" json:"email"`
	Name        string    `bson:"name" json:"name"`
	AvatarURL   string    `bson:"avatarUrl,omitempty" json:"avatarUrl,omitempty"`
	OIDCIssuer  string    `bson:"oidcIssuer,omitempty" json:"-"`
	OIDCSubject string    `bson:"oidcSubject,omitempty" json:"-"`
	CreatedAt   time.Time `bson:"createdAt" json:"createdAt"`
	LastLoginAt time.Time `bson:"lastLoginAt" json:"lastLoginAt"`
}

type Tenant struct {
	ID        string    `bson:"_id" json:"id"`
	Slug      string    `bson:"slug" json:"slug"`
	Name      string    `bson:"name" json:"name"`
	CreatedBy string    `bson:"createdBy" json:"createdBy"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

type Membership struct {
	ID        string    `bson:"_id" json:"id"`
	TenantID  string    `bson:"tenantId" json:"tenantId"`
	UserID    string    `bson:"userId" json:"userId"`
	Role      Role      `bson:"role" json:"role"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}

type Invite struct {
	ID         string     `bson:"_id" json:"id"`
	TenantID   string     `bson:"tenantId" json:"tenantId"`
	Email      string     `bson:"email" json:"email"`
	Role       Role       `bson:"role" json:"role"`
	TokenHash  string     `bson:"tokenHash" json:"-"`
	InvitedBy  string     `bson:"invitedBy" json:"invitedBy"`
	CreatedAt  time.Time  `bson:"createdAt" json:"createdAt"`
	ExpiresAt  time.Time  `bson:"expiresAt" json:"expiresAt"`
	AcceptedAt *time.Time `bson:"acceptedAt,omitempty" json:"acceptedAt,omitempty"`
}

type Session struct {
	ID         string    `bson:"_id" json:"-"` // token hash
	UserID     string    `bson:"userId" json:"-"`
	CreatedAt  time.Time `bson:"createdAt" json:"-"`
	ExpiresAt  time.Time `bson:"expiresAt" json:"-"`
	LastSeenAt time.Time `bson:"lastSeenAt" json:"-"`
}

type OAuthState struct {
	ID        string    `bson:"_id"` // state value
	Nonce     string    `bson:"nonce"`
	Verifier  string    `bson:"verifier"`
	ReturnTo  string    `bson:"returnTo"`
	ExpiresAt time.Time `bson:"expiresAt"`
}

type DataSource struct {
	ID           string            `bson:"_id" json:"id"`
	TenantID     string            `bson:"tenantId" json:"tenantId"`
	Name         string            `bson:"name" json:"name"`
	Type         string            `bson:"type" json:"type"`
	Config       string            `bson:"config" json:"-"` // encrypted JSON
	Tables       []connector.Table `bson:"tables" json:"tables"`
	Status       string            `bson:"status" json:"status"` // ready | error
	LastError    string            `bson:"lastError,omitempty" json:"lastError,omitempty"`
	CreatedBy    string            `bson:"createdBy" json:"createdBy"`
	CreatedAt    time.Time         `bson:"createdAt" json:"createdAt"`
	UpdatedAt    time.Time         `bson:"updatedAt" json:"updatedAt"`
	LastSyncedAt *time.Time        `bson:"lastSyncedAt,omitempty" json:"lastSyncedAt,omitempty"`
}

type SavedQuery struct {
	ID          string     `bson:"_id" json:"id"`
	TenantID    string     `bson:"tenantId" json:"tenantId"`
	Name        string     `bson:"name" json:"name"`
	Description string     `bson:"description,omitempty" json:"description,omitempty"`
	SQL         string     `bson:"sql" json:"sql"`
	Visibility  string     `bson:"visibility" json:"visibility"` // team | private
	CreatedBy   string     `bson:"createdBy" json:"createdBy"`
	CreatedAt   time.Time  `bson:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time  `bson:"updatedAt" json:"updatedAt"`
	LastRunAt   *time.Time `bson:"lastRunAt,omitempty" json:"lastRunAt,omitempty"`
}

type QueryRun struct {
	ID           string    `bson:"_id" json:"id"`
	TenantID     string    `bson:"tenantId" json:"tenantId"`
	UserID       string    `bson:"userId" json:"userId"`
	SavedQueryID string    `bson:"savedQueryId,omitempty" json:"savedQueryId,omitempty"`
	SQL          string    `bson:"sql" json:"sql"`
	Status       string    `bson:"status" json:"status"` // success | error
	DurationMS   int64     `bson:"durationMs" json:"durationMs"`
	RowCount     int       `bson:"rowCount" json:"rowCount"`
	Error        string    `bson:"error,omitempty" json:"error,omitempty"`
	Sources      []string  `bson:"sources" json:"sources"`
	CreatedAt    time.Time `bson:"createdAt" json:"createdAt"`
}

type AuditLog struct {
	ID         string         `bson:"_id" json:"id"`
	TenantID   string         `bson:"tenantId" json:"tenantId"`
	ActorID    string         `bson:"actorId" json:"actorId"`
	ActorName  string         `bson:"actorName" json:"actorName"`
	Action     string         `bson:"action" json:"action"`
	TargetType string         `bson:"targetType,omitempty" json:"targetType,omitempty"`
	TargetID   string         `bson:"targetId,omitempty" json:"targetId,omitempty"`
	Metadata   map[string]any `bson:"metadata,omitempty" json:"metadata,omitempty"`
	IP         string         `bson:"ip,omitempty" json:"ip,omitempty"`
	CreatedAt  time.Time      `bson:"createdAt" json:"createdAt"`
}

type APIKey struct {
	ID         string     `bson:"_id" json:"id"`
	TenantID   string     `bson:"tenantId" json:"tenantId"`
	Name       string     `bson:"name" json:"name"`
	Prefix     string     `bson:"prefix" json:"prefix"`
	KeyHash    string     `bson:"keyHash" json:"-"`
	CreatedBy  string     `bson:"createdBy" json:"createdBy"`
	CreatedAt  time.Time  `bson:"createdAt" json:"createdAt"`
	LastUsedAt *time.Time `bson:"lastUsedAt,omitempty" json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `bson:"revokedAt,omitempty" json:"revokedAt,omitempty"`
}
