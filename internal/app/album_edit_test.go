package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"

	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
)

// editUI is a window on a fake backend with the sheet open on a three-track
// album whose tags readAlbumTags answers without a disk.
func editUI(t *testing.T) (*UI, *fakeBackend, *fakeAudio, Album) {
	t.Helper()
	f := &fakeBackend{}
	fa := &fakeAudio{}
	u := NewUI(Options{Backend: f, Audio: fa})
	u.SetState(DemoState())
	u.st.Source = "/m"
	u.st.Device = Device{Found: true, Volume: `D:\`}
	a := Album{Dir: "/m/a", Device: "Talking Heads - Remain in Light", Title: "Talking Heads - Remain in Light",
		Tracks: 3, State: TileOnDevice, TrackList: threeTracks()}
	u.st.Albums = append([]Album{a}, u.st.Albums...)
	readAlbumTags = func(ts []Track) (albumTags, error) {
		at := albumTags{Album: "Remain in Light", AlbumArtist: "Talking Heads", Year: "1980",
			Mixed: map[string]bool{librarian.TagGenre: true}}
		for _, tr := range ts {
			at.Tracks = append(at.Tracks, trackTags{Path: tr.Path, Title: tr.Title, Artist: "Talking Heads",
				Number: itoa(tr.Number)})
		}
		return at, nil
	}
	t.Cleanup(func() { readAlbumTags = ReadAlbumTags; readTracks = ReadTracks })
	readTracks = func(dir string) ([]Track, error) { return threeTracks(), nil }
	u.openSheet(a)
	return u, f, fa, a
}

// openEditor clicks Edit… and draws frames until the fields are open.
func openEditor(t *testing.T, u *UI) *albumEdit {
	t.Helper()
	u.Layout(newTestContext(900, 600))
	u.sheet.editBtn.Click()
	deadline := time.Now().Add(2 * time.Second)
	for u.sheet.edit == nil && time.Now().Before(deadline) {
		u.Layout(newTestContext(900, 600))
		time.Sleep(time.Millisecond)
	}
	if u.sheet.edit == nil {
		t.Fatalf("the editor did not open (note %q)", u.sheet.note)
	}
	return u.sheet.edit
}

// Save sends exactly what was changed — the album title to every track, the
// one retitled track's title — to the backend, for the album's folder in
// the music folder, chained to the session's journal; the sheet then shows
// the album re-read and the grid marks it CHANGED.
func TestEditSaveSendsTheEdits(t *testing.T) {
	u, f, _, a := editUI(t)
	u.st.Journal = "/journal/earlier-fix.json"
	e := openEditor(t, u)
	e.album.SetText("Remain in Light (Deluxe)")
	e.rows[1].title.SetText("Crosseyed & Painless")
	e.saveBtn.Click()
	u.Layout(newTestContext(900, 600))
	settle(u)

	if len(f.editCalls) != 1 {
		t.Fatalf("EditTags calls: %d", len(f.editCalls))
	}
	c := f.editCalls[0]
	if c.root != "/m" || c.dir != a.Dir || c.after != "/journal/earlier-fix.json" {
		t.Errorf("call %+v", c)
	}
	if len(c.edits) != 3 {
		t.Fatalf("edits %+v", c.edits)
	}
	for i, ed := range c.edits {
		if ed.Path != threeTracks()[i].Path || ed.Set[librarian.TagAlbum] != "Remain in Light (Deluxe)" {
			t.Errorf("edit %d: %+v", i, ed)
		}
		_, retitled := ed.Set[librarian.TagTitle]
		if retitled != (i == 1) {
			t.Errorf("edit %d: title in it = %v", i, retitled)
		}
		for _, untouched := range []string{librarian.TagGenre, librarian.TagYear, librarian.TagArtist,
			librarian.TagAlbumArtist, librarian.TagTrack} {
			if _, ok := ed.Set[untouched]; ok {
				t.Errorf("edit %d writes %s, which nobody changed", i, untouched)
			}
		}
	}
	if c.edits[1].Set[librarian.TagTitle] != "Crosseyed & Painless" {
		t.Errorf("title %q", c.edits[1].Set[librarian.TagTitle])
	}

	if u.sheet == nil || u.sheet.edit != nil {
		t.Fatal("the sheet should be open with the fields closed")
	}
	if !strings.HasPrefix(u.sheet.note, "Saved: 3 files retagged") {
		t.Errorf("note %q", u.sheet.note)
	}
	if u.st.Journal != "/journal/edit-fix.json" || !u.st.LibraryChanged {
		t.Errorf("journal %q, changed %v", u.st.Journal, u.st.LibraryChanged)
	}
	got := u.st.Albums[0]
	if got.State != TileChanged || got.Copy != 3 || got.Title != "Talking Heads - Remain in Light (Deluxe)" {
		t.Errorf("grid album %+v", got)
	}
	if u.sheet.album.Title != got.Title || tileTitle(u.sheet.album) != "Remain in Light (Deluxe)" {
		t.Errorf("sheet album %q", u.sheet.album.Title)
	}
	// Then the queued passes: the Fix tab's scan of the renamed tree, and
	// the sync preview, whose plan says CHANGED for the same reason.
	settleWindow(u)
	if len(f.inspectCalls) == 0 || len(f.syncCalls) == 0 || !f.syncCalls[len(f.syncCalls)-1].DryRun {
		t.Errorf("after a save: %d scans, sync calls %+v", len(f.inspectCalls), f.syncCalls)
	}
}

// Cancel closes the fields and writes nothing; so does Esc, which leaves the
// sheet open.
func TestEditCancelWritesNothing(t *testing.T) {
	u, f, _, _ := editUI(t)
	e := openEditor(t, u)
	e.album.SetText("Something else")
	e.cancelBtn.Click()
	u.Layout(newTestContext(900, 600))
	settle(u)
	if u.sheet == nil || u.sheet.edit != nil || len(f.editCalls) != 0 || u.st.Busy() {
		t.Fatalf("after Cancel: sheet %v, edit %v, calls %d", u.sheet != nil, u.sheet != nil && u.sheet.edit != nil, len(f.editCalls))
	}

	e = openEditor(t, u)
	if e.album.Text() != "Remain in Light" {
		t.Errorf("the fields kept the cancelled text: %q", e.album.Text())
	}
	e.album.SetText("Something else")
	pressKey(u, key.NameEscape, 0)
	if u.sheet == nil || u.sheet.edit != nil || len(f.editCalls) != 0 {
		t.Error("Esc should close the fields, not the sheet, and write nothing")
	}
	// A click outside the sheet while the fields are open does nothing.
	e = openEditor(t, u)
	u.sheet.scrim.Click()
	u.Layout(newTestContext(900, 600))
	if u.sheet == nil || u.sheet.edit == nil {
		t.Error("a click outside closed the editor")
	}
}

// Save with nothing changed starts no job; a bad number or an empty title is
// refused on the spot; a genre the tracks disagree about is left alone until
// it is typed into, and then goes to every track.
func TestEditOnlyWritesWhatChanged(t *testing.T) {
	u, f, _, _ := editUI(t)
	e := openEditor(t, u)
	e.saveBtn.Click()
	u.Layout(newTestContext(900, 600))
	if u.st.Busy() || len(f.editCalls) != 0 || u.sheet.note != "Nothing was changed." {
		t.Errorf("unchanged save: busy %v, calls %d, note %q", u.st.Busy(), len(f.editCalls), u.sheet.note)
	}

	e = openEditor(t, u)
	e.rows[0].number.SetText("one")
	e.saveBtn.Click()
	u.Layout(newTestContext(900, 600))
	if len(f.editCalls) != 0 || !strings.Contains(e.note, "whole numbers") {
		t.Errorf("bad number: calls %d, note %q", len(f.editCalls), e.note)
	}
	e.rows[0].number.SetText("1")
	e.rows[2].title.SetText("  ")
	if _, err := e.edits(); err == nil || !strings.Contains(err.Error(), "Track 3 needs a title") {
		t.Errorf("empty title: %v", err)
	}
	e.rows[2].title.SetText("Three")

	if e.genre.Text() != "" {
		t.Errorf("a mixed genre opens empty, got %q", e.genre.Text())
	}
	e.genre.SetText("New Wave")
	e.rows[0].number.SetText("01")
	edits, err := e.edits()
	if err != nil || len(edits) != 3 {
		t.Fatalf("edits %+v %v", edits, err)
	}
	for i, ed := range edits {
		if ed.Set[librarian.TagGenre] != "New Wave" {
			t.Errorf("edit %d: %+v", i, ed.Set)
		}
	}
	if edits[0].Set[librarian.TagTrack] != "01" {
		t.Errorf("number %+v", edits[0].Set)
	}
}

// A Save that rewrites the playing track stops it first and says so; one
// that leaves the playing track alone lets it play.
func TestEditStopsThePlayingTrack(t *testing.T) {
	u, f, fa, a := editUI(t)
	u.playFrom(a, a.TrackList, 1, false)
	e := openEditor(t, u)
	e.rows[0].title.SetText("Uno")
	e.saveBtn.Click()
	u.Layout(newTestContext(900, 600))
	settle(u)
	if fa.stops != 0 || u.st.NowPlaying == nil {
		t.Fatalf("an edit of another track stopped the music (stops %d)", fa.stops)
	}

	e = openEditor(t, u)
	e.rows[1].title.SetText("Dos")
	e.saveBtn.Click()
	u.Layout(newTestContext(900, 600))
	settle(u)
	if fa.stops != 1 || u.st.NowPlaying != nil {
		t.Fatalf("the playing track was not stopped (stops %d)", fa.stops)
	}
	if len(f.editCalls) != 2 || !strings.HasPrefix(u.sheet.note, "Stopped “Two” to rewrite it. Saved") {
		t.Errorf("calls %d, note %q", len(f.editCalls), u.sheet.note)
	}
	if !strings.Contains(strings.Join(u.st.Log, "\n"), "stopped playing 02.flac") {
		t.Error("the log does not say the music was stopped")
	}
}

// A Save that fails keeps the fields open with the reason, and nothing in
// the grid changes.
func TestEditFailureKeepsTheFields(t *testing.T) {
	u, f, _, _ := editUI(t)
	f.editErr = errors.New("D: is read-only")
	e := openEditor(t, u)
	e.year.SetText("1981")
	e.saveBtn.Click()
	u.Layout(newTestContext(900, 600))
	settle(u)
	if u.sheet.edit == nil || !strings.Contains(u.sheet.edit.note, "read-only") || u.sheet.edit.saving {
		t.Fatalf("edit %+v", u.sheet.edit)
	}
	if u.st.Albums[0].State != TileOnDevice || u.st.LibraryChanged {
		t.Error("a failed save marked the album changed")
	}
}

// Space while the fields are open is a space, not play/pause.
func TestSpaceInTheEditorIsASpace(t *testing.T) {
	u, _, _, a := editUI(t)
	u.playFrom(a, a.TrackList, 0, false)
	openEditor(t, u)
	pressKey(u, key.NameSpace, 0)
	if np := u.st.NowPlaying; np == nil || !np.Playing {
		t.Error("Space paused the music while the fields were open")
	}
	u.cancelEdit()
	pressKey(u, key.NameSpace, 0)
	if np := u.st.NowPlaying; np == nil || np.Playing {
		t.Error("with the fields closed Space should pause again (the test's own control)")
	}
}

// ReadAlbumTags on real files: the album fields from the tags, a field the
// tracks disagree on marked mixed, "3/12" shown as 3.
func TestReadAlbumTags(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, tags map[string]string) string {
		p := filepath.Join(dir, name)
		raw := flac.BuildFile(flac.StreamInfo{SampleRate: 44100, Channels: 2, BitsPerSample: 16, TotalSamples: 4410}, tags, nil)
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p1 := write("01.flac", map[string]string{"album": "Blue", "artist": "Joni Mitchell", "title": "All I Want",
		"tracknumber": "1/10", "genre": "Folk", "date": "1971", "album artist": "Joni Mitchell"})
	p2 := write("02.flac", map[string]string{"album": "Blue", "artist": "Joni Mitchell", "title": "My Old Man",
		"tracknumber": "2/10", "genre": "Singer-songwriter", "date": "1971", "album artist": "Joni Mitchell"})
	at, err := ReadAlbumTags([]Track{{Path: p1}, {Path: p2}})
	if err != nil {
		t.Fatal(err)
	}
	if at.Album != "Blue" || at.AlbumArtist != "Joni Mitchell" || at.Year != "1971" || at.Genre != "" ||
		!at.Mixed[librarian.TagGenre] || at.Mixed[librarian.TagAlbum] {
		t.Errorf("album fields %+v", at)
	}
	if at.Tracks[1].Title != "My Old Man" || at.Tracks[1].Number != "2" || at.Tracks[0].Artist != "Joni Mitchell" {
		t.Errorf("tracks %+v", at.Tracks)
	}
	if _, err := ReadAlbumTags([]Track{{Title: "canned"}}); err == nil {
		t.Error("a tracklist with no files was editable")
	}
}

func TestRetitled(t *testing.T) {
	set := func(kv ...string) []librarian.TagEdit {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return []librarian.TagEdit{{Set: m}}
	}
	for _, c := range []struct {
		title string
		edits []librarian.TagEdit
		want  string
	}{
		{"Talking Heads - Remain in Light", set(librarian.TagAlbum, "Fear of Music"), "Talking Heads - Fear of Music"},
		{"Talking Heads - Remain in Light", set(librarian.TagAlbumArtist, "Heads"), "Heads - Remain in Light"},
		{"Talking Heads - Remain in Light", set(librarian.TagTitle, "x"), "Talking Heads - Remain in Light"},
		{"Talking Heads - Remain in Light", nil, "Talking Heads - Remain in Light"},
	} {
		if got := retitled(c.title, c.edits); got != c.want {
			t.Errorf("retitled(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// The editor lays out at every size, in both palettes.
func TestTheEditorLaysOut(t *testing.T) {
	for _, th := range []Theme{ThemeLinen, ThemeOnyx} {
		for _, sz := range [][2]int{{720, 520}, {900, 600}, {1400, 1000}} {
			th := th
			u := NewUI(Options{Backend: &fakeBackend{}, Audio: &fakeAudio{}, Theme: &th})
			u.SetState(DemoState())
			if err := cannedDialog(u, "albumedit"); err != nil {
				t.Fatal(err)
			}
			if u.sheet == nil || u.sheet.edit == nil || len(u.sheet.edit.rows) != 8 {
				t.Fatalf("canned editor %+v", u.sheet)
			}
			if d := u.Layout(newTestContext(sz[0], sz[1])); d.Size.X != sz[0] || d.Size.Y != sz[1] {
				t.Errorf("%s %v: %v", th, sz, d.Size)
			}
		}
	}
}
