package app

import (
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
)

// The sheet: one row per album with its tick (on except the ones already
// there and the ones that cannot be added, which say why), the name it
// gets, the songs, the size; the one tick for the originals, on; and the
// button counting the ticks, off at none.
func TestAddDialogRowsAndButton(t *testing.T) {
	plan := cannedAddPlan()
	d := addDialog(plan)
	if d.title != "Adding to your library" {
		t.Errorf("title %q", d.title)
	}
	if c := card(d); c["From"] != plan.Source || c["Into"] != plan.Library ||
		c["Found"] != "6 albums · 53 songs · FLAC 38, M4A 11, MP3 4 · 1.1 GB" {
		t.Errorf("card = %v", c)
	}
	if len(d.items) != 6 || d.items[0].label != "Blue - Joni Mitchell" || d.items[0].mid != "10 songs" ||
		d.items[0].value != "274 MB" {
		t.Fatalf("rows = %+v", d.items)
	}
	wantPicks := []bool{true, true, false, true, false, false}
	wantCan := []bool{true, true, true, true, false, false}
	for i := range wantPicks {
		if d.picks[i] != wantPicks[i] || d.pickable[i] != wantCan[i] {
			t.Errorf("row %d (%s): ticked %v, can %v; want %v, %v", i, d.items[i].label,
				d.picks[i], d.pickable[i], wantPicks[i], wantCan[i])
		}
		if !wantPicks[i] && d.pickWhy[i] == "" {
			t.Errorf("row %d is unticked and does not say why", i)
		}
	}
	if !strings.Contains(d.pickWhy[2], "already in your library") && !strings.Contains(d.pickWhy[2], "same songs") {
		t.Errorf("the duplicate's why = %q", d.pickWhy[2])
	}
	if d.option != addOptionText || !d.optionOn {
		t.Errorf("the originals tick: %q on=%v", d.option, d.optionOn)
	}
	if d.total != "3 new to your library · 1 already in it · 2 that cannot be added" {
		t.Errorf("total %q", d.total)
	}
	if label, ok := d.okButton(); label != "Add 3 albums" || !ok {
		t.Errorf("button %q enabled=%v", label, ok)
	}
	// Ticking the one already there counts it; a row that cannot be added
	// does not count even if its tick were somehow on.
	d.picks[2], d.picks[4] = true, true
	if label, _ := d.okButton(); label != "Add 4 albums" {
		t.Errorf("button %q", label)
	}
	if got := d.chosen(plan); len(got) != 4 || got[2] != "Low - David Bowie" {
		t.Errorf("chosen = %v", got)
	}
	for i := range d.picks {
		d.picks[i] = false
	}
	if label, ok := d.okButton(); label != "Add 0 albums" || ok {
		t.Errorf("with nothing ticked: %q enabled=%v", label, ok)
	}
}

// A folder with nothing addable says so, and offers no delete tick.
func TestAddDialogWithNothingToAdd(t *testing.T) {
	plan := &librarian.AddPlan{Albums: []librarian.AddAlbum{{Key: "?x", Name: "x", Problem: "no tags"}}}
	d := addDialog(plan)
	if d.note != addNoteNone || d.option != "" {
		t.Errorf("note %q option %q", d.note, d.option)
	}
	if _, ok := d.okButton(); ok {
		t.Error("the button is enabled with nothing to add")
	}
}

// Cancel adds nothing: the plan is read, the sheet shown, and Add is never
// called.
func TestAddCancelAddsNothing(t *testing.T) {
	f := &fakeBackend{addPlan: cannedAddPlan()}
	u := newTestUI(f)
	u.st.Journal = "/journal/before.json"
	answered := answerDialogs(u, CancelAnswer)
	u.addFolder("/downloads")
	settle(u)
	if n := <-answered; n != 1 {
		t.Fatalf("the sheet was shown %d times", n)
	}
	if len(f.planAddCalls) != 1 || f.planAddCalls[0] != [2]string{"/downloads", "/src"} {
		t.Errorf("PlanAdd calls = %v", f.planAddCalls)
	}
	if len(f.addCalls) != 0 {
		t.Fatalf("Add ran after a cancel: %+v", f.addCalls)
	}
	if u.st.Journal != "/journal/before.json" || u.st.LibraryChanged {
		t.Errorf("a cancel changed the model: journal %q changed=%v", u.st.Journal, u.st.LibraryChanged)
	}
}

