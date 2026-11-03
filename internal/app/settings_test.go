package app

import (
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/unit"
)

// readKeys is config.json as its top-level keys, for asserting what was
// and was not written.
func readKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

// Rule 1 of the settings plan: absent means default, and a default is not
// written. An untouched Config saves as {}, and {} loads as every default.
func TestConfigDefaultsAreNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) != "{}" {
		t.Errorf("an untouched config wrote %s", b)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !on(c.FixNames) || !on(c.FixOrganize) || !on(c.FixLowRes) || c.DiscFolders {
		t.Errorf("the ticks' defaults: %+v", c)
	}
	if w, h := c.WindowDp(); w != DefaultWindowW || h != DefaultWindowH {
		t.Errorf("window default %dx%d", w, h)
	}
	if !on(c.WindowRemember) || !on(c.LastTabRemember) || tabFromKey(c.LastTab) != TabAlbums {
		t.Errorf("remember defaults: %+v", c)
	}

	// The new keys round-trip, under the names the plan's table gives.
	f := false
	want := Config{DiscFolders: true, FixNames: &f, FixOrganize: &f, FixLowRes: &f,
		Window: &WindowSize{W: 1200, H: 800}, WindowRemember: nil, LastTab: "fix", LastTabRemember: &f}
	if err := SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	keys := readKeys(t, path)
	for _, k := range []string{"disc_folders", "fix_names", "fix_organize", "fix_lowres", "window", "last_tab",
		"last_tab_remember"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("%s was not written: %v", k, keys)
		}
	}
	if string(keys["window"]) != `{"w":1200,"h":800}` && !strings.Contains(string(keys["window"]), `"w": 1200`) {
		t.Errorf("window = %s", keys["window"])
	}
	got, _ := LoadConfig(path)
	if !got.DiscFolders || on(got.FixNames) || got.Window == nil || got.Window.W != 1200 || got.LastTab != "fix" {
		t.Errorf("round trip: %+v", got)
	}
}

// The ticks are remembered: a change saves, and the next window starts
// with them — the Fix tab's widgets and the model alike.
func TestTheTicksPersistAcrossNewUI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	u := NewUI(Options{Backend: &fakeBackend{}, ConfigPath: path})
	if !u.st.FixNames || !u.st.Organize || !u.st.FixLowRes || u.st.DiscFolders {
		t.Fatalf("first-run ticks: %+v", u.st)
	}
	u.namesTick.Value, u.lowResTick.Value, u.discTick.Value = false, false, true
	u.ticksChanged(true, true) // busy: no rescan
	keys := readKeys(t, path)
	if _, ok := keys["fix_organize"]; ok {
		t.Error("an untouched tick was written")
	}
	if string(keys["fix_names"]) != "false" || string(keys["disc_folders"]) != "true" {
		t.Errorf("saved %v", keys)
	}

	cfg, _ := LoadConfig(path)
	v := NewUI(Options{Config: cfg})
	if v.st.FixNames || !v.st.Organize || v.st.FixLowRes || !v.st.DiscFolders {
		t.Errorf("the next window's model: names %v organize %v lowres %v discs %v",
			v.st.FixNames, v.st.Organize, v.st.FixLowRes, v.st.DiscFolders)
	}
	if v.namesTick.Value || !v.organizeTick.Value || v.lowResTick.Value || !v.discTick.Value {
		t.Error("the next window's tick boxes do not match its model")
	}

	// Back on is back to no key at all.
	u.namesTick.Value, u.lowResTick.Value, u.discTick.Value = true, true, false
	u.ticksChanged(true, true)
	if keys := readKeys(t, path); len(keys) != 0 {
		t.Errorf("every tick back at its default still wrote %v", keys)
	}
}

// liveFake is a backend whose settings can change under it, recording
// what Settings told it.
type liveFake struct {
	fakeBackend
	minArt    []int
	backupDir []string
	online    []bool
	dups      []bool
	pre       []bool
	keep      []int
	artists   []bool
}

