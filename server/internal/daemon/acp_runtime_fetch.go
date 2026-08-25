package daemon

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Daemon-managed acquisition of the Antigravity ACP server
// (agy_acp_server.par), a separate ~300MB Google download distinct from the
// `agy` CLI that server/pkg/agent/antigravity.go drives. Acquisition is
// never triggered from daemon startup — only from an explicit CLI command
// (`multica runtime install antigravity-acp`) — and it never calls the ACP
// `authenticate` method itself: doing so with an interactive method opens a
// real, blocking Google OAuth browser flow with no headless fallback (see
// selectAntigravityAuthMethod in pkg/agent/antigravity_acp.go).

// antigravityACPRegistryURL is a var, not a const, so tests can point it at
// an httptest server instead of the real registry.
var antigravityACPRegistryURL = "https://raw.githubusercontent.com/agentclientprotocol/registry/refs/heads/main/antigravity-acp/agent.json"

// antigravityACPRegistryManifest mirrors the subset of
// agentclientprotocol/registry's antigravity-acp/agent.json this daemon
// reads. Confirmed live 2026-08-25 (see the "Migrate Antigravity to ACP"
// plan, Phase 0): darwin/windows binaries carry no extra args; linux
// binaries require the literal, non-templated flag "--uid=".
type antigravityACPRegistryManifest struct {
	Distribution struct {
		Binary map[string]struct {
			Archive string   `json:"archive"`
			Cmd     string   `json:"cmd"`
			Args    []string `json:"args"`
		} `json:"binary"`
	} `json:"distribution"`
}

// antigravityACPInstallManifest is our own sidecar record, written next to
// the extracted binary so a re-install of the same archive is a no-op and a
// rotated registry URL is detectable.
type antigravityACPInstallManifest struct {
	Version     string    `json:"version"`
	Archive     string    `json:"archive"`
	SHA256      string    `json:"sha256"`
	Cmd         string    `json:"cmd"`
	Args        []string  `json:"args"`
	InstalledAt time.Time `json:"installed_at"`
}

// antigravityACPPlatformKey maps Go's GOOS/GOARCH to the registry's platform
// key. Returns "" for a combination the registry does not publish — notably
// darwin/amd64: there is no ACP build for Intel Macs, which must keep using
// the `agy` CLI runtime instead.
func antigravityACPPlatformKey(goos, goarch string) string {
	switch goos {
	case "darwin":
		if goarch == "arm64" {
			return "darwin-aarch64"
		}
	case "linux":
		switch goarch {
		case "amd64":
			return "linux-x86_64"
		case "arm64":
			return "linux-aarch64"
		}
	case "windows":
		switch goarch {
		case "amd64":
			return "windows-x86_64"
		case "arm64":
			return "windows-aarch64"
		}
	}
	return ""
}

// antigravityACPRoot is the daemon-managed install root. Returns "" when the
// home directory can't be resolved.
func antigravityACPRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".multica", "runtimes", "antigravity-acp")
}

func antigravityCurrentVersionFile(root string) string {
	return filepath.Join(root, "current-version.txt")
}

