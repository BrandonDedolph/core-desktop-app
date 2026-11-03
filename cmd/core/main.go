// Command core is the unified host-side CLI for the iPod Video firmware
// project. One binary handles install, update, recovery, dev iteration,
// the simulator, and the test suite.
//
// See docs/design/companion-app-plan.md for the design; see README.md for usage.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BrandonDedolph/core-desktop-app/internal/cli"
	"github.com/BrandonDedolph/core-desktop-app/internal/selfupdate"
)

func main() {
	// What an earlier self-update renamed out of the way (core.old.exe,
	// core-app.old.exe) is no longer running by now, so it can go.
	if exe, err := selfupdate.Executable(); err == nil {
		selfupdate.CleanOld(filepath.Dir(exe))
	}
	// Build the root first and report through ITS error writer rather
	// than os.Stderr directly: --elevated-log retargets that writer, and
	// the elevated child's console closes the instant it exits, so an
	// error printed to the real stderr is an error nobody ever sees.
	root := cli.Root()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(root.ErrOrStderr(), "error:", err)
		// `core flash` on Windows does its work in an elevated child
		// process. That child's exit code has to reach the shell that
		// ran the parent, or a script cannot tell a flash that failed
		// inside the UAC window from one that succeeded.
		var exit *cli.ExitError
		if errors.As(err, &exit) && exit.Code != 0 {
			os.Exit(exit.Code)
		}
		os.Exit(1)
	}
}
