package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type opencodeExportResult struct {
	output     string
	usage      TokenUsage
	providerID string
	modelID    string
}

func (b *opencodeBackend) exportSession(parent context.Context, executable string, opts ExecOptions, sessionID string, env []string) (opencodeExportResult, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	// export takes the session ID positionally; cwd/PWD scopes it to the run's project.
	cmd := b.cfg.commandAt(executable).exec(ctx, "export", sessionID)
	hideAgentWindow(cmd)
	cmd.Dir = opts.Cwd
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	data, err := cmd.Output()
	if err != nil {
		return opencodeExportResult{}, err
	}
	return parseOpencodeExport(data)
}

func parseOpencodeExport(data []byte) (opencodeExportResult, error) {
	var transcript struct {
		Messages []struct {
			Info struct {
				Role       string          `json:"role"`
				Tokens     *opencodeTokens `json:"tokens,omitempty"`
				ProviderID string          `json:"providerID,omitempty"`
				ModelID    string          `json:"modelID,omitempty"`
			} `json:"info"`
			Parts []opencodeEventPart `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &transcript); err != nil {
		return opencodeExportResult{}, fmt.Errorf("parse export: %w", err)
	}
	selected := -1
	for i := range transcript.Messages {
		if transcript.Messages[i].Info.Role == "assistant" {
			selected = i
		}
	}
	if selected < 0 {
		return opencodeExportResult{}, fmt.Errorf("export contains no assistant text")
	}
	var text, reasoning strings.Builder
	message := transcript.Messages[selected]
	for _, part := range message.Parts {
		if part.Type == "text" {
			text.WriteString(part.Text)
		} else if part.Type == "reasoning" {
			reasoning.WriteString(part.Text)
		}
	}
	output := strings.TrimSpace(text.String())
	if output == "" {
		output = strings.TrimSpace(reasoning.String())
	}
	if output == "" {
		return opencodeExportResult{}, fmt.Errorf("export contains no assistant text")
	}
	return opencodeExportResult{
		output:     output,
		usage:      exportUsage(message.Info.Tokens),
		providerID: message.Info.ProviderID,
		modelID:    message.Info.ModelID,
	}, nil
}

func exportUsage(t *opencodeTokens) TokenUsage {
	if t == nil {
		return TokenUsage{}
	}
	u := TokenUsage{InputTokens: t.Input, OutputTokens: t.Output}
	if t.Cache != nil {
		u.CacheReadTokens = t.Cache.Read
		u.CacheWriteTokens = t.Cache.Write
	}
	return u
}
