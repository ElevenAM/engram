package main

import (
	"strings"
	"testing"

	versioncheck "github.com/Gentleman-Programming/engram/internal/version"
)

// The fork build must never relay upstream's "brew upgrade" advice — doing so
// would replace the /opt/homebrew/bin/engram symlink with a stock build and
// silently drop semantic search.
func TestForkBuildSuppressesBrewUpgradeAdvice(t *testing.T) {
	oldVersion := version
	version = "1.17.0-semantic.2"
	t.Cleanup(func() { version = oldVersion })

	_, stderr := captureOutput(t, func() {
		printUpdateCheckResult(versioncheck.CheckResult{
			Status:  versioncheck.StatusUpdateAvailable,
			Message: "Update available: 1.17.0 -> 1.18.0. Run `brew upgrade engram` to update.",
		})
	})

	if strings.Contains(stderr, "brew upgrade engram` to update") {
		t.Errorf("fork build relayed upstream upgrade advice:\n%s", stderr)
	}
	if !strings.Contains(stderr, "DO NOT run `brew upgrade engram`") {
		t.Errorf("fork build missing the do-not-upgrade warning:\n%s", stderr)
	}
	if !strings.Contains(stderr, "rebuild") {
		t.Errorf("fork warning should explain the rebuild path:\n%s", stderr)
	}
}

func TestNonForkBuildKeepsUpstreamAdvice(t *testing.T) {
	oldVersion := version
	version = "1.17.0"
	t.Cleanup(func() { version = oldVersion })

	_, stderr := captureOutput(t, func() {
		printUpdateCheckResult(versioncheck.CheckResult{
			Status:  versioncheck.StatusUpdateAvailable,
			Message: "Update available: 1.17.0 -> 1.18.0",
		})
	})

	if !strings.Contains(stderr, "Update available") {
		t.Errorf("stock build should relay the upstream message:\n%s", stderr)
	}
}

func TestForkBuildStaysQuietWhenUpToDate(t *testing.T) {
	oldVersion := version
	version = "1.17.0-semantic.2"
	t.Cleanup(func() { version = oldVersion })

	_, stderr := captureOutput(t, func() {
		printUpdateCheckResult(versioncheck.CheckResult{Status: versioncheck.StatusUpToDate})
	})
	if stderr != "" {
		t.Errorf("stderr = %q, want empty when up to date", stderr)
	}
}
