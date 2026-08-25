package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// antigravityACPTestPlatform returns the registry platform key and a
// matching archive filename for the current GOOS/GOARCH, or ("", "") if
// this platform has no antigravity-acp build (e.g. darwin/amd64).
func antigravityACPTestPlatform() (platformKey, archiveName string) {
	platformKey = antigravityACPPlatformKey(runtime.GOOS, runtime.GOARCH)
	if platformKey == "" {
		return "", ""
	}
	return platformKey, fmt.Sprintf("agy-acp-server-test_v1-%s-x86_64.zip", runtime.GOOS)
}

func buildFakeAntigravityACPArchive(t *testing.T, cmdContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: "agy_acp_server.par", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(cmdContent)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildFakeAntigravityACPArchiveWithReadOnlyEntry mirrors
// buildFakeAntigravityACPArchive but also adds a "localharness_external"
// entry with mode 0555, matching the real archive's read-only sidecar file.
func buildFakeAntigravityACPArchiveWithReadOnlyEntry(t *testing.T, cmdContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: "agy_acp_server.par", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(cmdContent)); err != nil {
		t.Fatal(err)
	}
	roHeader := &zip.FileHeader{Name: "localharness_external", Method: zip.Deflate}
	roHeader.SetMode(0o555)
	rw, err := zw.CreateHeader(roHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Write([]byte("fake read-only sidecar")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setAntigravityACPTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestInstallAntigravityACPExtractsAndRecordsManifest(t *testing.T) {
	platformKey, archiveName := antigravityACPTestPlatform()
	if platformKey == "" {
		t.Skipf("no antigravity-acp build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	setAntigravityACPTestHome(t)

	archiveBytes := buildFakeAntigravityACPArchive(t, "#!/bin/sh\necho fake-agy-acp-server\n")
	wantDigest := sha256.Sum256(archiveBytes)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// The manifest's archive field must be an absolute URL, which requires
	// knowing srv.URL, so register handlers after the server has started.
	mux.HandleFunc("/agent.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"distribution": map[string]any{
				"binary": map[string]any{
					platformKey: map[string]any{
						"archive": srv.URL + "/" + archiveName,
						"cmd":     "./agy_acp_server.par",
						"args":    []string{},
					},
				},
			},
		})
	})
	mux.HandleFunc("/"+archiveName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveBytes)
	})

	orig := antigravityACPRegistryURL
	antigravityACPRegistryURL = srv.URL + "/agent.json"
	t.Cleanup(func() { antigravityACPRegistryURL = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var progress bytes.Buffer
	version, err := InstallAntigravityACP(ctx, &progress)
	if err != nil {
		t.Fatalf("InstallAntigravityACP() error = %v, progress=%s", err, progress.String())
	}
	if version != "test_v1" {
		t.Fatalf("version = %q, want %q", version, "test_v1")
	}

	installedPath := managedACPServerPath()
	if installedPath == "" {
		t.Fatal("managedACPServerPath() is empty after install")
	}
	if _, err := os.Stat(installedPath); err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	info, err := os.Stat(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed binary is not executable: mode=%v", info.Mode())
	}

	root := antigravityACPRoot()
	manifestBytes, err := os.ReadFile(filepath.Join(root, version, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest antigravityACPInstallManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	if manifest.SHA256 != hex.EncodeToString(wantDigest[:]) {
		t.Errorf("manifest sha256 = %q, want %q", manifest.SHA256, hex.EncodeToString(wantDigest[:]))
	}
}

// TestInstallAntigravityACPMakesReadOnlyEntriesWritable reproduces the real
// antigravity-acp archive, which ships localharness_external at mode 0555.
// Extracting that mode verbatim leaves the file un-writable, which makes
// `xattr -cr` fail with EACCES and previously aborted the whole install.
func TestInstallAntigravityACPMakesReadOnlyEntriesWritable(t *testing.T) {
	platformKey, archiveName := antigravityACPTestPlatform()
	if platformKey == "" {
		t.Skipf("no antigravity-acp build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	setAntigravityACPTestHome(t)

	archiveBytes := buildFakeAntigravityACPArchiveWithReadOnlyEntry(t, "#!/bin/sh\necho fake-agy-acp-server\n")

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/agent.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"distribution": map[string]any{
				"binary": map[string]any{
					platformKey: map[string]any{
						"archive": srv.URL + "/" + archiveName,
						"cmd":     "./agy_acp_server.par",
						"args":    []string{},
					},
				},
			},
		})
	})
	mux.HandleFunc("/"+archiveName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveBytes)
	})

	orig := antigravityACPRegistryURL
	antigravityACPRegistryURL = srv.URL + "/agent.json"
	t.Cleanup(func() { antigravityACPRegistryURL = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var progress bytes.Buffer
	version, err := InstallAntigravityACP(ctx, &progress)
	if err != nil {
		t.Fatalf("InstallAntigravityACP() error = %v, progress=%s", err, progress.String())
	}

	sidecarPath := filepath.Join(antigravityACPRoot(), version, "localharness_external")
	info, err := os.Stat(sidecarPath)
	if err != nil {
		t.Fatalf("installed sidecar missing: %v", err)
	}
	if info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("installed sidecar is not owner-writable: mode=%v", info.Mode())
	}

	installedPath := managedACPServerPath()
	cmdInfo, err := os.Stat(installedPath)
	if err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	if cmdInfo.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed binary is not executable: mode=%v", cmdInfo.Mode())
	}
}

