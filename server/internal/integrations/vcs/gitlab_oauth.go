package vcs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type GitLabCredential struct {
	Token string
	OAuth bool
}

func ValidateGitLabOAuthToken(ctx context.Context, instanceURL, token string) (Account, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, NormalizeInstanceURL(instanceURL)+"/api/v4/user", nil)
	if err != nil {
		return Account{}, err
	}
	setGitLabAuth(req, GitLabCredential{Token: token, OAuth: true})
	resp, err := httpClient.Do(req)
	if err != nil {
		return Account{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Account{}, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Account{}, fmt.Errorf("gitlab: user status %d", resp.StatusCode)
	}
	var u struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return Account{}, err
	}
	if u.Username == "" {
		return Account{}, errors.New("gitlab: user response missing username")
	}
	return Account{Login: u.Username}, nil
}

type GitLabToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type GitLabTarget struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

var ErrRefreshRejected = errors.New("gitlab: refresh token rejected")

func GitLabAuthorizeURL(instanceURL, clientID, redirectURI, state, verifier string) string {
	u := NormalizeInstanceURL(instanceURL) + "/oauth/authorize"
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"scope":                 {"api"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(hash[:])},
		"code_challenge_method": {"S256"},
	}
	return u + "?" + q.Encode()
}

func GitLabExchangeCode(ctx context.Context, instanceURL, clientID, clientSecret, code, redirectURI, verifier string) (GitLabToken, error) {
	return gitLabTokenRequest(ctx, instanceURL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	})
}

func GitLabRefreshToken(ctx context.Context, instanceURL, clientID, clientSecret, refreshToken, redirectURI string) (GitLabToken, error) {
	return gitLabTokenRequest(ctx, instanceURL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
	})
}

func gitLabTokenRequest(ctx context.Context, instanceURL string, form url.Values) (GitLabToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, NormalizeInstanceURL(instanceURL)+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return GitLabToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return GitLabToken{}, fmt.Errorf("gitlab oauth: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "invalid_grant" {
			return GitLabToken{}, ErrRefreshRejected
		}
		return GitLabToken{}, fmt.Errorf("gitlab oauth: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return GitLabToken{}, fmt.Errorf("gitlab oauth: status %d", resp.StatusCode)
	}
	var tok GitLabToken
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return GitLabToken{}, err
	}
	if tok.AccessToken == "" {
		return GitLabToken{}, errors.New("gitlab oauth: response missing access token")
	}
	return tok, nil
}

func setGitLabAuth(req *http.Request, cred GitLabCredential) {
	if cred.OAuth {
		req.Header.Set("Authorization", "Bearer "+cred.Token)
		return
	}
	req.Header.Set("PRIVATE-TOKEN", cred.Token)
}

func GitLabListProjects(ctx context.Context, instanceURL string, cred GitLabCredential, page, perPage int) ([]GitLabTarget, int, error) {
	return gitLabListTargets(ctx, instanceURL, cred, "/api/v4/projects", url.Values{"membership": {"true"}, "min_access_level": {"40"}, "order_by": {"last_activity_at"}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}})
}

func GitLabListGroups(ctx context.Context, instanceURL string, cred GitLabCredential, page, perPage int) ([]GitLabTarget, int, error) {
	return gitLabListTargets(ctx, instanceURL, cred, "/api/v4/groups", url.Values{"min_access_level": {"40"}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}})
}

func gitLabListTargets(ctx context.Context, instanceURL string, cred GitLabCredential, path string, q url.Values) ([]GitLabTarget, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, NormalizeInstanceURL(instanceURL)+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	setGitLabAuth(req, cred)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("gitlab: %s status %d", path, resp.StatusCode)
	}
	var targets []GitLabTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, 0, err
	}
	next, _ := strconv.Atoi(resp.Header.Get("X-Next-Page"))
	return targets, next, nil
}

func GitLabCreateHook(ctx context.Context, instanceURL string, cred GitLabCredential, scope string, targetID int64, hookURL, secret string) (int64, error) {
	path, err := gitLabHookPath(scope, targetID, "")
	if err != nil {
		return 0, err
	}
	body, _ := json.Marshal(map[string]any{"url": hookURL, "token": secret, "name": "Multica", "merge_requests_events": true, "pipeline_events": true, "push_events": false, "enable_ssl_verification": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, NormalizeInstanceURL(instanceURL)+path, strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	setGitLabAuth(req, cred)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("gitlab: create hook status %d", resp.StatusCode)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

func GitLabDeleteHook(ctx context.Context, instanceURL string, cred GitLabCredential, scope string, targetID, hookID int64) error {
	path, err := gitLabHookPath(scope, targetID, strconv.FormatInt(hookID, 10))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, NormalizeInstanceURL(instanceURL)+path, nil)
	if err != nil {
		return err
	}
	setGitLabAuth(req, cred)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab: delete hook status %d", resp.StatusCode)
	}
	return nil
}

func gitLabHookPath(scope string, targetID int64, hookID string) (string, error) {
	if scope != "project" && scope != "group" {
		return "", errors.New("gitlab: invalid hook scope")
	}
	resource := map[string]string{"project": "projects", "group": "groups"}[scope]
	path := fmt.Sprintf("/api/v4/%s/%d/hooks", resource, targetID)
	if hookID != "" {
		path += "/" + hookID
	}
	return path, nil
}