// managedACPServerPath returns the absolute path to the currently installed
// Antigravity ACP server executable, or "" if nothing is installed. Safe to
// call unconditionally at probe time — every branch is a local stat, never a
// network call, so a host with nothing installed pays no cost.
func managedACPServerPath() string {
	root := antigravityACPRoot()
	if root == "" {
		return ""
	}
	versionBytes, err := os.ReadFile(antigravityCurrentVersionFile(root))
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(string(versionBytes))
	if version == "" {
		return ""
	}
	versionDir := filepath.Join(root, version)
	var manifest antigravityACPInstallManifest
	manifestBytes, err := os.ReadFile(filepath.Join(versionDir, "manifest.json"))
	if err != nil {
		return ""
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.Cmd == "" {
		return ""
	}
	path := filepath.Join(versionDir, strings.TrimPrefix(manifest.Cmd, "./"))
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// antigravityGeminiHome resolves the Gemini config root the ACP server reads
// credentials from. Confirmed live 2026-08-25: "Gemini home resolved to
// ~/.gemini (default; $GEMINI_HOME is unset)".
func antigravityGeminiHome() string {
	if v := strings.TrimSpace(os.Getenv("GEMINI_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini")
}

// antigravityACPCredentialsPresent reports whether the ACP server already
// has something to authenticate with — either a completed interactive login
// (~/.gemini/antigravity-acp/settings.json) or a non-interactive credential
// env var (matching the ones selectAntigravityAuthMethod checks for). The
// probe uses this the same way probeDshMulticaProfile gates dsh: a binary
// present with no usable credential must not be registered as a healthy
// runtime, since every task would fail on session/new.
func antigravityACPCredentialsPresent() bool {
	for _, key := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	home := antigravityGeminiHome()
	if home == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(home, "antigravity-acp", "settings.json"))
	return err == nil
}

// antigravityACPVersionFromArchiveURL extracts the build id (e.g.
// "agy_acp_server_20260818_01_RC01") from an archive filename such as
// "agy-acp-server-agy_acp_server_20260818_01_RC01-darwin-arm64.zip". The
// trailing platform suffix uses Go-style arch names (arm64), which differ
// from the registry's platform keys (aarch64), so it is located by its
// known os-name token rather than by echoing the registry key back.
func antigravityACPVersionFromArchiveURL(archiveURL string) string {
	base := archiveURLBasename(archiveURL)
	base = strings.TrimSuffix(base, ".zip")
	base = strings.TrimPrefix(base, "agy-acp-server-")
	for _, osToken := range []string{"-darwin-", "-linux-", "-windows-"} {
		if idx := strings.Index(base, osToken); idx >= 0 {
			return base[:idx]
		}
	}
	return base
}

// archiveURLBasename avoids importing net/url just to strip a query-free URL
// down to its filename; archive URLs here are plain https paths with no
// query string.
func archiveURLBasename(u string) string {
	if idx := strings.LastIndexByte(u, '/'); idx >= 0 {
		return u[idx+1:]
	}
	return u
}

// AntigravityACPInstallStatus reports on an install without touching the
// network, for `multica runtime install antigravity-acp --status` and
// similar read-only checks.
type AntigravityACPInstallStatus struct {
	Installed          bool   `json:"installed"`
	Version            string `json:"version,omitempty"`
	Path               string `json:"path,omitempty"`
	CredentialsPresent bool   `json:"credentials_present"`
}

func AntigravityACPStatus() AntigravityACPInstallStatus {
	path := managedACPServerPath()
	status := AntigravityACPInstallStatus{
		Installed:          path != "",
		Path:               path,
		CredentialsPresent: antigravityACPCredentialsPresent(),
	}
	if root := antigravityACPRoot(); root != "" {
		if v, err := os.ReadFile(antigravityCurrentVersionFile(root)); err == nil {
			status.Version = strings.TrimSpace(string(v))
		}
	}
	return status
}

// InstallAntigravityACP downloads and installs the Antigravity ACP server
// for the current platform. It never runs implicitly — callers are explicit
// CLI/daemon-command invocations only. progress receives short human-
// readable status lines.
func InstallAntigravityACP(ctx context.Context, progress io.Writer) (string, error) {
	if progress == nil {
		progress = io.Discard
	}

	platformKey := antigravityACPPlatformKey(runtime.GOOS, runtime.GOARCH)
	if platformKey == "" {
		if runtime.GOOS == "darwin" {
			return "", fmt.Errorf("antigravity-acp has no build for %s/%s (Intel Mac); use the antigravity (agy CLI) runtime instead", runtime.GOOS, runtime.GOARCH)
		}
		return "", fmt.Errorf("antigravity-acp has no build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	root := antigravityACPRoot()
	if root == "" {
		return "", fmt.Errorf("antigravity-acp: could not resolve home directory")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("antigravity-acp: create install root: %w", err)
	}

	fmt.Fprintln(progress, "Fetching antigravity-acp registry manifest...")
	entry, err := fetchAntigravityACPRegistryEntry(ctx, platformKey)
	if err != nil {
		return "", err
	}

	version := antigravityACPVersionFromArchiveURL(entry.Archive)
	if version == "" {
		return "", fmt.Errorf("antigravity-acp: could not derive a version from archive URL %q", entry.Archive)
	}

	if existing := managedACPServerPath(); existing != "" {
		var existingManifest antigravityACPInstallManifest
		if b, err := os.ReadFile(filepath.Join(root, version, "manifest.json")); err == nil {
			if json.Unmarshal(b, &existingManifest) == nil && existingManifest.Archive == entry.Archive {
				fmt.Fprintf(progress, "antigravity-acp %s already installed at %s (no-op)\n", version, existing)
				return version, nil
			}
		}
	}

	fmt.Fprintf(progress, "Downloading %s...\n", entry.Archive)
	archivePath, digest, err := downloadAntigravityACPArchive(ctx, root, entry.Archive)
	if err != nil {
		return "", err
	}
	defer os.Remove(archivePath)

	extractDir, err := os.MkdirTemp(root, ".extract-*")
	if err != nil {
		return "", fmt.Errorf("antigravity-acp: create extraction dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	fmt.Fprintln(progress, "Extracting...")
	if err := extractAntigravityACPArchive(archivePath, extractDir); err != nil {
		return "", err
	}

	cmdRel := strings.TrimPrefix(entry.Cmd, "./")
	cmdPath := filepath.Join(extractDir, cmdRel)
	if _, err := os.Stat(cmdPath); err != nil {
		return "", fmt.Errorf("antigravity-acp: extracted archive is missing %q: %w", cmdRel, err)
	}
	if err := os.Chmod(cmdPath, 0o755); err != nil {
		return "", fmt.Errorf("antigravity-acp: chmod %q: %w", cmdRel, err)
	}
	if runtime.GOOS == "darwin" {
		// Gatekeeper kills the first exec of a freshly-downloaded quarantined
		// binary unless the quarantine xattr is cleared. Best-effort: a Go HTTP
		// download does not set com.apple.quarantine, so a failure here is not
		// worth discarding the extracted tree.
		if out, err := exec.CommandContext(ctx, "xattr", "-cr", extractDir).CombinedOutput(); err != nil {
			fmt.Fprintf(progress, "warning: xattr -cr failed: %v (%s)\n", err, strings.TrimSpace(string(out)))
		}
	}

	installManifest := antigravityACPInstallManifest{
		Version:     version,
		Archive:     entry.Archive,
		SHA256:      digest,
		Cmd:         entry.Cmd,
		Args:        entry.Args,
		InstalledAt: time.Now().UTC(),
	}
	manifestBytes, err := json.MarshalIndent(installManifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("antigravity-acp: marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(extractDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return "", fmt.Errorf("antigravity-acp: write manifest: %w", err)
	}

	versionDir := filepath.Join(root, version)
	os.RemoveAll(versionDir) // best-effort: clear a stale partial install of the same version
	if err := os.Rename(extractDir, versionDir); err != nil {
		return "", fmt.Errorf("antigravity-acp: install %s: %w", version, err)
	}

	if err := writeFileAtomic(antigravityCurrentVersionFile(root), []byte(version)); err != nil {
		return "", fmt.Errorf("antigravity-acp: record current version: %w", err)
	}

	fmt.Fprintf(progress, "Installed antigravity-acp %s at %s\n", version, filepath.Join(versionDir, cmdRel))
	if !antigravityACPCredentialsPresent() {
		fmt.Fprintln(progress, "antigravity-acp is installed but not authenticated.")
		fmt.Fprintln(progress, "Run `multica runtime auth antigravity-acp` once, or set GEMINI_API_KEY / GOOGLE_API_KEY.")
	}
	return version, nil
}

func fetchAntigravityACPRegistryEntry(ctx context.Context, platformKey string) (struct {
	Archive string
	Cmd     string
	Args    []string
}, error) {
	var zero struct {
		Archive string
		Cmd     string
		Args    []string
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, antigravityACPRegistryURL, nil)
	if err != nil {
		return zero, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("antigravity-acp: fetch registry manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("antigravity-acp: fetch registry manifest: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return zero, fmt.Errorf("antigravity-acp: read registry manifest: %w", err)
	}
	var manifest antigravityACPRegistryManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return zero, fmt.Errorf("antigravity-acp: parse registry manifest: %w", err)
	}
	entry, ok := manifest.Distribution.Binary[platformKey]
	if !ok {
		return zero, fmt.Errorf("antigravity-acp: registry manifest has no binary for platform %q", platformKey)
	}
	if entry.Archive == "" || entry.Cmd == "" {
		return zero, fmt.Errorf("antigravity-acp: registry manifest entry for %q is missing archive or cmd", platformKey)
	}
	zero.Archive, zero.Cmd, zero.Args = entry.Archive, entry.Cmd, entry.Args
	return zero, nil
}

// downloadAntigravityACPArchive streams the archive to a temp file inside
// root (so the later rename of its extraction stays on the same filesystem)
// while hashing it, rather than buffering the ~300MB body in memory the way
// internal/cli/update.go's fetchURLBytes does.
func downloadAntigravityACPArchive(ctx context.Context, root, archiveURL string) (path string, sha256Hex string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("antigravity-acp: download archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("antigravity-acp: download archive: unexpected status %s", resp.Status)
	}

	tmp, err := os.CreateTemp(root, "download-*.zip")
	if err != nil {
		return "", "", fmt.Errorf("antigravity-acp: create download temp file: %w", err)
	}
	defer tmp.Close()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", "", fmt.Errorf("antigravity-acp: download archive: %w", err)
	}
	return tmp.Name(), hex.EncodeToString(hasher.Sum(nil)), nil
}

// extractAntigravityACPArchive extracts a flat zip (confirmed: no top-level
// directory — agy_acp_server.par and localharness_external sit side by
// side) into destDir, guarding against zip-slip path traversal since the
// archive is untrusted network content.
func extractAntigravityACPArchive(archivePath, destDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("antigravity-acp: open archive: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		destPath := filepath.Join(destDir, file.Name)
		if !strings.HasPrefix(destPath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("antigravity-acp: archive entry %q escapes extraction directory", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		if err := extractAntigravityACPArchiveFile(file, destPath); err != nil {
			return err
		}
	}
	return nil
}

func extractAntigravityACPArchiveFile(file *zip.File, destPath string) error {
	src, err := file.Open()
	if err != nil {
		return fmt.Errorf("antigravity-acp: open archive entry %q: %w", file.Name, err)
	}
	defer src.Close()

	// The archive ships localharness_external as 0555; keep owner write so
	// post-extract steps (xattr -cr, re-extraction over an existing tree) can
	// touch it. Execute bits from the archive are preserved.
	mode := file.Mode().Perm() | 0o200
	if mode == 0o200 {
		mode = 0o644
	}
	dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("antigravity-acp: create %q: %w", destPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("antigravity-acp: write %q: %w", destPath, err)
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file + rename in the same
// directory, so a crash mid-write never leaves a truncated pointer file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