func TestInstallAntigravityACPSecondRunIsNoOp(t *testing.T) {
	platformKey, archiveName := antigravityACPTestPlatform()
	if platformKey == "" {
		t.Skipf("no antigravity-acp build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	setAntigravityACPTestHome(t)

	archiveBytes := buildFakeAntigravityACPArchive(t, "#!/bin/sh\necho fake-agy-acp-server\n")

	var manifestHits, archiveHits atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/agent.json", func(w http.ResponseWriter, r *http.Request) {
		manifestHits.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"distribution": map[string]any{
				"binary": map[string]any{
					platformKey: map[string]any{
						"archive": srv.URL + "/" + archiveName,
						"cmd":     "./agy_acp_server.par",
						"args":    []string{},
					},
				},
			},
		})
	})
	mux.HandleFunc("/"+archiveName, func(w http.ResponseWriter, r *http.Request) {
		archiveHits.Add(1)
		w.Write(archiveBytes)
	})

	orig := antigravityACPRegistryURL
	antigravityACPRegistryURL = srv.URL + "/agent.json"
	t.Cleanup(func() { antigravityACPRegistryURL = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := InstallAntigravityACP(ctx, nil); err != nil {
		t.Fatalf("first InstallAntigravityACP() error = %v", err)
	}
	if _, err := InstallAntigravityACP(ctx, nil); err != nil {
		t.Fatalf("second InstallAntigravityACP() error = %v", err)
	}

	if got := archiveHits.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1 (second install should be a no-op)", got)
	}
	if got := manifestHits.Load(); got != 2 {
		t.Errorf("registry manifest fetched %d times, want 2", got)
	}
}

func TestInstallAntigravityACPBadArchiveLeavesNoPartialInstall(t *testing.T) {
	platformKey, archiveName := antigravityACPTestPlatform()
	if platformKey == "" {
		t.Skipf("no antigravity-acp build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	setAntigravityACPTestHome(t)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/agent.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"distribution": map[string]any{
				"binary": map[string]any{
					platformKey: map[string]any{
						"archive": srv.URL + "/" + archiveName,
						"cmd":     "./agy_acp_server.par",
						"args":    []string{},
					},
				},
			},
		})
	})
	mux.HandleFunc("/"+archiveName, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a zip file"))
	})

	orig := antigravityACPRegistryURL
	antigravityACPRegistryURL = srv.URL + "/agent.json"
	t.Cleanup(func() { antigravityACPRegistryURL = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := InstallAntigravityACP(ctx, nil); err == nil {
		t.Fatal("InstallAntigravityACP() with a corrupt archive succeeded, want error")
	}

	if path := managedACPServerPath(); path != "" {
		t.Fatalf("managedACPServerPath() = %q after a failed install, want \"\"", path)
	}
	root := antigravityACPRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read install root: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "test_v1" {
			t.Errorf("a version directory %q was left behind after a failed install", e.Name())
		}
	}
}
