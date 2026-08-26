package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/vcs"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type gitLabOAuthState struct {
	WorkspaceID string `json:"workspace_id"`
	Verifier    string `json:"verifier"`
	IssuedAt    int64  `json:"issued_at"`
}

func (h *Handler) StartGitLabOAuth(w http.ResponseWriter, r *http.Request) {
	if !h.isGitLabOAuthConfigured() {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "url": ""})
		return
	}
	ws := chi.URLParam(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, ws, "workspace id")
	if !ok {
		return
	}
	if member, found := middleware.MemberFromContext(r.Context()); !found || !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "workspace admin required")
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeError(w, 500, "failed to mint oauth state")
		return
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	state, err := h.sealVCSSecret(fmt.Sprintf(`{"workspace_id":%q,"verifier":%q,"issued_at":%d}`, wsUUID.String(), verifier, time.Now().Unix()))
	if err != nil {
		writeError(w, 500, "failed to seal oauth state")
		return
	}
	redirect := strings.TrimRight(h.cfg.PublicURL, "/") + "/api/gitlab/oauth/callback"
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "url": vcs.GitLabAuthorizeURL(h.cfg.GitLabInstanceURL, h.cfg.GitLabOAuthClientID, redirect, state, verifier)})
}

func (h *Handler) GitLabOAuthCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(reason string) {
		http.Redirect(w, r, strings.TrimRight(h.cfg.AppURL, "/")+"/settings?tab=integrations&gitlab_error="+urlQuery(reason), http.StatusFound)
	}
	state, err := h.openVCSSecret(r.URL.Query().Get("state"))
	if err != nil || state == "" {
		fail("invalid_state")
		return
	}
	var s gitLabOAuthState
	if err := json.Unmarshal([]byte(state), &s); err != nil || s.WorkspaceID == "" || s.Verifier == "" || time.Since(time.Unix(s.IssuedAt, 0)) > 10*time.Minute {
		fail("invalid_state")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequestValue(s.WorkspaceID)
	if !ok {
		fail("invalid_workspace")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail("authorization_denied")
		return
	}
	redirect := strings.TrimRight(h.cfg.PublicURL, "/") + "/api/gitlab/oauth/callback"
	tok, err := vcs.GitLabExchangeCode(r.Context(), h.cfg.GitLabInstanceURL, h.cfg.GitLabOAuthClientID, h.cfg.GitLabOAuthClientSecret, code, redirect, s.Verifier)
	if err != nil {
		fail("token_exchange")
		return
	}
	accountInfo, err := vcs.ValidateGitLabOAuthToken(r.Context(), h.cfg.GitLabInstanceURL, tok.AccessToken)
	if err != nil {
		fail("account_lookup")
		return
	}
	account := accountInfo.Login
	secret := ""
	if existing, lookupErr := h.Queries.GetVCSConnectionByWorkspaceAndInstance(r.Context(), db.GetVCSConnectionByWorkspaceAndInstanceParams{WorkspaceID: wsUUID, InstanceUrl: h.cfg.GitLabInstanceURL}); lookupErr == nil {
		secret, err = h.openVCSSecret(existing.WebhookSecretEncrypted)
	} else {
		secret, err = newVCSWebhookSecret()
	}
	if err != nil {
		fail("secret")
		return
	}
	secretEnc, err := h.sealVCSSecret(secret)
	if err != nil {
		fail("secret")
		return
	}
	accessEnc, err := h.sealVCSSecret(tok.AccessToken)
	if err != nil {
		fail("token")
		return
	}
	refreshEnc, err := h.sealVCSSecret(tok.RefreshToken)
	if err != nil {
		fail("token")
		return
	}
	var connectedBy pgtype.UUID
	if member, found := middleware.MemberFromContext(r.Context()); found {
		connectedBy = member.UserID
	}
	conn, err := h.Queries.UpsertVCSOAuthConnection(r.Context(), db.UpsertVCSOAuthConnectionParams{WorkspaceID: wsUUID, InstanceUrl: h.cfg.GitLabInstanceURL, AccountLogin: account, AccessTokenEncrypted: accessEnc, WebhookSecretEncrypted: secretEnc, ConnectedByID: connectedBy, RefreshTokenEncrypted: refreshEnc, AccessTokenExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second), Valid: tok.ExpiresIn > 0}})
	if err != nil {
		fail("save")
		return
	}
	h.publish("vcs.connection.created", s.WorkspaceID, "system", "", map[string]any{"id": uuidToString(conn.ID)})
	http.Redirect(w, r, strings.TrimRight(h.cfg.AppURL, "/")+"/settings?tab=integrations&gitlab_connected=1", http.StatusFound)
}

