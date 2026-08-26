package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type opencodeExportResult struct {
	output string
	usage  TokenUsage
}

func (b *opencodeBackend) exportSession(parent context.Context, executable string, opts ExecOptions, sessionID string, env []string) (opencodeExportResult, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	cmd := b.cfg.commandAt(executable).exec(ctx, "export", "--format", "json", "--session", sessionID)
	cmd.Dir = opts.Cwd
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	data, err := cmd.Output()
	if err != nil {
		return opencodeExportResult{}, err
	}
	var transcript struct {
		Messages []struct {
			Role  string `json:"role"`
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"messages"`
		Usage *opencodeTokens `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(data, &transcript); err != nil {
		return opencodeExportResult{}, fmt.Errorf("parse export: %w", err)
	}
	var reasoning strings.Builder
	for i := len(transcript.Messages) - 1; i >= 0; i-- {
		if transcript.Messages[i].Role != "assistant" {
			continue
		}
		var output strings.Builder
		for _, part := range transcript.Messages[i].Parts {
			if part.Type == "text" {
				output.WriteString(part.Text)
			}
			if part.Type == "reasoning" {
				reasoning.WriteString(part.Text)
			}
		}
		if text := strings.TrimSpace(output.String()); text != "" {
			return opencodeExportResult{output: text, usage: exportUsage(transcript.Usage)}, nil
		}
	}
	if output := strings.TrimSpace(reasoning.String()); output != "" {
		return opencodeExportResult{output: output, usage: exportUsage(transcript.Usage)}, nil
	}
	return opencodeExportResult{}, fmt.Errorf("export contains no assistant text")
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
