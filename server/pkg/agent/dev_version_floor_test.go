package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// DEV_CLI_VERSION (Makefile) is the version a tagless checkout stamps onto
// from-source builds. It must parse as a semver at or above every
// Min*CLIVersion floor below, or a from-source daemon fails every capability
// gate the moment there is no v* tag to describe against.
func TestDevCLIVersionClearsFloors(t *testing.T) {
	path := filepath.Join("..", "..", "..", "Makefile")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := regexp.MustCompile(`DEV_CLI_VERSION\s*:=\s*(\S+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("DEV_CLI_VERSION not found in Makefile")
	}
	devVersion := string(m[1])

	parsed, err := parseSemver(devVersion)
	if err != nil {
		t.Fatalf("DEV_CLI_VERSION %q does not parse as a semver: %v", devVersion, err)
	}

	floors := map[string]string{
		"MinQuickCreateCLIVersion":       MinQuickCreateCLIVersion,
		"MinQuickCreateFieldsCLIVersion": MinQuickCreateFieldsCLIVersion,
		"MinHandoffCLIVersion":           MinHandoffCLIVersion,
		"MinLocalWorktreeCLIVersion":     MinLocalWorktreeCLIVersion,
	}
	for name, floor := range floors {
		min, err := parseSemver(floor)
		if err != nil {
			t.Fatalf("%s %q does not parse as a semver: %v", name, floor, err)
		}
		if parsed.lessThan(min) {
			t.Errorf("DEV_CLI_VERSION %q is below %s (%q); bump DEV_CLI_VERSION in the Makefile", devVersion, name, floor)
		}
	}
}
