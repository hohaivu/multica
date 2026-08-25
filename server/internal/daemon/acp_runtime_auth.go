package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// syncWriter serializes writes from multiple goroutines onto one io.Writer.
// cmd.Stderr is copied by a goroutine internal to os/exec while our own
// status lines write concurrently from the caller's goroutine; a plain
// io.Writer such as bytes.Buffer is not safe for that without this.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// AntigravityACPMethodOAuthPersonal and AntigravityACPMethodOAuthBusiness are
// the interactive `authenticate` methodIds antigravity-acp advertises (see
// server/pkg/agent/antigravity_acp.go). Both open a real Google OAuth browser
// flow with no headless fallback (confirmed live, Phase 0 of the "Migrate
// Antigravity to ACP" plan) — which is why acquisition never calls
// authenticate itself. AuthenticateAntigravityACP is the one place daemon
// code is allowed to: it only runs from the explicit
// `multica runtime auth antigravity-acp` command, invoked by a human at a
// terminal who can complete the browser flow.
const (
	AntigravityACPMethodOAuthPersonal = "oauth-personal"
	AntigravityACPMethodOAuthBusiness = "oauth-business"
)

// AuthenticateAntigravityACP drives one initialize+authenticate handshake
// against the installed (or MULTICA_ANTIGRAVITY_ACP_PATH-overridden) ACP
// server and blocks until the login completes or ctx is done. progress
// receives both our own status lines and the server's stderr, so the "Log in
// with Google to continue..." prompt the real binary prints reaches the
// user live.
func AuthenticateAntigravityACP(ctx context.Context, progress io.Writer, methodID string) error {
	if progress == nil {
		progress = io.Discard
	}
	progress = &syncWriter{w: progress}
	binPath := strings.TrimSpace(os.Getenv("MULTICA_ANTIGRAVITY_ACP_PATH"))
	if binPath == "" {
		binPath = managedACPServerPath()
	}
	if binPath == "" {
		return fmt.Errorf("antigravity-acp is not installed; run `multica runtime install antigravity-acp` first")
	}

	cmd := exec.CommandContext(ctx, binPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("antigravity-acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("antigravity-acp: stdout pipe: %w", err)
	}
	cmd.Stderr = progress
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("antigravity-acp: start: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)

	send := func(id int, method string, params any) error {
		req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
		line, err := json.Marshal(req)
		if err != nil {
			return err
		}
		_, err = stdin.Write(append(line, '\n'))
		return err
	}

	// await waits for the JSON-RPC response carrying the given id, skipping
	// notifications (session/update, ...) and responses to other ids.
	await := func(id int) (json.RawMessage, error) {
		for scanner.Scan() {
			var msg struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.ID != id {
				continue
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("%s (code %d)", msg.Error.Message, msg.Error.Code)
			}
			return msg.Result, nil
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("antigravity-acp: read response: %w", err)
		}
		return nil, fmt.Errorf("antigravity-acp: exited before responding")
	}

	fmt.Fprintln(progress, "Starting antigravity-acp...")
	if err := send(1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}); err != nil {
		return fmt.Errorf("antigravity-acp: send initialize: %w", err)
	}
	if _, err := await(1); err != nil {
		return fmt.Errorf("antigravity-acp: initialize: %w", err)
	}

	fmt.Fprintf(progress, "Requesting %s login — complete it in the browser window that opens...\n", methodID)
	if err := send(2, "authenticate", map[string]any{"methodId": methodID}); err != nil {
		return fmt.Errorf("antigravity-acp: send authenticate: %w", err)
	}
	if _, err := await(2); err != nil {
		return fmt.Errorf("antigravity-acp: authenticate: %w", err)
	}

	fmt.Fprintf(progress, "Authenticated. Credentials are stored under %s.\n", filepath.Join(antigravityGeminiHome(), "antigravity-acp"))
	return nil
}