func (f *liveFake) SetMinArtPx(px int)      { f.minArt = append(f.minArt, px) }
func (f *liveFake) SetBackupDir(dir string) { f.backupDir = append(f.backupDir, dir) }
func (f *liveFake) SetOnlineArt(v bool)     { f.online = append(f.online, v) }
func (f *liveFake) SetCheckDuplicates(v bool) {
	f.dups = append(f.dups, v)
}
func (f *liveFake) SetPrereleases(v bool)    { f.pre = append(f.pre, v) }
func (f *liveFake) SetArtistPictures(v bool) { f.artists = append(f.artists, v) }
func (f *liveFake) SetKeepBackups(n int)     { f.keep = append(f.keep, n) }

// The small-covers picker's four stops are the four min_art_px values —
// 600, the default, is no key — the backend hears each at once, and a
// change queues one rescan of the music folder.
func TestTheMinArtPicker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	f := &liveFake{}
	u := NewUI(Options{Backend: f, ConfigPath: path})
	u.st.Source = "/src"
	want := map[string]string{"Off": "-1", "300 px": "300", "600 px": "", "1000 px": "1000"}
	for i, c := range minArtChoices {
		u.setMinArtPx(c.px)
		got := string(readKeys(t, path)["min_art_px"])
		if got != want[c.label] {
			t.Errorf("%s wrote min_art_px %q, want %q", c.label, got, want[c.label])
		}
		if minArtIndex(u.cfg.MinArtPx) != i {
			t.Errorf("%s: the picker shows stop %d", c.label, minArtIndex(u.cfg.MinArtPx))
		}
	}
	if len(f.minArt) != 4 || f.minArt[0] != -1 || f.minArt[2] != 0 || f.minArt[3] != 1000 {
		t.Errorf("the backend heard %v", f.minArt)
	}
	if len(u.pending) != 1 {
		t.Errorf("%d rescans queued, want one", len(u.pending))
	}
	if minArtIndex(600) != 2 || minArtIndex(800) != -1 || !strings.Contains(func() string {
		u.cfg.MinArtPx = 800
		return u.minArtNote()
	}(), "800") {
		t.Error("a hand-edited value is not shown as itself")
	}
}

// The backup folder: the backend is re-pointed live, the key is written,
// and clearing it goes back to the default with no key.
func TestTheBackupFolderIsLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	f := &liveFake{}
	u := NewUI(Options{Backend: f, ConfigPath: path})
	u.setBackupDir(`D:\core backups`)
	if len(f.backupDir) != 1 || f.backupDir[0] != `D:\core backups` {
		t.Errorf("the backend heard %v", f.backupDir)
	}
	if got := string(readKeys(t, path)["backup_dir"]); !strings.Contains(got, "core backups") {
		t.Errorf("backup_dir = %s", got)
	}
	if u.backupDirShown() != `D:\core backups` {
		t.Errorf("the row shows %q", u.backupDirShown())
	}
	u.setBackupDir("")
	if _, ok := readKeys(t, path)["backup_dir"]; ok {
		t.Error("the default backup folder was written")
	}
}

// RealBackend's setters change what its next job reads.
func TestRealBackendSetters(t *testing.T) {
	b := NewRealBackend("")
	b.SetBackupDir("/x/backups")
	b.SetMinArtPx(1000)
	if b.backupDir() != "/x/backups" || b.minArtPx() != 1000 {
		t.Errorf("backupDir %q minArt %d", b.backupDir(), b.minArtPx())
	}
	var _ liveSettings = b
}

