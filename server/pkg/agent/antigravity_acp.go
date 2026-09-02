package agent

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// antigravityACPBlockedArgs are flags hardcoded by the daemon that must not
// be overridden by user-configured custom_args. `--uid=` is the fixed,
// literal flag the registry manifest (agentclientprotocol/registry
// antigravity-acp/agent.json) requires on Linux builds — see
// antigravityACPLaunchArgs.
var antigravityACPBlockedArgs = map[string]blockedArgMode{
	"--uid": blockedWithValue,
}

// antigravityAuthMethodGeminiAPIKey and antigravityAuthMethodAgentPlatform
// are the two ACP authMethod ids that can be satisfied non-interactively.
// oauth-personal / oauth-business are deliberately never selected by this
// backend — see selectAntigravityAuthMethod.
const (
	antigravityAuthMethodGeminiAPIKey  = "gemini-api-key"
	antigravityAuthMethodAgentPlatform = "agent-platform"
)

// antigravityACPLaunchArgs returns the fixed launch arguments the registry
// manifest requires for this platform. Confirmed against
// antigravity-acp/agent.json (2026-08-25): Linux builds carry
// `"args": ["--uid="]` verbatim — a literal empty-valued flag, not a
// template — while darwin and windows builds carry no args at all.
func antigravityACPLaunchArgs() []string {
	if runtime.GOOS == "linux" {
		return []string{"--uid="}
	}
	return nil
}

// selectAntigravityAuthMethod picks a non-interactive ACP authMethod to
// proactively authenticate with, or "" when none is safely usable.
//
// The Antigravity ACP server (agy_acp_server_20260818_01_RC01) was confirmed
// live to answer authenticate({"methodId":"oauth-personal"}) by logging
// "Credentials missing or invalid. Launching browser login flow..." and
// opening a real interactive Google OAuth consent screen — there is no
// device-code or headless fallback. Calling that from a headless daemon
// would hang indefinitely or drive a browser flow behind the operator's
// back, so oauth-personal and oauth-business are never selected here. Only
// the two API-key-style methods are attempted, and only when the matching
// credential is actually present in the child environment; otherwise
// authenticate is skipped entirely and session/new relies on whatever the
// runtime already has cached in ~/.gemini/antigravity-acp/settings.json.
func selectAntigravityAuthMethod(methods []string, haveGeminiAPIKey, haveAgentPlatformCreds bool) string {
	offered := make(map[string]bool, len(methods))
	for _, m := range methods {
		if m = strings.TrimSpace(m); m != "" {
			offered[m] = true
		}
	}
	if haveGeminiAPIKey && offered[antigravityAuthMethodGeminiAPIKey] {
		return antigravityAuthMethodGeminiAPIKey
	}
	if haveAgentPlatformCreds && offered[antigravityAuthMethodAgentPlatform] {
		return antigravityAuthMethodAgentPlatform
	}
	return ""
}

// antigravitySessionFailureMessage turns a session/new or session/prompt
// failure into something an operator can act on when the underlying cause
// is missing credentials, instead of a bare JSON-RPC error.
func antigravitySessionFailureMessage(base string, err error) string {
	if !isACPAuthRequired(err) {
		return base
	}
	return base + " — antigravity-acp is installed but not authenticated." +
		" Run the interactive Google login once (see the Antigravity provider docs)," +
		" or set GEMINI_API_KEY / GOOGLE_API_KEY for non-interactive authentication."
}

// antigravityACPBackend implements Backend by spawning the Antigravity ACP
// server (agy_acp_server.par / .exe) and communicating via the standard ACP
// (Agent Client Protocol) JSON-RPC 2.0 transport over stdin/stdout.
//
// This is a distinct binary from the `agy` CLI that server/pkg/agent/antigravity.go
// drives — a ~300MB Google download with its own release channel, not a
// flag on the CLI. It reuses the shared hermesClient ACP transport, the same
// way qwenpaw/grok/zeroclaw/hermes do.
//
// Verified live against agy_acp_server_20260818_01_RC01 (2026-08-25):
//
//   - initialize advertises agentCapabilities.mcpCapabilities: {"http":true,
//     "sse":true} — no "stdio" key. Any workspace MCP server configured as
//     stdio is silently dropped by filterACPMcpServersByCapability for this
//     provider; that is the shared filter's documented behavior for a
//     runtime that never declared that transport, not a bug here.
//   - initialize advertises sessionCapabilities: {"list":{},"resume":{}} —
//     resume goes through session/resume, not session/load.
//   - session/new without valid credentials fails with
//     {"code":-32000,"message":"Authentication required"}; see
//     isACPAuthRequired and selectAntigravityAuthMethod for how this backend
//     handles authentication without ever risking an interactive browser
//     flow from a headless daemon.
//   - session/set_model support and session/cancel behavior were not
//     verified (both require an authenticated session). Until confirmed,
//     model selection is unsupported (see ModelSelectionSupported) and usage
//     is always attributed to "unknown", mirroring the qwenpaw precedent.
type antigravityACPBackend struct {
	cfg Config
}

