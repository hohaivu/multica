package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAntigravityACPPlatformKey(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "darwin-aarch64"},
		{"darwin", "amd64", ""}, // no ACP build for Intel Macs
		{"linux", "amd64", "linux-x86_64"},
		{"linux", "arm64", "linux-aarch64"},
		{"windows", "amd64", "windows-x86_64"},
		{"windows", "arm64", "windows-aarch64"},
		{"linux", "386", ""},
	}
	for _, tc := range tests {
		if got := antigravityACPPlatformKey(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("antigravityACPPlatformKey(%q,%q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestAntigravityACPVersionFromArchiveURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://dl.google.com/agy-extensions/releases/macos/agy-acp-server-agy_acp_server_20260818_01_RC01-darwin-arm64.zip", "agy_acp_server_20260818_01_RC01"},
		{"https://dl.google.com/agy-extensions/releases/linux/agy-acp-server-agy_acp_server_20260818_01_RC01-linux-x86_64.zip", "agy_acp_server_20260818_01_RC01"},
		{"https://dl.google.com/agy-extensions/releases/windows/agy-acp-server-agy_acp_server_20260818_01_RC01-windows-arm64.zip", "agy_acp_server_20260818_01_RC01"},
	}
	for _, tc := range tests {
		if got := antigravityACPVersionFromArchiveURL(tc.url); got != tc.want {
			t.Errorf("antigravityACPVersionFromArchiveURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestAntigravityACPCredentialsPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GEMINI_HOME", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	if antigravityACPCredentialsPresent() {
		t.Fatal("expected no credentials present")
	}

	t.Setenv("GEMINI_API_KEY", "test-key")
	if !antigravityACPCredentialsPresent() {
		t.Fatal("expected GEMINI_API_KEY to count as a credential")
	}
	t.Setenv("GEMINI_API_KEY", "")

	settingsDir := filepath.Join(home, ".gemini", "antigravity-acp")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !antigravityACPCredentialsPresent() {
		t.Fatal("expected settings.json to count as a stored credential")
	}
}

func writeFakeAntigravityACPInstall(t *testing.T, home string) string {
	t.Helper()
	root := filepath.Join(home, ".multica", "runtimes", "antigravity-acp")
	versionDir := filepath.Join(root, "v1")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(versionDir, "agy_acp_server.par")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := antigravityACPInstallManifest{Version: "v1", Cmd: "./agy_acp_server.par"}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "current-version.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cmdPath
}

func TestManagedACPServerPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got := managedACPServerPath(); got != "" {
		t.Fatalf("expected no managed install, got %q", got)
	}

	cmdPath := writeFakeAntigravityACPInstall(t, home)
	if got := managedACPServerPath(); got != cmdPath {
		t.Fatalf("managedACPServerPath() = %q, want %q", got, cmdPath)
	}
}

// TestProbeAgentCLIsRequiresAntigravityACPCredentials mirrors
// TestProbeAgentCLIsRequiresDshMulticaProfile: a binary being present on
// disk is not enough to register the runtime as healthy without a stored
// or env credential, since every task would fail on session/new.
func TestProbeAgentCLIsRequiresAntigravityACPCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GEMINI_HOME", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("MULTICA_ANTIGRAVITY_ACP_PATH", "")

	cmdPath := writeFakeAntigravityACPInstall(t, home)

	if _, found := probeAgentCLIs()["antigravityacp"]; found {
		t.Fatal("expected antigravityacp to be excluded without credentials")
	}

	settingsDir := filepath.Join(home, ".gemini", "antigravity-acp")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, found := probeAgentCLIs()["antigravityacp"]
	if !found {
		t.Fatal("expected antigravityacp to be discovered once credentials are present")
	}
	if entry.Path != cmdPath {
		t.Fatalf("entry.Path = %q, want %q", entry.Path, cmdPath)
	}
}