// A saved window size under the minimum is ignored, as is one with
// "Remember the window size" off.
func TestWindowSizeBelowMinimumIsIgnored(t *testing.T) {
	for _, c := range []struct {
		cfg  Config
		w, h int
	}{
		{Config{Window: &WindowSize{W: 1200, H: 800}}, 1200, 800},
		{Config{Window: &WindowSize{W: MinWidth - 1, H: 800}}, DefaultWindowW, DefaultWindowH},
		{Config{Window: &WindowSize{W: 1200, H: MinHeight - 1}}, DefaultWindowW, DefaultWindowH},
		{Config{Window: &WindowSize{}}, DefaultWindowW, DefaultWindowH},
		{Config{Window: &WindowSize{W: 1200, H: 800}, WindowRemember: onPtr(false)}, DefaultWindowW, DefaultWindowH},
	} {
		if w, h := c.cfg.WindowDp(); w != c.w || h != c.h {
			t.Errorf("%+v opens at %dx%d, want %dx%d", c.cfg.Window, w, h, c.w, c.h)
		}
	}
}

// A resize is saved after the quiet, in dp; the opening size, a size
// below the minimum and a maximized window are not; back at 980×700 is
// no key.
func TestWindowSizeIsSavedDebounced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	u := NewUI(Options{Backend: &fakeBackend{}, ConfigPath: path})
	now := time.Now()
	u.now = func() time.Time { return now }
	m := unit.Metric{PxPerDp: 1.5, PxPerSp: 1.5}
	frame := func() { u.Layout(newTestContext(900, 600)) }

	u.WindowResized(image.Pt(980*3/2, 700*3/2), m, true) // the opening size
	u.WindowResized(image.Pt(600, 600), m, true)         // 400 dp: below the minimum
	u.WindowResized(image.Pt(3000, 2000), m, false)      // maximized
	now = now.Add(time.Second)
	frame()
	if _, err := os.Stat(path); err == nil {
		if _, ok := readKeys(t, path)["window"]; ok {
			t.Fatal("a size nobody chose was saved")
		}
	}

	u.WindowResized(image.Pt(1800, 1200), m, true)
	frame()
	if _, err := os.Stat(path); err == nil {
		if _, ok := readKeys(t, path)["window"]; ok {
			t.Fatal("saved before the quiet")
		}
	}
	now = now.Add(volSaveAfter)
	frame()
	cfg, _ := LoadConfig(path)
	if cfg.Window == nil || cfg.Window.W != 1200 || cfg.Window.H != 800 {
		t.Fatalf("saved %+v, want 1200x800 dp", cfg.Window)
	}

	// Remember off forgets it and stops saving.
	u.setWindowRemember(false)
	u.WindowResized(image.Pt(1500, 1200), m, true)
	now = now.Add(time.Second)
	frame()
	keys := readKeys(t, path)
	if _, ok := keys["window"]; ok || string(keys["window_remember"]) != "false" {
		t.Errorf("remember off: %v", keys)
	}
}

