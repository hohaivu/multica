package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestFindBareMentionCandidates(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"plain bare mention", "please loop in @tech-lead now", []string{"tech-lead"}},
		{"npm scope rejected", "run @anthropic-ai/sdk instead", nil},
		{"email rejected", "contact user@example.com please", nil},
		{"css at-rule survives extraction but is a real word", "@media (min-width: 1px)", []string{"media"}},
		{"start of string", "@bob can you look", []string{"bob"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findBareMentionCandidates(c.text)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestProseTextSegmentsSkipsLinksAndCode(t *testing.T) {
	body := "See [@tech-lead](mention://agent/abc) and `@tech-lead` and:\n\n```\n@tech-lead\n```\n\nbut also @tech-lead bare."
	joined := strings.Join(proseTextSegments(body), "")
	if strings.Count(joined, "@tech-lead") != 1 {
		t.Fatalf("expected exactly one bare occurrence to survive, got segments: %q", proseTextSegments(body))
	}
}

func TestGuardBareMentionsOnlyFiresInAgentContext(t *testing.T) {
	agentsResp := []map[string]any{
		{"id": "agent-9999", "name": "tech-lead"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agents":
			json.NewEncoder(w).Encode(agentsResp)
		case "/api/squads":
			json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	ctx := context.Background()

	body := "please loop in @tech-lead"

	t.Run("human PAT context is never linted", func(t *testing.T) {
		t.Setenv("MULTICA_AGENT_ID", "")
		t.Setenv("MULTICA_TASK_ID", "")
		if err := guardBareMentions(ctx, client, body, "comment body"); err != nil {
			t.Errorf("expected no error outside agent context, got: %v", err)
		}
	})

	t.Run("agent task context hard-fails on a real bare mention", func(t *testing.T) {
		withAgentContext(t)
		err := guardBareMentions(ctx, client, body, "comment body")
		if err == nil {
			t.Fatal("expected a hard failure inside agent context")
		}
		if !strings.Contains(err.Error(), "@tech-lead") {
			t.Errorf("error should name the dropped mention, got: %v", err)
		}
	})

	t.Run("full link form passes", func(t *testing.T) {
		withAgentContext(t)
		linked := "please loop in [@tech-lead](mention://agent/agent-9999)"
		if err := guardBareMentions(ctx, client, linked, "comment body"); err != nil {
			t.Errorf("expected no error for a properly linked mention, got: %v", err)
		}
	})

	t.Run("npm scope is not a mention", func(t *testing.T) {
		withAgentContext(t)
		if err := guardBareMentions(ctx, client, "install @anthropic-ai/sdk", "comment body"); err != nil {
			t.Errorf("expected no error for an npm scope, got: %v", err)
		}
	})

	t.Run("email is not a mention", func(t *testing.T) {
		withAgentContext(t)
		if err := guardBareMentions(ctx, client, "contact user@example.com", "comment body"); err != nil {
			t.Errorf("expected no error for an email, got: %v", err)
		}
	})

	t.Run("code span quoting the mention is not linted", func(t *testing.T) {
		withAgentContext(t)
		if err := guardBareMentions(ctx, client, "use `@tech-lead` as an example", "comment body"); err != nil {
			t.Errorf("expected no error for a code-quoted mention, got: %v", err)
		}
	})

	t.Run("name with no matching agent/squad is not an error", func(t *testing.T) {
		withAgentContext(t)
		if err := guardBareMentions(ctx, client, "hello @nobody-here", "comment body"); err != nil {
			t.Errorf("expected no error for an unresolvable name, got: %v", err)
		}
	})
}
