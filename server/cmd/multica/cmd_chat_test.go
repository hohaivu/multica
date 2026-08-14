package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newChatReadRenderTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "history"}
	cmd.Flags().String("output", "table", "")
	return cmd
}

func TestRenderChatReadSlackOverviewKeepsThreadColumnsWithoutThreads(t *testing.T) {
	cmd := newChatReadRenderTestCmd(t)
	resp := map[string]any{
		"channel_type": "slack",
		"messages": []any{
			map[string]any{
				"ts":     "1700000000.000100",
				"role":   "user",
				"author": "Alice",
				"text":   "No replies on this page",
			},
		},
	}

	out, err := captureStdout(t, func() error { return renderChatRead(cmd, resp, true) })
	if err != nil {
		t.Fatalf("renderChatRead: %v", err)
	}
	for _, want := range []string{"THREAD_ID", "REPLIES"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Slack overview missing %q when page has no threaded rows:\n%s", want, out)
		}
	}
}

func TestRenderChatReadSlackWithThreadMetadataKeepsThreadColumns(t *testing.T) {
	cmd := newChatReadRenderTestCmd(t)
	resp := map[string]any{
		"channel_type": "slack",
		"messages": []any{
			map[string]any{
				"ts":          "1700000000.000100",
				"role":        "user",
				"author":      "Alice",
				"thread_id":   "1700000000.000100",
				"reply_count": float64(2),
				"text":        "Thread root",
			},
		},
	}

	out, err := captureStdout(t, func() error { return renderChatRead(cmd, resp, true) })
	if err != nil {
		t.Fatalf("renderChatRead: %v", err)
	}
	for _, want := range []string{"THREAD_ID", "REPLIES", "1700000000.000100", "2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Slack threaded overview missing %q:\n%s", want, out)
		}
	}
}

func TestRenderChatReadCompatibilityFallbackUsesThreadColumns(t *testing.T) {
	cmd := newChatReadRenderTestCmd(t)
	resp := map[string]any{
		"messages": []any{
			map[string]any{
				"ts":          "1700000000.000100",
				"role":        "user",
				"author":      "Alice",
				"thread_id":   "1700000000.000100",
				"reply_count": float64(1),
				"text":        "Legacy Slack response",
			},
		},
	}

	out, err := captureStdout(t, func() error { return renderChatRead(cmd, resp, true) })
	if err != nil {
		t.Fatalf("renderChatRead: %v", err)
	}
	if !strings.Contains(out, "THREAD_ID") || !strings.Contains(out, "REPLIES") {
		t.Fatalf("compatibility fallback omitted thread columns:\n%s", out)
	}
}

func TestRenderChatReadTranscriptOmitsThreadColumns(t *testing.T) {
	cmd := newChatReadRenderTestCmd(t)
	resp := map[string]any{
		"channel_type": "",
		"messages": []any{
			map[string]any{
				"ts":     "2026-08-14T07:00:00Z",
				"role":   "user",
				"author": "User",
				"text":   "Transcript message",
			},
		},
	}

	out, err := captureStdout(t, func() error { return renderChatRead(cmd, resp, true) })
	if err != nil {
		t.Fatalf("renderChatRead: %v", err)
	}
	for _, unwanted := range []string{"THREAD_ID", "REPLIES"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("transcript unexpectedly contains %q:\n%s", unwanted, out)
		}
	}
}
