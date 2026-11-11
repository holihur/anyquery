package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

type memberDTO struct {
	User      model.User `json:"user"`
	Role      model.Role `json:"role"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *Server) handleMembersList(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	memberships, err := s.store.ListMemberships(r.Context(), t.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	ids := make([]string, len(memberships))
	for i, m := range memberships {
		ids[i] = m.UserID
	}
	users, _ := s.store.GetUsers(r.Context(), ids)
	byID := map[string]model.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	out := make([]memberDTO, 0, len(memberships))
	for _, m := range memberships {
		if u, ok := byID[m.UserID]; ok {
			out = append(out, memberDTO{User: u, Role: m.Role, CreatedAt: m.CreatedAt})
		}
	}
	resp := map[string]any{"members": out, "role": currentRole(r)}
	if currentRole(r).CanAdmin() {
		invites, _ := s.store.ListInvites(r.Context(), t.ID)
		resp["invites"] = invites
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMemberUpdate(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can change member roles")
		return
	}
	targetID := r.PathValue("userID")
	target, err := s.store.GetMembership(r.Context(), t.ID, targetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Member not found")
		return
	}
	var body struct {
		Role model.Role `json:"role"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if !model.ValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "invalid_role", "Invalid role")
		return
	}
	if body.Role == model.RoleOwner && !currentRole(r).CanManageOwners() {
		writeError(w, http.StatusForbidden, "forbidden", "Only the workspace owner can transfer ownership")
		return
	}
	if target.Role == model.RoleOwner && !currentRole(r).CanManageOwners() {
		writeError(w, http.StatusForbidden, "forbidden", "Only the workspace owner can change another owner")
		return
	}
	if target.Role == model.RoleOwner && body.Role != model.RoleOwner {
		count, _ := s.store.CountOwners(r.Context(), t.ID)
		if count <= 1 {
			writeError(w, http.StatusBadRequest, "last_owner", "The workspace must keep at least one owner")
			return
		}
	}
	if err := s.store.UpdateMemberRole(r.Context(), t.ID, targetID, body.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "member.role", TargetType: "user", TargetID: targetID, Metadata: map[string]any{"from": target.Role, "to": body.Role}, IP: clientIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMemberDelete(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can remove members")
		return
	}
	targetID := r.PathValue("userID")
	target, err := s.store.GetMembership(r.Context(), t.ID, targetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Member not found")
		return
	}
	if target.Role == model.RoleOwner {
		count, _ := s.store.CountOwners(r.Context(), t.ID)
		if count <= 1 {
			writeError(w, http.StatusBadRequest, "last_owner", "The workspace must keep at least one owner")
			return
		}
		if !currentRole(r).CanManageOwners() {
			writeError(w, http.StatusForbidden, "forbidden", "Only the owner can remove another owner")
			return
		}
	}
	if err := s.store.RemoveMember(r.Context(), t.ID, targetID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "member.remove", TargetType: "user", TargetID: targetID, IP: clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInvitesList(w http.ResponseWriter, r *http.Request) {
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can view invitations")
		return
	}
	invites, err := s.store.ListInvites(r.Context(), currentTenant(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": invites})
}

func (s *Server) handleInviteCreate(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can invite members")
		return
	}
	var body struct {
		Email string     `json:"email"`
		Role  model.Role `json:"role"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !strings.Contains(email, "@") {
		writeError(w, http.StatusBadRequest, "invalid_email", "A valid email is required")
		return
	}
	if body.Role == "" {
		body.Role = model.RoleMember
	}
	if !model.ValidRole(body.Role) || body.Role == model.RoleOwner {
		writeError(w, http.StatusBadRequest, "invalid_role", "Invite role must be admin, member or viewer")
		return
	}
	if existing, err := s.store.GetUserByEmail(r.Context(), email); err == nil {
		if _, err := s.store.GetMembership(r.Context(), t.ID, existing.ID); err == nil {
			writeError(w, http.StatusConflict, "already_member", "This person is already a member")
			return
		}
	}
	token, err := cryptox.RandomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	inv, err := s.store.CreateInvite(r.Context(), model.Invite{
		ID: store.NewID(), TenantID: t.ID, Email: email, Role: body.Role,
		TokenHash: cryptox.HashToken(token), InvitedBy: u.ID, CreatedAt: store.Now(),
		ExpiresAt: store.Now().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "invite.create", TargetType: "invite", TargetID: inv.ID, Metadata: map[string]any{"email": email, "role": body.Role}, IP: clientIP(r)})
	writeJSON(w, http.StatusCreated, map[string]any{
		"invite": inv,
		"link":   s.cfg.BaseURL + "/invite/" + token,
		"token":  token,
	})
}

func (s *Server) handleInviteDelete(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can revoke invitations")
		return
	}
	err := s.store.DeleteInvite(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "invite.revoke", TargetType: "invite", TargetID: r.PathValue("id"), IP: clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

var _ = errors.Is
