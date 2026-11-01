// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// repoRoot locates the firmware tree: $CORE_REPO, a separate checkout of
// core-firmware. Returns "" when it is unset — every parity test here SKIPS
// in that case rather than failing, so the package tests clean on its own
// (plan §2, cross-cutting rules). A CORE_REPO that is not that tree fails.
func repoRoot(t *testing.T) string {
	t.Helper()
	if r := os.Getenv("CORE_REPO"); r != "" {
		if isRepo(r) {
			return r
		}
		t.Fatalf("CORE_REPO=%s does not look like the core-firmware tree", r)
	}
	return ""
}

func isRepo(dir string) bool {
	for _, f := range []string{
		filepath.Join(dir, "core", "kernel", "config.c"),
		filepath.Join(dir, "core", "ui", "settings.c"),
		filepath.Join(dir, "tools", "make_config.py"),
	} {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

// python3 returns the interpreter to shell out to, or "" when there is none.
func python3(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		return ""
	}
	return p
}
