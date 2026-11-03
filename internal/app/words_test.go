package app

import (
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
)

// forbidden are the words the front of the window no longer uses: the old
// button names, instructions to press things that are gone, and the
// register-level facts that belong to the Details card and the log.
var forbidden = []string{
	"Dry run", "dry run", "prune", "Prune", "Flash file", "Scan again", "press Refresh",
	"Music card", "PhysicalDrive", "OSOS", "CORECFG", "core fix --dry-run",
}

// frontStrings is every string a canned state puts on the front of the
// window: the header (both lines, the primary, the title), the tab names and
// the tab row's note, the menu, the Settings sheet, the chooser, the job names, the Install screen's text and
// buttons, the Library preview's overflow line, and the sync preview.
func frontStrings(u *UI) []string {
	name, model := u.headName()
	status, _ := u.statusLine()
	out := []string{name, model, status, u.st.PrimaryLabel(), u.Title(), u.st.StatusLine(),
		u.tabNote(), u.appleBackupPromise(), more(3)}
	for _, t := range []Tab{TabAlbums, TabLibrary, TabDetails} {
		out = append(out, t.String())
	}
	// The iPod tab's rows, with Advanced closed: Advanced is the one
	// place the disk path, the OSOS entry and the device files may appear.
	if u.st.Device.Found {
		adv := u.st.Advanced
		u.st.Advanced = false
		out = append(out, u.ipodStrings()...)
		u.st.Advanced = adv
	}
	out = append(out, musicStrings()...)
	cat := u.catalog()
	for _, a := range cat.Artists {
		out = append(out, a.Facts, artistStatus(u.st.Albums, a.Albums))
	}
	for _, g := range cat.Genres {
		out = append(out, g.Facts)
	}
	out = append(out, menuLabels[:]...)
	out = append(out, u.settingsStrings()...)
	out = append(out, chooserStrings()...)
	for k := JobNone; k <= JobEditTags; k++ {
		out = append(out, k.String())
	}
	out = append(out, albumEditStrings()...)
	for _, c := range Categories {
		out = append(out, c.Title(), c.Sub())
	}
	out = append(out, syncPreviewStrings(cannedPreview(u))...)
	// The log drawer's tabs and the iPod tab's dividers and states — not
	// the device's own lines under them, which are the log.
	out = append(out, devLogStrings()...)
	return out
}

// syncPreviewStrings is the sync preview's front: everything but the plan
// behind "Show the plan", which — like the flasher's — is the plan as the
// command line prints it.
func syncPreviewStrings(d *dialogRequest) []string {
	out := []string{d.title, d.total, d.note, d.okLabel}
	for _, rows := range [][]cardRow{d.card, d.items} {
		for _, r := range rows {
			out = append(out, r.label, r.mid, r.value)
		}
	}
	return out
}

func TestNoOldWordsOnTheFront(t *testing.T) {
	states := map[string]func() State{
		"demo": DemoState, "library": LibraryState, "empty": EmptyState,
		"looking": LookingState, "notinstalled": NotInstalledState, "menu": MenuState,
		"artists": ArtistsState, "genres": GenresState, "genre": GenreState, "songs": SongsState,
	}
	for name, mk := range states {
		for _, th := range []Theme{ThemeLinen, ThemeOnyx} {
			th := th
			u := NewUI(Options{Theme: &th})
			u.SetState(mk())
			for _, tab := range []Tab{TabAlbums, TabLibrary, TabDetails} {
				u.st.Tab = tab
				for _, v := range musicViews {
					// The tab row's note is the view's own on the Music tab.
					u.st.View = v
					for _, s := range frontStrings(u) {
						for _, bad := range forbidden {
							if strings.Contains(s, bad) {
								t.Errorf("%s/%s/%s/%s: %q contains %q", name, th, tab, v, s, bad)
							}
						}
					}
				}
			}
		}
	}
}

// Picking a file for "Install from a file…" chooses it; it does not
// install. The button then names the file, and pressing it installs through
// the usual gate.
func TestInstallFromAFileWaitsForTheSecondClick(t *testing.T) {
	f := &fakeBackend{dev: NotInstalledState().Device, volumeDir: t.TempDir()}
	u := newTestUI(f)
	u.st.Device = f.dev
	u.mu.Lock()
	u.newPath, u.pathFor = `C:\images\core-v0.2.1.ipod`, JobInstall
	u.mu.Unlock()
	u.drain()
	if u.st.Busy() || len(f.installCalls) != 0 {
		t.Fatalf("the pick installed: busy=%v calls=%d", u.st.Busy(), len(f.installCalls))
	}
	if u.st.InstallFile != `C:\images\core-v0.2.1.ipod` {
		t.Fatalf("InstallFile = %q", u.st.InstallFile)
	}
	if got := "Install from " + fileBase(u.st.InstallFile); got != "Install from core-v0.2.1.ipod" {
		t.Errorf("button = %q", got)
	}

	answerDialogs(u, f.dev.Path)
	u.startInstall(u.st.InstallFile)
	settle(u)
	if len(f.installCalls) != 1 || f.installCalls[0].Image != `C:\images\core-v0.2.1.ipod` {
		t.Fatalf("install calls = %+v", f.installCalls)
	}
	if f.installAns != f.dev.Path {
		t.Errorf("the install did not go through the typed gate: answer %q", f.installAns)
	}
}

