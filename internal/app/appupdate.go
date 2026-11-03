package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BrandonDedolph/core-desktop-app/internal/ghrelease"
	"github.com/BrandonDedolph/core-desktop-app/internal/selfupdate"
)

// The app updates itself (library plan L6): ··· → Update the app to vX.Y.Z
// asks once, then downloads this computer's core-app and core from the
// release, checks each against the .sha256 beside it, swaps them in
// (internal/selfupdate: on Windows the running files become *.old.exe,
// which the next start deletes) and starts the new app. A failure leaves
// both programs as they were and the log says why.

// appUpdateLabel is the menu item: the action and the release it installs.
func appUpdateLabel(tag string) string { return "Update the app to " + tag }

// appUpdateQuestion is the one Yes/Cancel dialog's body.
func appUpdateQuestion(tag string) string {
	return "It downloads " + tag + ", replaces this program and core.exe beside it, and restarts."
}

// startAppUpdate is the menu item's job.
func (u *UI) startAppUpdate() {
	tag := u.st.NewerApp
	if tag == "" {
		return
	}
	u.start(JobAppUpdate, func(ctx context.Context, emit func(Event)) error {
		answer, err := u.ask(ctx, &dialogRequest{
			title:   "Update the app?",
			body:    appUpdateQuestion(tag),
			prose:   true,
			okLabel: "Yes",
		})
		if err != nil {
			return err
		}
		if answer == CancelAnswer {
			emit(Event{Kind: EventLog, Text: "app update: cancelled; nothing was downloaded"})
			return nil
		}
		exe, err := u.be.UpdateApp(ctx, tag, emit)
		if err != nil {
			return fmt.Errorf("%w — the app was left as it was", err)
		}
		emit(Event{Kind: EventLog, Text: "updated to " + tag + "; starting it"})
		return u.be.RelaunchApp(exe)
	})
}

// UpdateApp downloads, verifies and swaps in the app release tag: this
// program and the core beside it. It returns the program's path, read
// before the swap, for RelaunchApp.
func (b *RealBackend) UpdateApp(ctx context.Context, tag string, emit func(Event)) (string, error) {
	repo := b.AppRepo
	if repo == "" {
		repo = ghrelease.AppRepo
	}
	goos := b.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	exe, err := selfupdate.Executable()
	if err != nil {
		return "", fmt.Errorf("finding this program's own file: %w", err)
	}
	c := b.releaseClient()
	rel, err := c.ByTag(ctx, repo, tag)
	if err != nil {
		return "", err
	}
	targets := selfupdate.Targets(goos, exe, selfupdate.App)
	last := -1
	upd := &selfupdate.Updater{
		Client: c, GOOS: goos,
		Log: func(s string) { emit(Event{Kind: EventLog, Text: s}) },
		Progress: func(name string, done, total int64) {
			if total <= 0 {
				return
			}
			pct := int(done * 100 / total)
			if pct == last {
				return
			}
			last = pct
			emit(Event{Kind: EventProgress, Text: "downloading " + name, Pct: float32(done) / float32(total)})
		},
	}
	fetched, err := upd.Fetch(ctx, rel, targets)
	if err != nil {
		if errors.Is(err, selfupdate.ErrNotWritable) && rel.URL != "" {
			err = fmt.Errorf("%w; the release is at %s", err, rel.URL)
		}
		return "", err
	}
	emit(Event{Kind: EventProgress, Text: "replacing the programs in " + filepath.Dir(exe), Pct: -1})
	if err := selfupdate.SwapAll(goos, fetched); err != nil {
		return "", err
	}
	return exe, nil
}

// RelaunchApp starts the new program with this one's arguments and ends
// this process.
func (b *RealBackend) RelaunchApp(exe string) error {
	return selfupdate.Relaunch(exe, os.Args[1:])
}
