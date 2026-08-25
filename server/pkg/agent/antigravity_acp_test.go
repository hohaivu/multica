package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFileOrFatal(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestNewReturnsAntigravityACPBackend(t *testing.T) {
	t.Parallel()
	b, err := New("antigravityacp", Config{ExecutablePath: "/nonexistent/agy_acp_server.par"})
	if err != nil {
		t.Fatalf("New(antigravityacp) error: %v", err)
	}
	if _, ok := b.(*antigravityACPBackend); !ok {
		t.Fatalf("expected *antigravityACPBackend, got %T", b)
	}
}

// fakeAntigravityACPScript impersonates agy_acp_server.par for unit tests.
// Wire format mirrors the other Multica ACP fakes (grok/qwenpaw): method
// "session/update" with update.sessionUpdate discriminators, session/new /
// session/resume returning sessionId, session/prompt returning
// stopReason=end_turn.
//
//   - ANTIGRAVITY_AUTH_METHODS=api advertises gemini-api-key so the backend's
//     conditional authenticate step actually fires when GEMINI_API_KEY is set.
//   - ANTIGRAVITY_RESUME_NOT_FOUND makes session/resume fail with the
//     confirmed-live error shape.
//   - ANTIGRAVITY_MALFORMED_UPDATE emits one unparsable session/update line
//     before the real notifications, to check the reader tolerates it.
func fakeAntigravityACPScript() string {
	return `#!/bin/sh
authenticated=
while IFS= read -r line; do
  if [ -n "$ANTIGRAVITY_REQUESTS_FILE" ]; then
    printf '%s\n' "$line" >> "$ANTIGRAVITY_REQUESTS_FILE"
  fi
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      case "$ANTIGRAVITY_AUTH_METHODS" in
        api)
          printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"gemini-api-key","name":"Gemini API key"}],"agentCapabilities":{"loadSession":true,"mcpCapabilities":{"http":true,"sse":true},"sessionCapabilities":{"list":{},"resume":{}}}}}\n' "$id"
          ;;
        *)
          printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"oauth-personal","name":"Log in with Google"}],"agentCapabilities":{"loadSession":true,"mcpCapabilities":{"http":true,"sse":true},"sessionCapabilities":{"list":{},"resume":{}}}}}\n' "$id"
          ;;
      esac
      ;;
    *'"method":"authenticate"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      authenticated=1
      ;;
    *'"method":"session/resume"'*)
      if [ -n "$ANTIGRAVITY_RESUME_NOT_FOUND" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"session not found"}}\n' "$id"
        exit 0
      fi
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"ses_resumed"}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"ses_new"}}\n' "$id"
      ;;
    *'"method":"session/prompt"'*)
      if [ -n "$ANTIGRAVITY_MALFORMED_UPDATE" ]; then
        printf '{"jsonrpc":"2.0","method":"session/update","params":{not valid json\n'
      fi
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_new","update":{"sessionUpdate":"tool_call","toolCallId":"tc-1","name":"Shell","status":"pending","parameters":{"command":"echo hi"}}}}\n'
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_new","update":{"sessionUpdate":"tool_call_update","toolCallId":"tc-1","status":"completed","name":"Shell","output":"hi\\n"}}}\n'
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_new","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"pong"}}}}\n'
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
      exit 0
      ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}\n' "$id"
      ;;
  esac
done
`
}

func TestAntigravityACPSessionNewStreamsAndCompletes(t *testing.T) {
	t.Parallel()
	fakePath := filepath.Join(t.TempDir(), "agy_acp_server.par")
	writeTestExecutable(t, fakePath, []byte(fakeAntigravityACPScript()))

	backend, err := New("antigravityacp", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new antigravityacp backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "say pong", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var messages []Message
	done := make(chan struct{})
	go func() {
		defer close(done)
		for m := range session.Messages {
			messages = append(messages, m)
		}
	}()
	result := <-session.Result
	<-done

	if result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}
	if result.SessionID != "ses_new" {
		t.Fatalf("session id = %q, want ses_new", result.SessionID)
	}
	if !strings.Contains(result.Output, "pong") {
		t.Fatalf("output = %q, want it to contain 'pong'", result.Output)
	}
	var sawToolUse bool
	for _, m := range messages {
		if m.Type == MessageToolUse {
			sawToolUse = true
		}
	}
	if !sawToolUse {
		t.Errorf("expected a streamed MessageToolUse from the tool_call update; messages=%+v", messages)
	}
}

func TestAntigravityACPAuthenticatesWhenAPIKeyPresent(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	requestsFile := filepath.Join(tempDir, "requests.jsonl")
	fakePath := filepath.Join(tempDir, "agy_acp_server.par")
	writeTestExecutable(t, fakePath, []byte(fakeAntigravityACPScript()))

	backend, err := New("antigravityacp", Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env: map[string]string{
			"GEMINI_API_KEY":            "test-key",
			"ANTIGRAVITY_AUTH_METHODS":  "api",
			"ANTIGRAVITY_REQUESTS_FILE": requestsFile,
		},
	})
	if err != nil {
		t.Fatalf("new antigravityacp backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "hi", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}

	requests := readFileOrFatal(t, requestsFile)
	if !strings.Contains(requests, `"method":"authenticate"`) {
		t.Fatalf("expected an authenticate request when GEMINI_API_KEY is set and gemini-api-key is offered; requests=%s", requests)
	}
	if !strings.Contains(requests, `"methodId":"gemini-api-key"`) {
		t.Fatalf("expected authenticate to select gemini-api-key; requests=%s", requests)
	}
}

func TestAntigravityACPSkipsAuthenticateWithoutCredentials(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	requestsFile := filepath.Join(tempDir, "requests.jsonl")
	fakePath := filepath.Join(tempDir, "agy_acp_server.par")
	writeTestExecutable(t, fakePath, []byte(fakeAntigravityACPScript()))

	backend, err := New("antigravityacp", Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env: map[string]string{
			"ANTIGRAVITY_REQUESTS_FILE": requestsFile,
		},
	})
	if err != nil {
		t.Fatalf("new antigravityacp backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "hi", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}

	requests := readFileOrFatal(t, requestsFile)
	if strings.Contains(requests, `"method":"authenticate"`) {
		t.Fatalf("expected no authenticate call without a credential or an oauth-only server offer; requests=%s", requests)
	}
}

func TestAntigravityACPResumeNotFoundSetsResumeRejected(t *testing.T) {
	t.Parallel()
	fakePath := filepath.Join(t.TempDir(), "agy_acp_server.par")
	writeTestExecutable(t, fakePath, []byte(fakeAntigravityACPScript()))

	backend, err := New("antigravityacp", Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env: map[string]string{
			"ANTIGRAVITY_RESUME_NOT_FOUND": "1",
		},
	})
	if err != nil {
		t.Fatalf("new antigravityacp backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "hi", ExecOptions{
		Timeout:         5 * time.Second,
		ResumeSessionID: "ses_old",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "failed" {
		t.Fatalf("expected failed, got status=%q", result.Status)
	}
	if !result.ResumeRejected {
		t.Fatal("expected ResumeRejected=true when session/resume reports session not found")
	}
}

func TestAntigravityACPMalformedSessionUpdateDoesNotPanic(t *testing.T) {
	t.Parallel()
	fakePath := filepath.Join(t.TempDir(), "agy_acp_server.par")
	writeTestExecutable(t, fakePath, []byte(fakeAntigravityACPScript()))

	backend, err := New("antigravityacp", Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env: map[string]string{
			"ANTIGRAVITY_MALFORMED_UPDATE": "1",
		},
	})
	if err != nil {
		t.Fatalf("new antigravityacp backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "hi", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("expected the run to complete despite the malformed update line, got status=%q error=%q", result.Status, result.Error)
	}
}

func TestAntigravityACPBlockedUidArg(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	argsFile := filepath.Join(tempDir, "argv.txt")
	fakePath := filepath.Join(tempDir, "agy_acp_server.par")
	writeTestExecutable(t, fakePath, []byte(`#!/bin/sh
: >> "`+argsFile+`"
for arg in "$@"; do printf '%s\n' "$arg" >> "`+argsFile+`"; done
`))

	backend, err := New("antigravityacp", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new antigravityacp backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "hi", ExecOptions{
		Timeout:   5 * time.Second,
		ExtraArgs: []string{"--uid=attacker"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}
	<-session.Result

	args := readFileOrFatal(t, argsFile)
	if strings.Contains(args, "--uid=attacker") {
		t.Fatalf("expected --uid to be blocked from custom args; argv=%s", args)
	}
}