// The last tab: restored at NewUI when remembered, saved when it changes,
// and forgotten when "Reopen the last tab" is off. A canned state beats it.
func TestTheLastTab(t *testing.T) {
	u := NewUI(Options{Config: Config{LastTab: "summary"}})
	if u.st.Tab != TabDetails {
		t.Errorf("reopened on %v", u.st.Tab)
	}
	u = NewUI(Options{Config: Config{LastTab: "summary", LastTabRemember: onPtr(false)}})
	if u.st.Tab != TabAlbums {
		t.Errorf("not remembering, reopened on %v", u.st.Tab)
	}
	u = NewUI(Options{Config: Config{LastTab: "summary"}})
	u.SetState(DemoState())
	if u.st.Tab != DemoState().Tab {
		t.Error("the canned state did not beat the saved tab")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	u = NewUI(Options{Backend: &fakeBackend{}, ConfigPath: path})
	u.st.Tab = TabFix
	u.Layout(newTestContext(900, 600))
	if got := string(readKeys(t, path)["last_tab"]); got != `"fix"` {
		t.Errorf("last_tab = %s", got)
	}
	u.st.Tab = TabAlbums
	u.Layout(newTestContext(900, 600))
	if _, ok := readKeys(t, path)["last_tab"]; ok {
		t.Error("Music, the default, was written")
	}
	u.st.Tab = TabDetails
	u.setTabRemember(false)
	keys := readKeys(t, path)
	if _, ok := keys["last_tab"]; ok || string(keys["last_tab_remember"]) != "false" {
		t.Errorf("remember off: %v", keys)
	}
	u.st.Tab = TabFix
	u.Layout(newTestContext(900, 600))
	if _, ok := readKeys(t, path)["last_tab"]; ok {
		t.Error("a tab was saved with remembering off")
	}
}

// Restore defaults asks once and, on a yes, leaves only the names and the
// music folder in the file and the live window at its defaults.
func TestRestoreDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	f := &liveFake{}
	vol := 40
	cfg := Config{Source: "/src", Names: map[string]string{"S1": "Pod"}, Theme: ThemeOnyx, MinArtPx: 1000,
		BackupDir: "/b", Volume: &vol, ReplayGain: onPtr(false), DiscFolders: true, FixNames: onPtr(false),
		Window: &WindowSize{W: 1000, H: 800}, LastTab: "fix"}
	u := NewUI(Options{Backend: f, Config: cfg, ConfigPath: path, Audio: &fakeAudio{}})
	u.openSettings()
	u.askRestore()
	if u.dlg == nil {
		t.Fatal("Restore defaults did not ask")
	}
	u.cancel()
	if u.cfg.Theme != ThemeOnyx {
		t.Fatal("Cancel restored")
	}
	u.askRestore()
	u.ok()
	keys := readKeys(t, path)
	if len(keys) != 2 || keys["names"] == nil || keys["source"] == nil {
		t.Errorf("after restoring, the file holds %v", keys)
	}
	if u.theme != ThemeSystem || !u.replayGainOn() || !u.st.FixNames || u.st.DiscFolders || u.vol.level != 1 {
		t.Errorf("the live window was not restored: theme %v rg %v names %v discs %v vol %v",
			u.theme, u.replayGainOn(), u.st.FixNames, u.st.DiscFolders, u.vol.level)
	}
	if len(f.minArt) != 1 || f.minArt[0] != 0 || len(f.backupDir) != 1 || f.backupDir[0] != "" {
		t.Errorf("the backend was not reset: %v %v", f.minArt, f.backupDir)
	}
}

// The menu: Settings… sits in the last group, before About; the theme and
// ReplayGain are no longer in it.
func TestTheMenuOpensSettings(t *testing.T) {
	last := menuGroups[len(menuGroups)-1]
	if last[len(last)-1] != menuAbout || last[len(last)-2] != menuSettings {
		t.Errorf("the last group is %v", last)
	}
	for _, g := range menuGroups {
		if g == nil {
			t.Error("the theme's group is still in the menu")
		}
	}
	for _, l := range menuLabels {
		if strings.Contains(l, "ReplayGain") || strings.Contains(l, "Theme") {
			t.Errorf("menu item %q", l)
		}
	}
	u := NewUI(Options{})
	u.SetState(MenuState())
	u.Layout(newTestContext(900, 600))
	u.menuClicks[menuSettings].Click()
	u.Layout(newTestContext(900, 600))
	if u.settings == nil || u.st.MenuOpen {
		t.Errorf("Settings… did not open the sheet (open %v, menu %v)", u.settings != nil, u.st.MenuOpen)
	}
}

// The sheet lays out in both palettes, at the minimum size and larger,
// without a GPU: the frame is the size it was given.
func TestTheSettingsSheetLaysOut(t *testing.T) {
	for _, th := range []Theme{ThemeLinen, ThemeOnyx} {
		for _, sz := range []image.Point{{MinWidth, MinHeight}, {900, 600}, {1200, 900}} {
			th := th
			u := NewUI(Options{Theme: &th})
			u.SetState(DemoState())
			u.openSettings()
			gtx := newTestContext(sz.X, sz.Y)
			if d := u.Layout(gtx); d.Size != sz {
				t.Errorf("%s %v: the frame is %v", th, sz, d.Size)
			}
		}
	}
}
