package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
	"github.com/BrandonDedolph/core-desktop-app/internal/doctor"
	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// The Summary tab's Library row is the doctor's library checks in words, and
// its Repair is a sync through the preview — with the covers re-rendered
// exactly when a cover is what is wrong.

var (
	coverFinding = doctor.Finding{Text: "3 albums have no cover on the iPod", Repair: doctor.RepairArt, N: 3,
		Names: []string{"A - B", "C - D", "E - F"}, Fix: "a sync renders the covers again"}
	songFinding = doctor.Finding{Text: "2 songs in the index are not on the iPod", Repair: doctor.RepairPlain, N: 2,
		Names: []string{"A - B/01. X.flac", "A - B/02. Y.flac"}, Fix: "a sync copies them back"}
	tornFinding = doctor.Finding{Text: "On-The-Go 3 is a save that was interrupted", Repair: doctor.RepairNone, N: 1,
		Fix: "open it on the iPod and choose Delete Playlist; a sync never rewrites a saved list"}
)

func libraryWith(fs ...doctor.Finding) Library {
	lib := Library{Present: true, Songs: 907, Albums: 101, Genres: 17, Findings: fs}
	for _, f := range fs {
		if f.Repair > lib.Repair {
			lib.Repair = f.Repair
		}
	}
	return lib
}

func libraryRowFor(t *testing.T, lib Library) ipodRow {
	t.Helper()
	u := newTestUI(&fakeBackend{})
	u.st.Library = lib
	return rowByLabel(t, u, "Library on the iPod")
}

func TestLibraryRowClean(t *testing.T) {
	r := libraryRowFor(t, libraryWith())
	if r.value != "907 songs · 101 albums · 17 genres · index valid" || r.warn != "" ||
		len(r.lines) != 0 || len(r.actions) != 0 {
		t.Errorf("row = %+v", r)
	}
	// Nothing to read about a library that is not there: Storage says the
	// first sync writes one, and there is no Library row to contradict it.
	u := newTestUI(&fakeBackend{})
	for _, row := range u.ipodRows() {
		if row.label == "Library on the iPod" {
			t.Errorf("a Library row with no library: %+v", row)
		}
	}
}

func TestLibraryRowFindings(t *testing.T) {
	r := libraryRowFor(t, libraryWith(coverFinding, songFinding))
	if r.value != "907 songs · 101 albums · 17 genres · " || r.warn != "2 problems" {
		t.Errorf("value %q warn %q", r.value, r.warn)
	}
	if strings.Join(r.lines, "|") != coverFinding.Text+"|"+songFinding.Text {
		t.Errorf("lines = %q", r.lines)
	}
	if !strings.Contains(r.sub, "renders every cover again") {
		t.Errorf("sub = %q — the repair re-renders covers and must say so", r.sub)
	}
	if strings.Join(actionLabels(r), ",") != "Repair" || !r.actions[0].enabled {
		t.Errorf("actions = %+v", r.actions)
	}

	r = libraryRowFor(t, libraryWith(songFinding))
	if r.warn != "1 problem" || !strings.HasPrefix(r.sub, "Repair is a sync:") ||
		strings.Contains(r.sub, "cover") {
		t.Errorf("plain: warn %q sub %q", r.warn, r.sub)
	}
}

// What a sync cannot fix carries what does on its own line, and on its own it
// gets no Repair: a button that runs a sync which changes nothing is a
// button that lies.
func TestLibraryRowWhatASyncCannotFix(t *testing.T) {
	r := libraryRowFor(t, libraryWith(tornFinding))
	if len(r.actions) != 0 {
		t.Errorf("a Repair for a finding no sync fixes: %+v", r.actions)
	}
	if len(r.lines) != 1 || !strings.Contains(r.lines[0], "Delete Playlist") ||
		!strings.Contains(r.sub, "a sync cannot fix this") {
		t.Errorf("row = %+v", r)
	}
	// Beside one a sync does fix, Repair is back, and the torn slot still
	// says its own way out.
	r = libraryRowFor(t, libraryWith(songFinding, tornFinding))
	if strings.Join(actionLabels(r), ",") != "Repair" || !strings.Contains(r.lines[1], "Delete Playlist") {
		t.Errorf("row = %+v", r)
	}
}

func TestLibraryRowListsFourThenTheRest(t *testing.T) {
	var fs []doctor.Finding
	for i := 0; i < 6; i++ {
		fs = append(fs, songFinding)
	}
	r := libraryRowFor(t, libraryWith(fs...))
	if len(r.lines) != 5 || r.lines[4] != "… and 2 more" || r.warn != "6 problems" {
		t.Errorf("lines = %q warn %q", r.lines, r.warn)
	}
	// Five fit: a fifth line is the finding itself, not "… and 1 more".
	r = libraryRowFor(t, libraryWith(fs[:5]...))
	if len(r.lines) != 5 || r.lines[4] != songFinding.Text {
		t.Errorf("five: %q", r.lines)
	}
}

func TestLibraryRowNeedsAMusicFolder(t *testing.T) {
	u := newTestUI(&fakeBackend{})
	u.st.Library = libraryWith(songFinding)
	u.st.Source = ""
	r := rowByLabel(t, u, "Library on the iPod")
	if len(r.actions) != 1 || r.actions[0].enabled || !strings.Contains(r.sub, "choose your music folder") {
		t.Errorf("row = %+v", r)
	}
}

