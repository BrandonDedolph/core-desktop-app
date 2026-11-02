package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/library"
	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// Slice C3's keys follow rule 1: absent is the default, a default is not
// written, and each one is spelled the way `core config` spells it.
func TestC3KeysRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.OnlineArtOn() || !c.CheckDuplicatesOn() || c.VerifySync || c.EjectAfterSync || c.Prereleases ||
		c.KeepBackups != 0 || c.UpdateCheck != "" || c.PlaylistsDir != "" || c.GenreMap != "" || c.LogFile != "" {
		t.Errorf("{} does not load as the defaults: %+v", c)
	}

	// Default-on keys switched back on are no key at all.
	if err := SaveConfig(path, Config{OnlineArt: onPtr(true), CheckDuplicates: onPtr(true)}); err != nil {
		t.Fatal(err)
	}
	if keys := readKeys(t, path); len(keys) != 0 {
		t.Errorf("defaults were written: %v", keys)
	}

	want := Config{PlaylistsDir: "/p", OnlineArt: onPtr(false), CheckDuplicates: onPtr(false), VerifySync: true,
		EjectAfterSync: true, GenreMap: "/g.json", UpdateCheck: UpdateCheckNever, Prereleases: true,
		KeepBackups: 3, LogFile: "/l/core-app.log"}
	if err := SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	keys := readKeys(t, path)
	for k, v := range map[string]string{
		"playlists_dir": `"/p"`, "online_art": "false", "check_duplicates": "false", "verify_sync": "true",
		"eject_after_sync": "true", "genre_map": `"/g.json"`, "update_check": `"never"`, "prereleases": "true",
		"keep_backups": "3", "log_file": `"/l/core-app.log"`,
	} {
		if string(keys[k]) != v {
			t.Errorf("%s = %s, want %s", k, keys[k], v)
		}
	}
	got, _ := LoadConfig(path)
	if got.OnlineArtOn() || got.CheckDuplicatesOn() || got.KeepBackups != 3 || got.UpdateCheck != UpdateCheckNever {
		t.Errorf("round trip: %+v", got)
	}
}

