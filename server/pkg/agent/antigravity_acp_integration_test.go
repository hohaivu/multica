//go:build agentintegration

package agent

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// resolveAntigravityACPPath mirrors the daemon probe's discovery order
// (agents_probe.go): MULTICA_ANTIGRAVITY_ACP_PATH first, then PATH lookup.
func resolveAntigravityACPPath() (string, error) {
	if p := os.Getenv("MULTICA_ANTIGRAVITY_ACP_PATH"); p != "" {
		return p, nil
	}
	return exec.LookPath("agy_acp_server.par")
}

// TestAntigravityACPRealSmoke drives the real Antigravity ACP server
// (agy_acp_server.par) end-to-end.
//
// This test requires a machine that already completed the one-time
// interactive Google login (`multica runtime auth antigravity-acp`) or has
// GEMINI_API_KEY/GOOGLE_API_KEY set — session/new fails with "Authentication
// required" otherwise, which this test treats as a hard failure rather than
// skipping, since a real smoke run implies credentials are expected to be
// present.
//
// Gated by MULTICA_RUN_REAL_AGENT_SMOKE=1 and requires the binary to be
// discoverable via MULTICA_ANTIGRAVITY_ACP_PATH or PATH.
func TestAntigravityACPRealSmoke(t *testing.T) {
	requireRealAgentSmoke(t)
	if testing.Short() {
		t.Skip("skipping real-binary smoke test in -short mode")
	}

	path, err := resolveAntigravityACPPath()
	if err != nil {
		t.Skip("agy_acp_server.par not found (set MULTICA_ANTIGRAVITY_ACP_PATH or install via `multica runtime install antigravity-acp`); skipping real-binary smoke test")
	}

	backend, err := New("antigravityacp", Config{
		ExecutablePath: path,
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("new antigravityacp backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx,
		"Reply with exactly one word: pong. Do not use any tools.",
		ExecOptions{
			Cwd:     t.TempDir(),
			Timeout: 80 * time.Second,
		},
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	go func() {
		for range session.Messages {
		}
	}()

	var sessionID string
	select {
	case result := <-session.Result:
		if result.Status != "completed" {
			t.Fatalf("real antigravity-acp run did not complete: status=%q error=%q", result.Status, result.Error)
		}
		if !strings.Contains(strings.ToLower(result.Output), "pong") {
			t.Fatalf("expected real antigravity-acp output to contain 'pong', got %q", result.Output)
		}
		if result.SessionID == "" {
			t.Error("expected a non-empty session id from real antigravity-acp")
		}
		sessionID = result.SessionID
		t.Logf("real antigravity-acp smoke OK: session=%s output=%q", result.SessionID, result.Output)

	case <-time.After(90 * time.Second):
		t.Fatal("timeout waiting for real antigravity-acp result")
	}

	// Verify session/resume: pass the same session ID back as
	// ResumeSessionID and confirm a fresh backend can resume it.
	t.Run("session resume", func(t *testing.T) {
		backend2, err := New("antigravityacp", Config{
			ExecutablePath: path,
			Logger:         slog.Default(),
		})
		if err != nil {
			t.Fatalf("new antigravityacp backend (resume): %v", err)
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel2()

		session2, err := backend2.Execute(ctx2,
			"Say: resume-ok. Do not use any tools.",
			ExecOptions{
				Cwd:             t.TempDir(),
				Timeout:         80 * time.Second,
				ResumeSessionID: sessionID,
			},
		)
		if err != nil {
			t.Fatalf("resume execute: %v", err)
		}

		go func() {
			for range session2.Messages {
			}
		}()

		select {
		case r := <-session2.Result:
			if r.Status != "completed" {
				t.Fatalf("resumed run did not complete: status=%q error=%q", r.Status, r.Error)
			}
			if !strings.Contains(strings.ToLower(r.Output), "resume-ok") {
				t.Fatalf("expected resumed output to contain 'resume-ok', got %q", r.Output)
			}
			t.Logf("real antigravity-acp resume OK: session=%s output=%q", r.SessionID, r.Output)
		case <-time.After(90 * time.Second):
			t.Fatal("timeout waiting for resumed result")
		}
	})
}
