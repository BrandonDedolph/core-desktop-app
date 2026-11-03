package cli

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/ghrelease"
	"github.com/BrandonDedolph/core-desktop-app/internal/selfupdate"
	"github.com/BrandonDedolph/core-desktop-app/internal/version"
	"github.com/spf13/cobra"
)

// `core self-update` is the window's Update the app, typed: the newest app
// release's core and core-app for this computer, downloaded into the
// cache, checked against the .sha256 beside each on the release, and
// swapped in (internal/selfupdate). It replaces this program and the
// core-app beside it; it does not start anything afterwards — the next
// `core` typed is the new one.

// Seams: the running program's own file, read before anything is
// replaced, and the platform the assets are picked for.
var (
	selfExecutable = selfupdate.Executable
	selfGOOS       = runtime.GOOS
	selfGOARCH     = runtime.GOARCH
)

func newSelfUpdateCmd() *cobra.Command {
	var (
		check, yes, prereleases bool
		repo                    string
	)
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Replace core and core-app with the newest app release",
		Long: `Asks GitHub for the newest release of the desktop app and the CLI. When it
is newer than this program, downloads this computer's core and core-app
binaries into the cache, checks each against the <name>.sha256 published
beside it (a binary without one is refused), and replaces this program
and the core-app beside it. On Windows the running files are renamed to
core.old.exe and core-app.old.exe, which the next start deletes.

A download that fails or does not verify leaves both programs as they
were. A folder this user cannot write (Program Files without an
administrator) is refused before anything is downloaded.

--check says what would happen and stops, having downloaded nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prereleases = resolveBoolFlag(cmd, "prereleases", "prereleases", prereleases)
			return runSelfUpdate(cmd, check, yes, prereleases, strings.TrimSpace(repo))
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Say whether a newer app release exists and what it would replace, then stop")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask before replacing the programs")
	cmd.Flags().BoolVar(&prereleases, "prereleases", false,
		"Also consider releases GitHub marks as pre-releases (default: prereleases in core config)")
	cmd.Flags().StringVar(&repo, "repo", ghrelease.AppRepo, "GitHub repository the app's releases come from, as owner/name")
	return cmd
}

func runSelfUpdate(cmd *cobra.Command, check, yes, prereleases bool, repo string) error {
	out := cmd.OutOrStdout()
	exe, err := selfExecutable()
	if err != nil {
		return fmt.Errorf("finding this program's own file: %w", err)
	}
	c := newReleaseClient()
	c.IncludePrereleases = prereleases
	rel, err := c.LatestApp(cmd.Context(), repo)
	if err != nil {
		return fmt.Errorf("looking up the newest app release in %s: %w", repo, err)
	}
	running := version.Version
	fmt.Fprintf(out, "this:   %s · latest: %s\n", running, rel.Tag)
	if rel.URL != "" {
		fmt.Fprintf(out, "page:   %s\n", rel.URL)
	}
	if !ghrelease.AppNewer(rel.Tag, running) {
		if !strings.HasPrefix(running, "v") {
			fmt.Fprintf(out, "\nthis is a development build (%s); self-update only replaces a release build.\n", running)
		} else {
			fmt.Fprintf(out, "\nup to date: nothing to do.\n")
		}
		return nil
	}

	targets := selfupdate.Targets(selfGOOS, exe, selfupdate.CLI)
	var problem error
	for _, t := range targets {
		p, err := selfupdate.PickAsset(rel, t.Prog, selfGOOS, selfGOARCH)
		if err != nil {
			problem = err
			fmt.Fprintf(out, "replace: %s — %v\n", t.Path, err)
			continue
		}
		fmt.Fprintf(out, "replace: %s with %s (%d bytes, checked against %s)\n",
			t.Path, p.Bin.Name, p.Bin.Size, p.Sum.Name)
	}
	if problem == nil {
		for _, t := range targets {
			if err := selfupdate.CheckWritable(filepath.Dir(t.Path)); err != nil {
				problem = err
				fmt.Fprintf(out, "refused: %v\n", err)
				break
			}
		}
	}
	if check {
		fmt.Fprintf(out, "\n--check: nothing was downloaded and nothing was replaced.\n")
		return nil
	}
	if problem != nil {
		if errors.Is(problem, selfupdate.ErrNotWritable) && rel.URL != "" {
			return fmt.Errorf("%w\nthe release is at %s", problem, rel.URL)
		}
		return problem
	}

	if !yes {
		fmt.Fprintf(out, "\nReplace %s with %s? [y/N] ", joinPaths(targets), rel.Tag)
		line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintf(out, "nothing was replaced.\n")
			return nil
		}
	}

	u := &selfupdate.Updater{
		Client: c, GOOS: selfGOOS, GOARCH: selfGOARCH,
		Log: func(s string) { fmt.Fprintln(out, s) },
	}
	fetched, err := u.Fetch(cmd.Context(), rel, targets)
	if err != nil {
		return fmt.Errorf("%w\nnothing was replaced", err)
	}
	if err := selfupdate.SwapAll(selfGOOS, fetched); err != nil {
		return fmt.Errorf("%w\nthe programs were left as they were", err)
	}
	fmt.Fprintf(out, "\nupdated to %s: %s\n", rel.Tag, joinPaths(targets))
	if selfGOOS == "windows" {
		fmt.Fprintf(out, "the old files are now *.old.exe beside them; the next start deletes them.\n")
	}
	return nil
}

func joinPaths(ts []selfupdate.Target) string {
	var ps []string
	for _, t := range ts {
		ps = append(ps, t.Path)
	}
	return strings.Join(ps, " and ")
}