// repairThroughTheButton clicks Repair the way a person does, presses Sync
// on the preview, and returns the preview and the options both Sync calls
// ran with.
func repairThroughTheButton(t *testing.T, lib Library, first *syncer.Plan) (*dialogRequest, []syncer.Options, []string) {
	t.Helper()
	f := &fakeBackend{plans: []*syncer.Plan{first}}
	u := newTestUI(f)
	u.st.Library = lib
	u.st.Tab = TabDetails
	r := rowByLabel(t, u, "Library on the iPod")
	if len(r.actions) != 1 {
		t.Fatalf("no Repair: %+v", r)
	}
	r.actions[0].click.Click()
	d := frameUntilDialog(t, u)
	clickThrough(u, &u.dlgOK)
	settle(u)
	return d, f.syncCalls, u.st.Log
}

func TestRepairIsASyncThroughThePreview(t *testing.T) {
	covers := &syncer.Plan{Art: []syncer.ArtOp{
		{Album: "A - B", Write: true}, {Album: "C - D", Write: true}, {Album: "E - F", Write: true},
	}}
	d, calls, log := repairThroughTheButton(t, libraryWith(coverFinding, songFinding), covers)
	if len(calls) != 2 || !calls[0].DryRun || calls[1].DryRun {
		t.Fatalf("sync calls = %+v — the plan, then the sync", calls)
	}
	for i, o := range calls {
		if !o.ArtRefresh {
			t.Errorf("call %d without ArtRefresh: the covers are the finding", i)
		}
	}
	var card []string
	for _, c := range d.card {
		card = append(card, c.label+" "+c.value)
	}
	if !strings.Contains(strings.Join(card, "|"), previewCoversLabel+" 3 albums") {
		t.Errorf("preview card = %q", card)
	}
	if !strings.Contains(strings.Join(log, "\n"), "repair: 3 albums have no cover on the iPod (A - B; C - D; E - F)") {
		t.Errorf("the log does not say why:\n%s", strings.Join(log, "\n"))
	}

	_, calls, _ = repairThroughTheButton(t, libraryWith(songFinding),
		&syncer.Plan{Copy: []syncer.FileOp{{Album: "A - B", Src: "/src/x.flac", Size: 10}}})
	if len(calls) != 2 {
		t.Fatalf("sync calls = %+v", calls)
	}
	for i, o := range calls {
		if o.ArtRefresh {
			t.Errorf("call %d re-renders every cover for a missing song", i)
		}
	}
}

// Cancel on the repair's preview writes nothing, as on any sync's.
func TestRepairCancelWritesNothing(t *testing.T) {
	f := &fakeBackend{plans: []*syncer.Plan{{Copy: []syncer.FileOp{{Album: "A - B", Src: "/src/x.flac", Size: 10}}}}}
	u := newTestUI(f)
	u.st.Library = libraryWith(songFinding)
	u.startRepair()
	frameUntilDialog(t, u)
	clickThrough(u, &u.dlgCancel)
	settle(u)
	if len(f.syncCalls) != 1 || !f.syncCalls[0].DryRun {
		t.Errorf("sync calls = %+v", f.syncCalls)
	}

	// And a library whose only finding no sync fixes starts nothing.
	f = &fakeBackend{}
	u = newTestUI(f)
	u.st.Library = libraryWith(tornFinding)
	u.startRepair()
	settle(u)
	if len(f.syncCalls) != 0 || u.st.Job != nil {
		t.Errorf("a repair ran for nothing a sync fixes: %+v / %+v", f.syncCalls, u.st.Job)
	}
}

// The real findings, from the real checks on a volume broken many ways at
// once, read through the row: none of them uses a word the front of the
// window does not. (The file names CORECFG and the like are the log's and
// Advanced's.)
func TestLibraryFindingsWords(t *testing.T) {
	root := t.TempDir()
	music := filepath.Join(root, devicefs.MusicDir)
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(music, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("A - B/01. X.flac", "audio")                                         // no covers
	write(devicefs.IndexName, "CIDX junk")                                     // a damaged index
	write(devicefs.PlaylistDir+"/Gym.m3u8", "/Music/Gone/01.flac\n")           // points at nothing
	write(devicefs.PlaylistDir+"/"+devicefs.OTGSlotName(1), "/Music/x.flac\n") // a foreign slot
	if err := os.WriteFile(filepath.Join(root, devicefs.ConfigName), make([]byte, devicefs.ConfigFileBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := doctor.LibraryChecks(root)
	if len(rep.Findings) < 6 {
		t.Fatalf("the broken volume found only %d things", len(rep.Findings))
	}
	u := newTestUI(&fakeBackend{})
	u.st.Library = Library{Present: true, Findings: rep.Findings, Repair: rep.Repair()}
	strs := u.ipodStrings()
	for _, f := range rep.Findings {
		strs = append(strs, f.Text, f.Fix)
	}
	for _, s := range strs {
		for _, bad := range forbidden {
			if strings.Contains(s, bad) {
				t.Errorf("%q contains %q", s, bad)
			}
		}
	}
}
