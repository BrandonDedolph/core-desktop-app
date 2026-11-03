package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
)

// Add music…: the front door to the library. A folder from anywhere — a
// ripped CD, a download, a USB stick — is read (PlanAdd), shown on one sheet,
// "Adding to your library", and copied in only when that sheet's button is
// pressed. Every file is copied, read back and checked against its original;
// only then, with the one tick on, are the originals deleted. The grid then
// shows the albums as NEW, the header says "Sync N albums", the Fix scan runs
// again and Undo takes it back the way it takes back a Fix.
//
// It is in the ··· menu, on the empty grid, and at the right of the Music
// tab's row.

// The sheet's words.
const (
	addTitle      = "Adding to your library"
	addOptionText = "Delete the originals once they are safely in the library"
	addNoteDelete = "Every file is copied, read back and checked against its original; the originals are " +
		"deleted only once every file of their album checked out. Nothing on the iPod changes."
	addNoteNone = "Nothing in this folder can be added: each album above says why."
	addButton   = "Add music…"
)

// startAdd is the menu item and the two buttons: the chooser in folder mode,
// then the sheet.
func (u *UI) startAdd() {
	if strings.TrimSpace(u.st.Source) == "" {
		u.st.Logf("add music: choose your music folder first (Fix tab), so the music has somewhere to go")
		return
	}
	u.openChooser(chooseAddKind, "", func(path string) {
		u.mu.Lock()
		u.newPath, u.pathFor = path, JobAdd
		u.mu.Unlock()
		u.invalidate()
	})
}

// addFolder reads src, shows the sheet, and adds what is ticked when its
// button is pressed. Cancel adds nothing.
func (u *UI) addFolder(src string) {
	lib := strings.TrimSpace(u.st.Source)
	after := u.st.Journal
	u.start(JobAdd, func(ctx context.Context, emit func(Event)) error {
		if lib == "" {
			return fmt.Errorf("no music folder: choose it on the Fix tab")
		}
		emit(Event{Kind: EventProgress, Text: "reading the tags in " + sourceName(src), Pct: -1})
		plan, err := u.be.PlanAdd(ctx, src, lib, emit)
		if err != nil {
			return err
		}
		req := addDialog(plan)
		answer, err := u.ask(ctx, req)
		if err != nil {
			return err
		}
		chosen := req.chosen(plan)
		if answer == CancelAnswer || len(chosen) == 0 {
			emit(Event{Kind: EventLog, Text: "add music: cancelled, nothing was copied"})
			return nil
		}
		res, err := u.be.Add(ctx, plan, chosen, req.optionOn, after, emit)
		if res != nil {
			u.mu.Lock()
			u.newAdd = res
			u.mu.Unlock()
			for _, f := range res.Failures {
				emit(Event{Kind: EventLog, Text: "add music: " + string(f.Album) + " — " + f.Reason})
			}
			if res.Journal != "" {
				emit(Event{Kind: EventLog, Text: "undo journal: " + res.Journal})
			}
		}
		return err
	})
}

// addDialog is the sheet for one plan: the source and the counts at the top,
// one ticked row per album (on, except the ones already there and the ones
// that cannot be added, which say why), the one tick for the originals, and
// "Add N albums".
func addDialog(plan *librarian.AddPlan) *dialogRequest {
	found := fmt.Sprintf("%d album%s · %d song%s", len(plan.Albums), plural(len(plan.Albums)),
		plan.Songs, plural(plan.Songs))
	if f := plan.FormatsText(); f != "" {
		found += " · " + f
	}
	if s := binarySize(plan.Bytes); s != "" {
		found += " · " + s
	}
	req := &dialogRequest{
		title:    addTitle,
		card:     []cardRow{{label: "From", value: plan.Source}, {label: "Found", value: found}, {label: "Into", value: plan.Library}},
		option:   addOptionText,
		optionOn: true,
		note:     addNoteDelete,
		okFor:    addOK,
	}
	can, there, cannot := 0, 0, 0
	for _, a := range plan.Albums {
		req.items = append(req.items, cardRow{label: a.Name,
			mid: fmt.Sprintf("%d song%s", a.Songs, plural(a.Songs)), value: binarySize(a.Bytes)})
		req.picks = append(req.picks, a.Ticked())
		req.pickable = append(req.pickable, a.CanAdd())
		req.pickWhy = append(req.pickWhy, a.Why())
		switch {
		case !a.CanAdd():
			cannot++
		case a.InLibrary != "":
			there++
		default:
			can++
		}
	}
	parts := []string{fmt.Sprintf("%d new to your library", can)}
	if there > 0 {
		parts = append(parts, fmt.Sprintf("%d already in it", there))
	}
	if cannot > 0 {
		parts = append(parts, fmt.Sprintf("%d that cannot be added", cannot))
	}
	req.total = strings.Join(parts, " · ")
	if can+there == 0 {
		req.note, req.option = addNoteNone, ""
	}
	return req
}

// addOK is the sheet's button: "Add 3 albums", off when nothing is ticked.
func addOK(d *dialogRequest) (string, bool) {
	n := 0
	for i, on := range d.picks {
		if on && i < len(d.pickable) && d.pickable[i] {
			n++
		}
	}
	return fmt.Sprintf("Add %d album%s", n, plural(n)), n > 0
}

// chosen is the keys of the ticked rows, in the plan's order.
func (d *dialogRequest) chosen(plan *librarian.AddPlan) []librarian.AlbumKey {
	var out []librarian.AlbumKey
	for i, a := range plan.Albums {
		if i < len(d.picks) && d.picks[i] && a.CanAdd() {
			out = append(out, a.Key)
		}
	}
	return out
}

// addDone folds a finished add into the model: the journal for Undo, the
// header's Sync, and the two read-only passes that make the grid show the
// albums as NEW and the Fix tab say what it says about them.
func (u *UI) addDone(res *librarian.AddResult) {
	if res.Journal != "" {
		u.st.Journal = res.Journal
	}
	n := res.Albums()
	if n == 0 {
		return
	}
	u.st.LibraryChanged = true
	u.st.Notice, u.st.NoticeAt = fmt.Sprintf("Added %d album%s to your library", n, plural(n)), u.now()
	src := u.st.Source
	u.later(func() { u.startInspect(src) })
	u.later(func() { u.startSync(JobDryRun) })
}
