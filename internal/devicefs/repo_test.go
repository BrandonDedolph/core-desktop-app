// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/testutil"
)

// repoRoot locates the firmware tree through testutil.CoreRepo: $CORE_REPO,
// a separate checkout of core-firmware. Unset, it skips the test (fails under
// CORE_REQUIRE_PARITY=1); a CORE_REPO that is not that tree fails. Either
// way the caller never sees an empty path.
func repoRoot(t *testing.T) string {
	t.Helper()
	return testutil.CoreRepo(t,
		filepath.Join("core", "kernel", "config.c"),
		filepath.Join("core", "ui", "settings.c"),
		filepath.Join("tools", "make_config.py"))
}

// python3 returns the interpreter to shell out to. Without one the parity
// test skips, or fails under CORE_REQUIRE_PARITY=1.
func python3(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		testutil.SkipParity(t, "python3 not on PATH")
	}
	return p
}
