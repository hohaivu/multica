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

func TestRenderChatReadSlackFallbackToTranscriptOmitsThreadColumns(t *testing.T) {
	// A Slack-bound session still falls back to the plain chat_message
	// transcript (channel_type stays "slack") whenever the live Slack read is
	// unavailable (h.SlackHistory == nil or slack.ErrNoSlackSession). Those
	// rows carry no thread metadata, so the layout must follow the rows, not
	// the channel type — otherwise THREAD_ID/REPLIES print empty.
	cmd := newChatReadRenderTestCmd(t)
	resp := map[string]any{
		"channel_type": "slack",
		"messages": []any{
			map[string]any{
				"ts":     "2026-08-14T07:00:00Z",
				"role":   "user",
				"author": "User",
				"text":   "Transcript fallback message",
			},
		},
	}

	out, err := captureStdout(t, func() error { return renderChatRead(cmd, resp, true) })
	if err != nil {
		t.Fatalf("renderChatRead: %v", err)
	}
	for _, unwanted := range []string{"THREAD_ID", "REPLIES"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("Slack transcript fallback unexpectedly contains %q:\n%s", unwanted, out)
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

func TestRenderChatReadNonSlackWithThreadMetadataKeepsThreadColumns(t *testing.T) {
	// The layout follows the rows, not channel_type: any channel overview that
	// carries thread metadata gets the thread columns, not just Slack.
	cmd := newChatReadRenderTestCmd(t)
	resp := map[string]any{
		"channel_type": "wecom",
		"messages": []any{
			map[string]any{
				"ts":          "1700000000.000100",
				"role":        "user",
				"author":      "Alice",
				"thread_id":   "1700000000.000100",
				"reply_count": float64(1),
				"text":        "Threaded response",
			},
		},
	}

	out, err := captureStdout(t, func() error { return renderChatRead(cmd, resp, true) })
	if err != nil {
		t.Fatalf("renderChatRead: %v", err)
	}
	if !strings.Contains(out, "THREAD_ID") || !strings.Contains(out, "REPLIES") {
		t.Fatalf("non-Slack overview with thread metadata omitted thread columns:\n%s", out)
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