func (h *Handler) ListGitLabTargets(w http.ResponseWriter, r *http.Request) {
	conn, ok := h.loadGitLabConnection(w, r)
	if !ok {
		return
	}
	tok, err := h.openVCSSecret(conn.AccessTokenEncrypted)
	if err != nil {
		writeError(w, 409, "reconnect GitLab")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per < 1 || per > 100 {
		per = 25
	}
	projects, next, err := vcs.GitLabListProjects(r.Context(), conn.InstanceUrl, vcs.GitLabCredential{Token: tok, OAuth: conn.AuthKind == "oauth"}, page, per)
	if err != nil {
		writeError(w, 502, "could not list GitLab projects")
		return
	}
	groups, _, err := vcs.GitLabListGroups(r.Context(), conn.InstanceUrl, vcs.GitLabCredential{Token: tok, OAuth: conn.AuthKind == "oauth"}, page, per)
	if err != nil {
		writeError(w, 502, "could not list GitLab groups")
		return
	}
	writeJSON(w, 200, map[string]any{"projects": projects, "groups": groups, "next_page": next})
}

func (h *Handler) loadGitLabConnection(w http.ResponseWriter, r *http.Request) (db.VcsConnection, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "connectionId"), "connection id")
	if !ok {
		return db.VcsConnection{}, false
	}
	conn, err := h.Queries.GetVCSConnectionByID(r.Context(), id)
	if err != nil || conn.Provider != "gitlab" {
		writeError(w, 404, "GitLab connection not found")
		return db.VcsConnection{}, false
	}
	return conn, true
}

type vcsHookRequest struct {
	Scope      string `json:"scope"`
	TargetID   int64  `json:"target_id"`
	TargetPath string `json:"target_path"`
}

func (h *Handler) CreateVCSWebhookRegistration(w http.ResponseWriter, r *http.Request) {
	conn, ok := h.loadGitLabConnection(w, r)
	if !ok {
		return
	}
	var in vcsHookRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || (in.Scope != "project" && in.Scope != "group") {
		writeError(w, 400, "scope and target_id are required")
		return
	}
	tok, err := h.openVCSSecret(conn.AccessTokenEncrypted)
	if err != nil {
		writeError(w, 409, "reconnect GitLab")
		return
	}
	secret, err := h.openVCSSecret(conn.WebhookSecretEncrypted)
	if err != nil {
		writeError(w, 500, "webhook secret unavailable")
		return
	}
	hook, err := vcs.GitLabCreateHook(r.Context(), conn.InstanceUrl, vcs.GitLabCredential{Token: tok, OAuth: conn.AuthKind == "oauth"}, in.Scope, in.TargetID, h.vcsWebhookURL(uuidToString(conn.ID)), secret)
	if err != nil {
		writeError(w, 400, "group webhooks require GitLab Premium; pick a project instead")
		return
	}
	row, err := h.Queries.UpsertVCSWebhookRegistration(r.Context(), db.UpsertVCSWebhookRegistrationParams{ConnectionID: conn.ID, Scope: in.Scope, TargetID: in.TargetID, TargetPath: in.TargetPath, HookID: hook})
	if err != nil {
		writeError(w, 500, "failed to save webhook registration")
		return
	}
	writeJSON(w, 201, row)
}
func (h *Handler) ListVCSWebhookRegistrations(w http.ResponseWriter, r *http.Request) {
	conn, ok := h.loadGitLabConnection(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListVCSWebhookRegistrations(r.Context(), conn.ID)
	if err != nil {
		writeError(w, 500, "failed to list webhook registrations")
		return
	}
	writeJSON(w, 200, map[string]any{"registrations": rows})
}
func (h *Handler) DeleteVCSWebhookRegistration(w http.ResponseWriter, r *http.Request) {
	conn, ok := h.loadGitLabConnection(w, r)
	if !ok {
		return
	}
	target, _ := strconv.ParseInt(chi.URLParam(r, "targetId"), 10, 64)
	row, err := h.Queries.GetVCSWebhookRegistration(r.Context(), db.GetVCSWebhookRegistrationParams{ConnectionID: conn.ID, Scope: chi.URLParam(r, "scope"), TargetID: target})
	if err == nil {
		tok, _ := h.openVCSSecret(conn.AccessTokenEncrypted)
		_ = vcs.GitLabDeleteHook(r.Context(), conn.InstanceUrl, vcs.GitLabCredential{Token: tok, OAuth: conn.AuthKind == "oauth"}, row.Scope, row.TargetID, row.HookID)
	}
	_ = h.Queries.DeleteVCSWebhookRegistration(r.Context(), db.DeleteVCSWebhookRegistrationParams{ConnectionID: conn.ID, Scope: chi.URLParam(r, "scope"), TargetID: target})
	w.WriteHeader(204)
}

func urlQuery(s string) string {
	return url.QueryEscape(s)
}

func parseUUIDOrBadRequestValue(value string) (pgtype.UUID, bool) {
	u, err := util.ParseUUID(value)
	return u, err == nil
}