func (b *antigravityACPBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "agy_acp_server.par"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("antigravity-acp executable not found at %q: %w", execPath, err)
	}

	mcpServers, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("antigravity-acp: invalid mcp_config: %w", err)
	}

	timeout := opts.Timeout
	runCtx, cancel := runContext(ctx, timeout)

	antigravityArgs := append([]string{}, antigravityACPLaunchArgs()...)
	antigravityArgs = append(antigravityArgs, filterCustomArgs(opts.ExtraArgs, antigravityACPBlockedArgs, b.cfg.Logger)...)
	antigravityArgs = append(antigravityArgs, filterCustomArgs(opts.CustomArgs, antigravityACPBlockedArgs, b.cfg.Logger)...)

	cmd := b.cfg.commandAt(execPath).exec(runCtx, antigravityArgs...)
	hideAgentWindow(cmd)
	b.cfg.logAgentCommand(cmd, newAgentCommandLogArgs(antigravityArgs))
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	childEnv := buildEnv(b.cfg.Env)
	cmd.Env = childEnv

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("antigravity-acp stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("antigravity-acp stdin pipe: %w", err)
	}

	providerErr := newACPProviderErrorSniffer("antigravity-acp")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("antigravity-acp stderr pipe: %w", err)
	}

	if err := startOwnedProcessTree(cmd, b.cfg.Logger); err != nil {
		cancel()
		return nil, fmt.Errorf("start antigravity-acp: %w", err)
	}

	stderrSink := io.MultiWriter(newLogWriter(b.cfg.Logger, "[antigravity-acp:stderr] "), providerErr)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderrSink, stderr)
	}()

	b.cfg.Logger.Info("antigravity-acp started", "pid", cmd.Process.Pid, "cwd", opts.Cwd)

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)

	var outputMu sync.Mutex
	var output strings.Builder

	promptDone := make(chan hermesPromptResult, 1)

	c := &hermesClient{
		cfg:          b.cfg,
		stdin:        stdin,
		pending:      make(map[int]*pendingRPC),
		pendingTools: make(map[string]*pendingToolCall),
		onMessage: func(msg Message) {
			if msg.Type == MessageText {
				outputMu.Lock()
				output.WriteString(msg.Content)
				outputMu.Unlock()
			}
			trySend(msgCh, msg)
		},
		onPromptDone: func(result hermesPromptResult) {
			select {
			case promptDone <- result:
			default:
			}
		},
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := newAgentStreamScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			c.handleLine(line)
		}
		c.closeAllPending(fmt.Errorf("antigravity-acp process exited"))
	}()

	go func() {
		defer cancel()
		defer close(msgCh)
		defer close(resCh)
		defer func() {
			stdin.Close()
			_ = cmd.Wait()
			releaseProcessGroup(cmd)
		}()

		startTime := time.Now()
		finalStatus := "completed"
		var finalError string
		var sessionID string
		var resumeRejected bool

		// 1. Initialize handshake.
		initResult, err := c.request(runCtx, "initialize", map[string]any{
			"protocolVersion": 1,
			"clientInfo": map[string]any{
				"name":    "multica-agent-sdk",
				"version": "0.2.0",
			},
			"clientCapabilities": map[string]any{},
		})
		if err != nil {
			finalStatus = "failed"
			finalError = fmt.Sprintf("antigravity-acp initialize failed: %v", err)
			resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
			return
		}

		// 2. Authenticate only with a non-interactive method, and only when
		// its credential is actually present — see selectAntigravityAuthMethod.
		// Skipping this step entirely (no method selected) is the common case
		// and relies on a login already completed via the interactive flow.
		methodID := selectAntigravityAuthMethod(
			extractACPAuthMethods(initResult),
			envHasNonEmpty(childEnv, "GEMINI_API_KEY"),
			envHasNonEmpty(childEnv, "GOOGLE_API_KEY") || envHasNonEmpty(childEnv, "GOOGLE_APPLICATION_CREDENTIALS"),
		)
		if methodID != "" {
			if _, err := c.request(runCtx, "authenticate", map[string]any{"methodId": methodID}); err != nil {
				finalStatus = "failed"
				finalError = fmt.Sprintf("antigravity-acp authenticate (%s) failed: %v", methodID, err)
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}
			b.cfg.Logger.Info("antigravity-acp authenticated", "method", methodID)
		}

		// mcpCapabilities is {"http":true,"sse":true} — no stdio. Drop any
		// stdio-transport MCP entries rather than send a session/new the
		// runtime would reject outright.
		mcpServers = filterACPMcpServersByCapability(mcpServers, extractACPMcpCapabilities(initResult), "antigravity-acp", b.cfg)

		// 3. Create or resume a session.
		cwd := opts.Cwd
		if cwd == "" {
			cwd = "."
		}

		if opts.ResumeSessionID != "" {
			result, err := c.request(runCtx, "session/resume", map[string]any{
				"cwd":        cwd,
				"sessionId":  opts.ResumeSessionID,
				"mcpServers": mcpServers,
			})
			if err != nil {
				finalStatus = "failed"
				finalError = antigravitySessionFailureMessage(fmt.Sprintf("antigravity-acp session/resume failed: %v", err), err)
				if isACPSessionNotFound(err) {
					sessionID = ""
					resumeRejected = true
				}
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds(), ResumeRejected: resumeRejected}
				return
			}
			var changed bool
			sessionID, changed = resolveResumedSessionID(opts.ResumeSessionID, result)
			if changed {
				b.cfg.Logger.Warn("agent returned a different session id on resume — original was likely lost; continuing with the new id",
					"backend", "antigravity-acp",
					"requested", opts.ResumeSessionID,
					"actual", sessionID,
				)
			}
		} else {
			result, err := c.request(runCtx, "session/new", map[string]any{
				"cwd":        cwd,
				"mcpServers": mcpServers,
			})
			if err != nil {
				if runCtx.Err() == context.DeadlineExceeded {
					finalStatus = "timeout"
					finalError = fmt.Sprintf("antigravity-acp timed out during session/new: %v", timeout)
				} else if runCtx.Err() == context.Canceled {
					finalStatus = "aborted"
					finalError = fmt.Sprintf("antigravity-acp aborted: %v", err)
				} else {
					finalStatus = "failed"
					finalError = antigravitySessionFailureMessage(fmt.Sprintf("antigravity-acp session/new failed: %v", err), err)
				}
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}
			sessionID = extractACPSessionID(result)
			if sessionID == "" {
				finalStatus = "failed"
				finalError = "antigravity-acp session/new returned no session ID"
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}
		}

		if sessionID == "" {
			finalStatus = "failed"
			finalError = "antigravity-acp session ID is empty"
			resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds(), ResumeRejected: resumeRejected}
			return
		}

		c.sessionID = sessionID
		b.cfg.Logger.Info("antigravity-acp session created", "session_id", sessionID)

		// 4. Build the prompt content. If we have a system prompt, prepend it.
		userText := prompt
		if opts.SystemPrompt != "" {
			userText = opts.SystemPrompt + "\n\n---\n\n" + prompt
		}

		// 5. Send the prompt and wait for PromptResponse.
		_, err = c.request(runCtx, "session/prompt", map[string]any{
			"sessionId": sessionID,
			"prompt": []map[string]any{
				{"type": "text", "text": userText},
			},
		})
		if err != nil {
			if runCtx.Err() == context.DeadlineExceeded {
				finalStatus = "timeout"
				finalError = fmt.Sprintf("antigravity-acp timed out after %s", timeout)
			} else if runCtx.Err() == context.Canceled {
				finalStatus = "aborted"
				finalError = "execution cancelled"
			} else {
				finalStatus = "failed"
				finalError = antigravitySessionFailureMessage(fmt.Sprintf("antigravity-acp session/prompt failed: %v", err), err)
				if opts.ResumeSessionID != "" && isACPSessionNotFound(err) {
					sessionID = ""
					resumeRejected = true
				}
			}
		} else {
			select {
			case pr := <-promptDone:
				if pr.stopReason == "cancelled" {
					duration := time.Since(startTime)
					b.cfg.Logger.Info("antigravity-acp prompt cancelled", "stopReason", pr.stopReason, "duration", duration.Round(time.Millisecond).String())
				}
				c.mergeUsage(pr.usage)
			default:
			}
		}

		duration := time.Since(startTime)
		b.cfg.Logger.Info("antigravity-acp finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

		stdin.Close()
		cancel()

		<-readerDone
		<-stderrDone

		outputMu.Lock()
		finalOutput := output.String()
		outputMu.Unlock()

		finalStatus, finalError = promoteACPResultOnProviderError(finalStatus, finalError, finalOutput, providerErr)

		u := c.accumulatedUsage()

		var usageMap map[string]TokenUsage
		if acpUsagePresent(u) {
			// Model selection is unsupported (see ModelSelectionSupported), so
			// the backend never sends opts.Model to the agent. Always
			// attribute usage to "unknown" rather than a model that was never
			// applied — mirrors the qwenpaw precedent.
			usageMap = map[string]TokenUsage{"unknown": u}
		}

		resCh <- Result{
			Status:         finalStatus,
			Output:         finalOutput,
			Error:          finalError,
			DurationMs:     duration.Milliseconds(),
			SessionID:      sessionID,
			ResumeRejected: resumeRejected,
			Usage:          usageMap,
		}
	}()

	return &Session{Messages: msgCh, Result: resCh}, nil
}
