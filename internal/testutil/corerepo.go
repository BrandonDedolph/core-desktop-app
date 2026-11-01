// SPDX-License-Identifier: Apache-2.0

// Package testutil holds the one rule every firmware-parity test shares:
// where the core-firmware checkout is, and what happens when it is not there.
//
// A parity test checks a byte layout in this module against the firmware's
// own source (its C, its Python reference tools). It needs $CORE_REPO, a
// checkout of BrandonDedolph/core-firmware. Without it the test SKIPS, so a
// contributor who only cloned this repository still gets a clean run.
//
// A skip is also how parity silently stopped being checked at all: CI never
// set CORE_REPO after the repository split, and every one of these tests
// skipped on every push without anybody noticing. So CI also sets
// CORE_REQUIRE_PARITY=1, and under it every skip routed through this package
// is a FAILURE instead. Only parity prerequisites go through here; a test
// that wants, say, the user's own music library (CORE_PARITY_SRC) keeps its
// plain t.Skip and is not affected.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const (
	// RepoEnv names the core-firmware checkout.
	RepoEnv = "CORE_REPO"
	// RequireEnv, set to anything but "" or "0", turns a parity skip into
	// a failure.
	RequireEnv = "CORE_REQUIRE_PARITY"
)

// RequireParity reports whether CORE_REQUIRE_PARITY is on.
func RequireParity() bool {
	v := os.Getenv(RequireEnv)
	return v != "" && v != "0"
}

// SkipParity skips the test with the given reason — or, under
// CORE_REQUIRE_PARITY, fails it with that reason. Use it for anything a
// parity test cannot run without: the firmware tree, python3, ffprobe, a
// file inside the tree.
func SkipParity(t testing.TB, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if RequireParity() {
		t.Fatalf("%s=1 but this parity test cannot run: %s", RequireEnv, msg)
	}
	t.Skip(msg)
}

// CoreRepo returns $CORE_REPO. Unset: the test skips (or fails, under
// CORE_REQUIRE_PARITY). Set, but missing any of markers (paths relative to
// the tree's root): the test fails, because a CORE_REPO pointing at the
// wrong directory is a mistake to report, not a reason to quietly test less.
func CoreRepo(t testing.TB, markers ...string) string {
	t.Helper()
	repo := os.Getenv(RepoEnv)
	if repo == "" {
		SkipParity(t, "firmware repo not given (set %s=/path/to/core-firmware)", RepoEnv)
		return ""
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(repo, m)); err != nil {
			t.Fatalf("%s=%s does not look like the core-firmware tree: %v", RepoEnv, repo, err)
		}
	}
	return repo
}
