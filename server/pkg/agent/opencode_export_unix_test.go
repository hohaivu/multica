//go:build unix

package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpencodeExportFallback(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "opencode")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = run ]; then
  cat >/dev/null
  printf '%%s\n' '{"type":"step_start","sessionID":"ses_fake","part":{"type":"step-start"}}'
  printf '%%s\n' '{"type":"step_finish","sessionID":"ses_fake","part":{"type":"step-finish","tokens":{"total":1}}}'
elif [ "$1" = export ] && [ "$2" = ses_fake ] && [ "$3" = '' ]; then
  printf '%%s\n' export ses_fake > %[1]q
  printf '%%s' '{"messages":[{"info":{"role":"assistant","providerID":"p","modelID":"m","tokens":{"input":4,"output":7}},"parts":[{"type":"text","text":"recovered"}]}]}'
else
  printf '%%s\n' bad "$@" > %[1]q
  exit 1
fi
`, argvPath)
	writeTestExecutable(t, fake, []byte(script))
	backend, err := New("opencode", Config{ExecutablePath: fake, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := backend.Execute(t.Context(), "prompt", ExecOptions{Cwd: dir, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var messages []Message
	for msg := range session.Messages {
		messages = append(messages, msg)
	}
	result := <-session.Result
	if result.Status != "completed" || result.Error != "" || result.Output != "recovered" {
		t.Fatalf("result = %+v", result)
	}
	if result.Usage["p/m"].OutputTokens != 7 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	for _, msg := range messages {
		if msg.Type == MessageError {
			t.Fatalf("unexpected recovery error: %+v", msg)
		}
	}
	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(argv)) != "export\nses_fake" {
		t.Fatalf("argv = %q", argv)
	}
}

func TestOpencodeExportFallbackFailureIsNonFatal(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "opencode")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = run ]; then
  cat >/dev/null
  printf '%%s\n' '{"type":"step_start","sessionID":"ses_fake","part":{"type":"step-start"}}'
  printf '%%s\n' '{"type":"step_finish","sessionID":"ses_fake","part":{"type":"step-finish","tokens":{"total":1}}}'
elif [ "$1" = export ]; then
  printf '%%s\n' export "$@" > %[1]q
  exit 1
fi
`, argvPath)
	writeTestExecutable(t, fake, []byte(script))
	backend, err := New("opencode", Config{ExecutablePath: fake, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := backend.Execute(t.Context(), "prompt", ExecOptions{Cwd: dir, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "failed" || result.Error != "opencode returned empty output after closing a step" || result.Output != "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(argvPath); err != nil {
		t.Fatal(err)
	}
}

func TestOpencodeNormalStreamingDoesNotExport(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "opencode")
	script := fmt.Sprintf(`#!/bin/sh
cat >/dev/null
printf '%%s\n' "$@" > %[1]q
printf '%%s\n' '{"type":"step_start","sessionID":"ses_fake","part":{"type":"step-start"}}'
printf '%%s\n' '{"type":"text","sessionID":"ses_fake","part":{"type":"text","text":"streamed"}}'
printf '%%s\n' '{"type":"step_finish","sessionID":"ses_fake","part":{"type":"step-finish"}}'
`, argvPath)
	writeTestExecutable(t, fake, []byte(script))
	backend, err := New("opencode", Config{ExecutablePath: fake, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := backend.Execute(t.Context(), "prompt", ExecOptions{Cwd: dir, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Output != "streamed" {
		t.Fatalf("result = %+v", result)
	}
	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argv), "export") {
		t.Fatalf("unexpected export invocation: %q", argv)
	}
}
