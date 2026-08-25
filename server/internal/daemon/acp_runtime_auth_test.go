package daemon

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeAntigravityACPAuthScript impersonates agy_acp_server.par for
// AuthenticateAntigravityACP's initialize+authenticate handshake. It logs the
// authenticate request to $REQUESTS_FILE and, if $ANTIGRAVITY_AUTH_ERROR is
// set, responds with a JSON-RPC error instead of a result.
func fakeAntigravityACPAuthScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{}}'
      ;;
    *'"method":"authenticate"'*)
      printf '%s\n' "$line" >> "$REQUESTS_FILE"
      if [ -n "$ANTIGRAVITY_AUTH_ERROR" ]; then
        printf '%s\n' '{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"login failed"}}'
      else
        printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'
      fi
      ;;
  esac
done
`
}

func writeFakeAntigravityACPAuthServer(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(fakeAntigravityACPAuthScript()), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticateAntigravityACPSendsRequestedMethod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake server is a POSIX shell script")
	}
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "agy_acp_server.par")
	writeFakeAntigravityACPAuthServer(t, fakePath)
	requestsFile := filepath.Join(dir, "requests.jsonl")
	t.Setenv("REQUESTS_FILE", requestsFile)
	t.Setenv("MULTICA_ANTIGRAVITY_ACP_PATH", fakePath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var progress bytes.Buffer
	if err := AuthenticateAntigravityACP(ctx, &progress, AntigravityACPMethodOAuthBusiness); err != nil {
		t.Fatalf("AuthenticateAntigravityACP() error = %v", err)
	}

	requested, err := os.ReadFile(requestsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(requested), `"methodId":"oauth-business"`) {
		t.Fatalf("expected requested methodId in %q", requested)
	}
	if !strings.Contains(progress.String(), "Authenticated.") {
		t.Fatalf("expected success message in progress output, got %q", progress.String())
	}
}

func TestAuthenticateAntigravityACPPropagatesAuthenticateError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake server is a POSIX shell script")
	}
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "agy_acp_server.par")
	writeFakeAntigravityACPAuthServer(t, fakePath)
	t.Setenv("REQUESTS_FILE", filepath.Join(dir, "requests.jsonl"))
	t.Setenv("MULTICA_ANTIGRAVITY_ACP_PATH", fakePath)
	t.Setenv("ANTIGRAVITY_AUTH_ERROR", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := AuthenticateAntigravityACP(ctx, io.Discard, AntigravityACPMethodOAuthPersonal)
	if err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("expected login failed error, got %v", err)
	}
}

func TestAuthenticateAntigravityACPNotInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MULTICA_ANTIGRAVITY_ACP_PATH", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := AuthenticateAntigravityACP(ctx, io.Discard, AntigravityACPMethodOAuthPersonal)
	if err == nil || !strings.Contains(err.Error(), "runtime install antigravity-acp") {
		t.Fatalf("expected not-installed error, got %v", err)
	}
}