// The Sync section reaches the syncer: Verify, the playlists folder and a
// genre map that loads — on the plain sync and on the preview's.
func TestSyncSettingsReachTheSyncer(t *testing.T) {
	gm := filepath.Join(t.TempDir(), "genres.json")
	if err := os.WriteFile(gm, []byte(`{"Band": "Rock"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeBackend{}
	u := newTestUI(f)
	u.cfg.VerifySync, u.cfg.PlaylistsDir, u.cfg.GenreMap = true, "/lists", gm

	u.startSync(JobSync)
	settle(u)
	u.startSync(JobDryRun)
	settle(u)
	if len(f.syncCalls) != 2 {
		t.Fatalf("%d sync calls", len(f.syncCalls))
	}
	for i, o := range f.syncCalls {
		if !o.Verify || o.Playlists != "/lists" || o.GenreMap != gm {
			t.Errorf("call %d: Verify %v Playlists %q GenreMap %q", i, o.Verify, o.Playlists, o.GenreMap)
		}
	}
}

// A genre map that does not load is a log line and the built-in map: the
// map fills in genres, and a broken one must never stop a sync.
func TestABadGenreMapFallsBackToTheBuiltIn(t *testing.T) {
	f := &fakeBackend{}
	u := newTestUI(f)
	u.cfg.GenreMap = filepath.Join(t.TempDir(), "missing.json")
	u.startSync(JobSync)
	settle(u)
	if len(f.syncCalls) != 1 || f.syncCalls[0].GenreMap != library.GenreMapEmbedded {
		t.Fatalf("sync calls = %+v", f.syncCalls)
	}
	if !strings.Contains(strings.Join(u.st.Log, "\n"), "genre map:") {
		t.Error("the fallback was not said in the log")
	}
}

// Eject when a sync finishes: after a sync that copied with no error, the
// Eject job runs (the button's own path); after a failed one, after a
// preview closed with Cancel, and with the setting off, it does not.
func TestEjectAfterSync(t *testing.T) {
	copyPlan := func() *syncer.Plan {
		return &syncer.Plan{Copy: []syncer.FileOp{{Album: "A - B", Src: "/src/B - A/01.flac", Size: 100}}}
	}
	for _, tc := range []struct {
		name  string
		on    bool
		err   error
		run   func(u *UI)
		eject bool
	}{
		{"sync", true, nil, func(u *UI) { u.startSync(JobSync) }, true},
		{"setting off", false, nil, func(u *UI) { u.startSync(JobSync) }, false},
		{"failed sync", true, errors.New("disk full"), func(u *UI) { u.startSync(JobSync) }, false},
		{"preview OK", true, nil, func(u *UI) {
			u.startSyncPreview()
			frameUntilDialog(t, u)
			clickThrough(u, &u.dlgOK)
		}, true},
		{"preview cancelled", true, nil, func(u *UI) {
			u.startSyncPreview()
			frameUntilDialog(t, u)
			u.cancel()
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The refresh that follows a sync reads the iPod again; it is
			// still there, so the eject queued after it has a volume.
			f := &fakeBackend{plans: []*syncer.Plan{copyPlan(), copyPlan()}, syncErr: tc.err,
				dev: Device{Found: true, Path: `\\.\PhysicalDrive2`, Volume: `D:\`, Tested: true}}
			u := newTestUI(f)
			u.cfg.EjectAfterSync = tc.on
			tc.run(u)
			settleWindow(u)
			if got := len(f.ejectCalls) > 0; got != tc.eject {
				t.Errorf("ejected = %v (%v), want %v", got, f.ejectCalls, tc.eject)
			}
		})
	}
}

// Check for updates: Never — no check by itself when an iPod connects; the
// default still asks once.
func TestUpdateCheckNever(t *testing.T) {
	for _, tc := range []struct {
		setting string
		calls   int
	}{{"", 1}, {UpdateCheckNever, 0}} {
		f := &fakeBackend{
			dev: Device{Found: true, Path: `\\.\PhysicalDrive2`, Volume: `D:\`},
			rel: Release{Checked: true, Tag: "v0.2.1"},
		}
		u := newTestUI(f)
		u.cfg.UpdateCheck = tc.setting
		u.startRefresh()
		settleWindow(u)
		f.mu.Lock()
		n := f.releaseCalls
		f.mu.Unlock()
		if n != tc.calls {
			t.Errorf("update_check %q: %d release check(s), want %d", tc.setting, n, tc.calls)
		}
	}
}

// The C3 rows save at once and tell the backend; the duplicate tick
// rescans; Restore defaults puts all of it back.
func TestC3RowsAreLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	f := &liveFake{}
	u := NewUI(Options{Backend: f, ConfigPath: path})
	u.st.Source = "/src"
	u.openSettings()

	u.setOnlineArt(false)
	u.setCheckDuplicates(false)
	u.setPrereleases(true)
	u.setKeepBackups(3)
	keys := readKeys(t, path)
	for k, v := range map[string]string{"online_art": "false", "check_duplicates": "false",
		"prereleases": "true", "keep_backups": "3"} {
		if string(keys[k]) != v {
			t.Errorf("%s = %s, want %s", k, keys[k], v)
		}
	}
	if len(f.online) != 1 || f.online[0] || len(f.dups) != 1 || f.dups[0] ||
		len(f.pre) != 1 || !f.pre[0] || len(f.keep) != 1 || f.keep[0] != 3 {
		t.Errorf("the backend heard online %v dups %v pre %v keep %v", f.online, f.dups, f.pre, f.keep)
	}
	if !u.rescanQueued {
		t.Error("turning the duplicate check off did not rescan")
	}
	if !strings.Contains(u.catSub(CatArt), "album folder only") {
		t.Errorf("Missing cover art still says %q with the internet off", u.catSub(CatArt))
	}

	u.applyPicked(pickedPath{pickLogFolder, "/logs"})
	if u.cfg.LogFile != filepath.Join("/logs", logFileName) || u.settings.logEd.Text() != u.cfg.LogFile {
		t.Errorf("log file = %q, field %q", u.cfg.LogFile, u.settings.logEd.Text())
	}

	u.restoreDefaults()
	if keys := readKeys(t, path); len(keys) != 1 || keys["source"] == nil {
		t.Errorf("after restoring: %v", keys)
	}
	if !f.online[len(f.online)-1] || !f.dups[len(f.dups)-1] || f.pre[len(f.pre)-1] || f.keep[len(f.keep)-1] != 0 {
		t.Errorf("the backend was not reset: online %v dups %v pre %v keep %v", f.online, f.dups, f.pre, f.keep)
	}
	if u.settings.logEd.Text() != "" || !u.settings.onlineTick.Value {
		t.Error("the sheet's own widgets were not reset")
	}
}

// Look for covers on the internet, off, at the backend: no art client (an
// untyped nil, which the librarian reads as folder covers only), and a
// candidate's thumbnail is not fetched.
func TestRealBackendOffline(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	b := &RealBackend{HTTP: srv.Client()}
	if b.artClient() == nil {
		t.Fatal("online: no art client")
	}
	b.SetOnlineArt(false)
	if c := b.artClient(); c != nil {
		t.Errorf("offline: art client %#v, want nil", c)
	}
	if _, err := b.FetchThumb(context.Background(), srv.URL+"/thumb.jpg"); !errors.Is(err, ErrNoThumb) {
		t.Errorf("offline FetchThumb = %v, want ErrNoThumb", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d request(s) with the internet off", n)
	}
}
