package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/ghrelease"
)

// online_art false: `fix --art` is a request to search the internet, and
// the file says not to — it refuses, naming the key, and searches nothing.
func TestFixArtRefusesWithOnlineArtOff(t *testing.T) {
	useConfig(t, `{"online_art": false}`)
	_, _, err := runCore(t, "fix", "--art", "--src", t.TempDir(), "--journal-dir", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "online_art") {
		t.Fatalf("fix --art with online_art off = %v; want a refusal naming online_art", err)
	}
	// Names and folders do not need the internet and still run.
	if _, _, err := runCore(t, "fix", "--names", "--src", t.TempDir(), "--journal-dir", t.TempDir()); err != nil {
		t.Errorf("fix --names with online_art off: %v", err)
	}
}

// keep_backups reaches the flash `core update` runs, is said once on
// stderr, and goes to the elevated child as a flag (the child may read
// another account's config.json).
func TestKeepBackupsFromConfig(t *testing.T) {
	useConfig(t, `{"keep_backups": 3}`)
	g := newFakeGitHub(t)
	g.publish(releaseV013(g, ipodFile(t, plausibleImage(8192))))
	stubDeviceVersion(t, "v0.1.2", nil)
	cache := isolateCache(t)
	flash := captureFlash(t)

	_, stderr, err := runCore(t, "update", "--yes", "--repo", "owner/name", "--backup-dir", cache)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if flash.Opts.KeepBackups != 3 {
		t.Errorf("flasher KeepBackups = %d, want 3", flash.Opts.KeepBackups)
	}
	if !strings.Contains(strings.Join(flash.Deps.ChildArgs, " "), "--keep-backups 3") {
		t.Errorf("the child is not told: %v", flash.Deps.ChildArgs)
	}
	if strings.Count(stderr, "keep_backups: 3 (from core config)") != 1 {
		t.Errorf("stderr = %q", stderr)
	}

	// The child's flag beats the file it may read over there.
	cmd := findCmd(t, "flash")
	if err := cmd.Flags().Set(keepBackupsFlag, "10"); err != nil {
		t.Fatal(err)
	}
	if got := resolveKeepBackups(cmd, 10); got != 10 {
		t.Errorf("--keep-backups 10 = %d", got)
	}
	if f := findCmd(t, "install").Flags().Lookup(keepBackupsFlag); f == nil || !f.Hidden {
		t.Error("install has no hidden --keep-backups for its child")
	}
	o := installOptions{KeepBackups: 3, BackupDir: "/b"}
	if args := strings.Join(installChildArgs(findCmd(t, "install"), o, "/img"), " "); !strings.Contains(args, "--keep-backups 3") {
		t.Errorf("install's child is not told: %s", args)
	}
}

// --prereleases (or prereleases in the file) lets the latest be a
// pre-release; without either it is the newest full release.
func TestUpdatePrereleases(t *testing.T) {
	g := newFakeGitHub(t)
	g.publish(releaseV013(g, ipodFile(t, plausibleImage(4096))))
	rc := releaseV013(g, ipodFile(t, plausibleImage(4096)))
	rc.Tag, rc.Prerelease, rc.Published = "v0.1.4-rc1", true, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rc.Assets = []ghrelease.Asset{g.asset("core-v0.1.4-rc1.ipod", ipodFile(t, plausibleImage(4096)))}
	g.publish(rc)
	stubDeviceVersion(t, "v0.1.2", nil)
	isolateCache(t)

	for _, tc := range []struct {
		config string
		args   []string
		want   string
	}{
		{"", nil, "latest: v0.1.3"},
		{"", []string{"--prereleases"}, "latest: v0.1.4-rc1"},
		{`{"prereleases": true}`, nil, "latest: v0.1.4-rc1"},
		{`{"prereleases": true}`, []string{"--prereleases=false"}, "latest: v0.1.3"},
	} {
		useConfig(t, tc.config)
		out, _, err := runCore(t, append([]string{"update", "--check", "--repo", "owner/name"}, tc.args...)...)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Errorf("config %q args %v: %v\n%s\nwant %q", tc.config, tc.args, err, out, tc.want)
		}
	}
}