// The ticks on screen are what Add is handed: an unticked row stays out, the
// originals tick off keeps them, the session's journal is chained; then the
// journal is Undo's, Sync is the header's button, and the scan and the grid
// run again.
func TestAddRunsWithTheTicksAndUndoFollows(t *testing.T) {
	f := &fakeBackend{addPlan: cannedAddPlan(), report: fixtureReport()}
	u := newTestUI(f)
	u.st.Journal = "/journal/before.json"
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			u.mu.Lock()
			req := u.askQueue
			u.askQueue = nil
			u.mu.Unlock()
			if req == nil {
				time.Sleep(time.Millisecond)
				continue
			}
			req.picks[1] = false // Hounds of Love: not this time
			req.optionOn = false
			req.reply <- "yes"
			return
		}
	}()
	u.addFolder("/downloads")
	settle(u)
	if len(f.addCalls) != 1 {
		t.Fatalf("Add calls = %+v", f.addCalls)
	}
	c := f.addCalls[0]
	if len(c.chosen) != 2 || c.chosen[0] != "Blue - Joni Mitchell" || c.chosen[1] != "Mezzanine - Massive Attack" {
		t.Errorf("chosen = %v", c.chosen)
	}
	if c.deleteOriginals || c.after != "/journal/before.json" {
		t.Errorf("deleteOriginals=%v after=%q", c.deleteOriginals, c.after)
	}
	if u.st.Journal != "/journal/add.json" || !u.st.LibraryChanged {
		t.Errorf("journal %q changed=%v", u.st.Journal, u.st.LibraryChanged)
	}
	if !strings.HasPrefix(u.st.Notice, "Added 2 albums") {
		t.Errorf("notice %q", u.st.Notice)
	}
	// The queued scan and plan run next.
	for i := 0; i < 4 && len(f.inspectCalls) == 0; i++ {
		u.drain()
		settle(u)
	}
	if len(f.inspectCalls) == 0 || f.inspectCalls[0] != "/src" {
		t.Errorf("the Fix scan did not run again: %v", f.inspectCalls)
	}
	u.startUndo(u.st.Journal)
	settle(u)
	if len(f.undoCalls) != 1 || f.undoCalls[0] != "/journal/add.json" {
		t.Errorf("Undo calls = %v", f.undoCalls)
	}
}

// An add that put nothing in the library leaves the journal and the header
// as they were.
func TestAddThatAddedNothingChangesNothing(t *testing.T) {
	f := &fakeBackend{addPlan: cannedAddPlan(), addResult: &librarian.AddResult{
		Failures: []librarian.Failure{{Album: "Blue - Joni Mitchell", Reason: "the copy of 01.flac did not match"}}}}
	u := newTestUI(f)
	u.st.Journal = "/journal/before.json"
	answered := answerDialogs(u, "yes")
	u.addFolder("/downloads")
	settle(u)
	<-answered
	if u.st.Journal != "/journal/before.json" || u.st.LibraryChanged {
		t.Errorf("journal %q changed=%v", u.st.Journal, u.st.LibraryChanged)
	}
	if !strings.Contains(strings.Join(u.st.Log, "\n"), "did not match") {
		t.Errorf("the failure is not in the log:\n%s", strings.Join(u.st.Log, "\n"))
	}
}

// The three ways in: the menu item (greyed with no music folder or while a
// job runs), and the chooser it opens, whose folder goes to the sheet.
func TestAddMusicOpensTheChooserAndThenTheSheet(t *testing.T) {
	f := &fakeBackend{addPlan: cannedAddPlan()}
	u := newTestUI(f)
	if !u.menuEnabled(menuAddMusic) || menuLabels[menuAddMusic] != "Add music…" {
		t.Fatalf("the menu item: %q enabled=%v", menuLabels[menuAddMusic], u.menuEnabled(menuAddMusic))
	}
	u.st.Source = ""
	if u.menuEnabled(menuAddMusic) {
		t.Error("Add music… is offered with no music folder to add to")
	}
	u.st.Source = "/src"

	u.startAdd()
	if u.chooser == nil || u.chooser.kind.id != "add" || u.chooser.kind.title != "Choose a folder of music to add" {
		t.Fatalf("the chooser: %+v", u.chooser)
	}
	answered := answerDialogs(u, CancelAnswer)
	u.chooser.deliver("/downloads/rip")
	u.chooser = nil
	u.drain()
	settle(u)
	<-answered
	if len(f.planAddCalls) != 1 || f.planAddCalls[0][0] != "/downloads/rip" {
		t.Errorf("the chosen folder did not reach PlanAdd: %v", f.planAddCalls)
	}
}

// The header says how far it has got: "add music: adding 2 of 4 · 42%".
func TestAddProgressInTheHeader(t *testing.T) {
	u := newTestUI(&fakeBackend{})
	u.st.Job = &JobStatus{Kind: JobAdd, Pct: -1}
	u.st.Apply(Event{Kind: EventProgress, Job: JobAdd, Text: "adding 2 of 4", Pct: 0.42})
	if s, _ := u.statusLine(); s != "add music: adding 2 of 4  ·  42%" {
		t.Errorf("status %q", s)
	}
}

// The button at the right of the Library tab's row, and the one on the empty
// grid, both open the chooser.
func TestAddMusicButtons(t *testing.T) {
	for _, albums := range [][]Album{nil, {{Title: "Blue", Device: "Joni Mitchell - Blue"}}} {
		u := NewUI(Options{Backend: &fakeBackend{}})
		u.SetState(DemoState())
		u.st.Albums = albums
		u.st.Tab = TabAlbums
		u.Layout(newTestContext(900, 600))
		u.addBtn.Click()
		u.Layout(newTestContext(900, 600))
		if u.chooser == nil || u.chooser.kind.id != "add" {
			t.Errorf("with %d album(s): the button did not open the add chooser", len(albums))
		}
	}
}
