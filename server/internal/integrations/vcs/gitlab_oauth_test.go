package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGitLabAuthorizeURLUsesS256(t *testing.T) {
	u, err := url.Parse(GitLabAuthorizeURL("https://gitlab.example", "client", "http://localhost/cb", "state", "verifier"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("scope") != "api" || q.Get("state") != "state" {
		t.Fatalf("unexpected query: %s", u.RawQuery)
	}
	if q.Get("code_challenge") == "" {
		t.Fatal("missing code challenge")
	}
}

func TestGitLabOAuthRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(GitLabToken{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 7200})
			return
		}
		if r.URL.Path == "/api/v4/projects" {
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("missing bearer auth")
			}
			w.Header().Set("X-Next-Page", "2")
			_ = json.NewEncoder(w).Encode([]GitLabTarget{{ID: 1, Name: "p"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	tok, err := GitLabExchangeCode(context.Background(), server.URL, "id", "secret", "code", "http://cb", "verifier")
	if err != nil || tok.AccessToken != "access" {
		t.Fatalf("exchange: %#v %v", tok, err)
	}
	targets, next, err := GitLabListProjects(context.Background(), server.URL, GitLabCredential{Token: tok.AccessToken, OAuth: true}, 1, 25)
	if err != nil || len(targets) != 1 || next != 2 {
		t.Fatalf("projects: %#v %d %v", targets, next, err)
	}
}

func TestGitLabRefreshRejectsInvalidGrant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	_, err := GitLabRefreshToken(context.Background(), server.URL, "id", "secret", "refresh", "http://cb")
	if err != ErrRefreshRejected {
		t.Fatalf("err = %v, want ErrRefreshRejected", err)
	}
}

func TestGitLabCreateHookPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "pat" {
			t.Errorf("missing private token")
		}
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		body := string(b[:n])
		for _, want := range []string{"merge_requests_events", "pipeline_events", "hook-secret"} {
			if !strings.Contains(body, want) {
				t.Errorf("payload missing %q: %s", want, body)
			}
		}
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer server.Close()
	id, err := GitLabCreateHook(context.Background(), server.URL, GitLabCredential{Token: "pat"}, "project", 7, "http://hook", "hook-secret")
	if err != nil || id != 42 {
		t.Fatalf("create hook: %d %v", id, err)
	}
}
