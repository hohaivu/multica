package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/vcs"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
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
	if _, err := h.Queries.GetWorkspace(r.Context(), wsUUID); err != nil {
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
	existing, lookupErr := h.Queries.GetVCSConnectionByWorkspaceAndInstance(r.Context(), db.GetVCSConnectionByWorkspaceAndInstanceParams{WorkspaceID: wsUUID, InstanceUrl: h.cfg.GitLabInstanceURL})
	switch {
	case lookupErr == nil:
		secret, err = h.openVCSSecret(existing.WebhookSecretEncrypted)
	case errors.Is(lookupErr, pgx.ErrNoRows):
		secret, err = newVCSWebhookSecret()
	default:
		fail("lookup")
		return
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
	h.publish(protocol.EventVCSConnectionCreated, s.WorkspaceID, "system", "", map[string]any{"id": uuidToString(conn.ID)})
	http.Redirect(w, r, strings.TrimRight(h.cfg.AppURL, "/")+"/settings?tab=integrations&gitlab_connected=1", http.StatusFound)
}

func (h *Handler) ListGitLabTargets(w http.ResponseWriter, r *http.Request) {
	conn, ok := h.loadGitLabConnection(w, r)
	if !ok {
		return
	}
	cred, err := h.gitlabAccessToken(r.Context(), conn)
	if err != nil {
		writeGitLabCredentialError(w, err)
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
	projects, next, err := vcs.GitLabListProjects(r.Context(), conn.InstanceUrl, cred, page, per)
	if err != nil {
		writeGitLabUpstreamError(w, err, "could not list GitLab projects")
		return
	}
	groups, groupsNext, err := vcs.GitLabListGroups(r.Context(), conn.InstanceUrl, cred, page, per)
	if err != nil {
		writeGitLabUpstreamError(w, err, "could not list GitLab groups")
		return
	}
	// Both lists use the same page, so continue until the larger cursor is exhausted.
	if groupsNext > next {
		next = groupsNext
	}
	writeJSON(w, 200, map[string]any{"projects": projects, "groups": groups, "next_page": next})
}

func (h *Handler) loadGitLabConnection(w http.ResponseWriter, r *http.Request) (db.VcsConnection, bool) {
	if !h.isVCSAvailable() {
		writeError(w, http.StatusNotFound, "vcs integration is not available on this deployment")
		return db.VcsConnection{}, false
	}
	if !h.isVCSConfigured() {
		writeError(w, http.StatusServiceUnavailable, "vcs integration not configured (MULTICA_VCS_SECRET_KEY unset)")
		return db.VcsConnection{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return db.VcsConnection{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "connectionId"), "connection id")
	if !ok {
		return db.VcsConnection{}, false
	}
	conn, err := h.Queries.GetVCSConnectionByID(r.Context(), id)
	if err != nil || conn.Provider != "gitlab" || uuidToString(conn.WorkspaceID) != uuidToString(wsUUID) {
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
	cred, err := h.gitlabAccessToken(r.Context(), conn)
	if err != nil {
		writeGitLabCredentialError(w, err)
		return
	}
	secret, err := h.openVCSSecret(conn.WebhookSecretEncrypted)
	if err != nil {
		writeError(w, 500, "webhook secret unavailable")
		return
	}
	hook, err := vcs.GitLabCreateHook(r.Context(), conn.InstanceUrl, cred, in.Scope, in.TargetID, h.vcsWebhookURL(uuidToString(conn.ID)), secret)
	if err != nil {
		writeGitLabHookError(w, err, in.Scope)
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
	scope := chi.URLParam(r, "scope")
	if scope != "project" && scope != "group" {
		writeError(w, http.StatusBadRequest, "invalid scope")
		return
	}
	target, err := strconv.ParseInt(chi.URLParam(r, "targetId"), 10, 64)
	if err != nil || target < 1 {
		writeError(w, http.StatusBadRequest, "invalid target id")
		return
	}
	row, err := h.Queries.GetVCSWebhookRegistration(r.Context(), db.GetVCSWebhookRegistrationParams{ConnectionID: conn.ID, Scope: scope, TargetID: target})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "webhook registration not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to find webhook registration")
		return
	}
	cred, err := h.gitlabAccessToken(r.Context(), conn)
	if err != nil {
		writeGitLabCredentialError(w, err)
		return
	}
	if err := vcs.GitLabDeleteHook(r.Context(), conn.InstanceUrl, cred, row.Scope, row.TargetID, row.HookID); err != nil {
		slog.Warn("failed to delete GitLab webhook; deleting local registration", "connection_id", uuidToString(conn.ID), "hook_id", row.HookID, "error", err)
	}
	if err := h.Queries.DeleteVCSWebhookRegistration(r.Context(), db.DeleteVCSWebhookRegistrationParams{ConnectionID: conn.ID, Scope: scope, TargetID: target}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete webhook registration")
		return
	}
	w.WriteHeader(204)
}

var errGitLabReconnect = errors.New("reconnect GitLab")

func (h *Handler) gitlabAccessToken(ctx context.Context, conn db.VcsConnection) (vcs.GitLabCredential, error) {
	access, err := h.openVCSSecret(conn.AccessTokenEncrypted)
	if err != nil {
		return vcs.GitLabCredential{}, errGitLabReconnect
	}
	if conn.AuthKind != "oauth" {
		return vcs.GitLabCredential{Token: access}, nil
	}
	if conn.CredentialStatus == "expired" {
		return vcs.GitLabCredential{}, errGitLabReconnect
	}
	if conn.AccessTokenExpiresAt.Valid && time.Until(conn.AccessTokenExpiresAt.Time) > 5*time.Minute {
		return vcs.GitLabCredential{Token: access, OAuth: true}, nil
	}
	v, err, _ := h.gitlabRefresh.Do(uuidToString(conn.ID), func() (any, error) {
		fresh, e := h.Queries.GetVCSConnectionByID(ctx, conn.ID)
		if e != nil {
			return nil, e
		}
		if fresh.CredentialStatus == "expired" {
			return nil, errGitLabReconnect
		}
		if fresh.AccessTokenExpiresAt.Valid && time.Until(fresh.AccessTokenExpiresAt.Time) > 5*time.Minute {
			access, e := h.openVCSSecret(fresh.AccessTokenEncrypted)
			if e != nil {
				return nil, e
			}
			return vcs.GitLabCredential{Token: access, OAuth: true}, nil
		}
		refresh, e := h.openVCSSecret(fresh.RefreshTokenEncrypted)
		if e != nil {
			return nil, errGitLabReconnect
		}
		tok, e := vcs.GitLabRefreshToken(ctx, fresh.InstanceUrl, h.cfg.GitLabOAuthClientID, h.cfg.GitLabOAuthClientSecret, refresh, strings.TrimRight(h.cfg.PublicURL, "/")+"/api/gitlab/oauth/callback")
		if errors.Is(e, vcs.ErrRefreshRejected) {
			_ = h.Queries.MarkVCSConnectionCredentialExpired(ctx, conn.ID)
			return nil, errGitLabReconnect
		}
		if e != nil {
			return nil, e
		}
		a, e := h.sealVCSSecret(tok.AccessToken)
		if e != nil {
			return nil, e
		}
		r, e := h.sealVCSSecret(tok.RefreshToken)
		if e != nil {
			return nil, e
		}
		_, e = h.Queries.UpdateVCSConnectionTokens(ctx, db.UpdateVCSConnectionTokensParams{ID: conn.ID, AccessTokenEncrypted: a, RefreshTokenEncrypted: r, AccessTokenExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second), Valid: tok.ExpiresIn > 0}})
		if e != nil {
			return nil, e
		}
		return vcs.GitLabCredential{Token: tok.AccessToken, OAuth: true}, nil
	})
	if err != nil {
		return vcs.GitLabCredential{}, err
	}
	return v.(vcs.GitLabCredential), nil
}

func writeGitLabCredentialError(w http.ResponseWriter, err error) {
	if errors.Is(err, errGitLabReconnect) {
		writeError(w, 409, "reconnect GitLab")
		return
	}
	writeError(w, 502, "GitLab credential unavailable")
}
func writeGitLabUpstreamError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, vcs.ErrUnauthorized) {
		writeError(w, 409, "reconnect GitLab")
		return
	}
	writeError(w, 502, fallback)
}
func writeGitLabHookError(w http.ResponseWriter, err error, scope string) {
	var se vcs.StatusError
	if errors.As(err, &se) {
		if scope == "group" && (se.Status == 403 || se.Status == 404) {
			writeError(w, 400, "group webhooks require GitLab Premium; pick a project instead")
			return
		}
		if se.Status != 401 && se.Status != 403 {
			writeError(w, 502, "GitLab upstream error")
			return
		}
		writeError(w, 409, "reconnect GitLab")
		return
	}
	writeError(w, 502, "GitLab upstream error")
}

func urlQuery(s string) string {
	return url.QueryEscape(s)
}

func parseUUIDOrBadRequestValue(value string) (pgtype.UUID, bool) {
	u, err := util.ParseUUID(value)
	return u, err == nil
}