// Rescan — on the Library tab and on the empty grid — is the scan AND the
// preview that fills the grid, the same pair a newly chosen folder gets.
func TestRescanAlsoPreviews(t *testing.T) {
	f := &fakeBackend{}
	u := newTestUI(f)
	u.autoChecked = true
	u.rescan()
	settleWindow(u)
	if len(f.inspectCalls) != 1 || f.inspectCalls[0] != "/src" {
		t.Fatalf("inspect calls = %q", f.inspectCalls)
	}
	if len(f.syncCalls) != 1 || !f.syncCalls[0].DryRun {
		t.Fatalf("sync calls after a rescan = %+v, want one preview", f.syncCalls)
	}
}

// The preview's overflow line promises the log lists everything: it does.
func TestScanListsEveryItemInTheLog(t *testing.T) {
	st := LibraryState()
	u := NewUI(Options{})
	u.mu.Lock()
	u.newReport = st.Report
	u.mu.Unlock()
	u.drain()
	log := strings.Join(u.st.Log, "\n")
	for _, m := range st.Report.Misnamed {
		if !strings.Contains(log, relPath(st.Report.Root, m.To)) {
			t.Errorf("the log does not list %s", m.To)
		}
	}
	for _, a := range st.Report.NeedsAttention {
		if !strings.Contains(log, relPath(st.Report.Root, a.Path)) {
			t.Errorf("the log does not list %s", a.Path)
		}
	}
}

func TestShortName(t *testing.T) {
	if got := shortName("core-v0.2.1.ipod", 28); got != "core-v0.2.1.ipod" {
		t.Errorf("short = %q", got)
	}
	got := shortName("core-v0.2.1-with-a-very-long-name.ipod", 28)
	if len([]rune(got)) != 28 || !strings.HasSuffix(got, "name.ipod") || !strings.HasPrefix(got, "core-v0.2.1") {
		t.Errorf("long = %q", got)
	}
}

// The tabs are named for what they are for (the owner's decision in the
// app pass): Music, Fix, Summary — and the Fix count reads "Fix · 16" (the
// canned report: 13 from the first checks plus two small covers and one
// duplicate pair).
func TestTabNames(t *testing.T) {
	want := map[Tab]string{TabAlbums: "Music", TabLibrary: "Fix", TabDetails: "Summary"}
	for tab, name := range want {
		if tab.String() != name {
			t.Errorf("tab %d is %q, want %q", tab, tab.String(), name)
		}
	}
	u := NewUI(Options{})
	u.SetState(LibraryState())
	if n := u.fixCount(); n != 16 {
		t.Fatalf("the canned report counts %d", n)
	}
}

// albumEditStrings is the album sheet's Edit…: its button, the fields'
// labels and hints, the foot's line, and what a save says after.
func albumEditStrings() []string {
	e := newAlbumEdit(albumTags{Mixed: map[string]bool{librarian.TagGenre: true}})
	res := &librarian.EditResult{Written: 3, Renamed: 2, Failures: []librarian.Failure{{Reason: "x"}}}
	return []string{"Edit…", "Album", "Album artist", "Genre", "Year", "Title", "Artist", editHint,
		e.hint(librarian.TagGenre, "Genre"), e.hint(librarian.TagAlbum, "Album title"),
		savedNote(res, "The Great Curve"), "Nothing was changed.", "Reading the tags…", "Saving…"}
}

// The editor's own words: what Save does is said in the library's terms —
// the music folder, the next sync — and never as an edit of the iPod.
func TestTheEditorSaysWhereTheChangeGoes(t *testing.T) {
	if !strings.Contains(editHint, "music folder") || !strings.Contains(editHint, "next sync") {
		t.Errorf("editHint %q", editHint)
	}
	got := savedNote(&librarian.EditResult{Written: 1, Moved: 1}, "")
	if got != "Saved: 1 file retagged, 1 renamed to match. The next sync copies them; Undo on the Fix tab takes it back." {
		t.Errorf("savedNote %q", got)
	}
	for _, s := range albumEditStrings() {
		if strings.Contains(s, "on the iPod") && !strings.Contains(s, "next sync") {
			t.Errorf("%q talks about editing the iPod", s)
		}
	}
}
