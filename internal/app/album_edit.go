package app

// album_edit.go is the album sheet's Edit…: the album's tags in fields, and
// Save, which writes them into the files in the music folder and renames the
// album to match (librarian.EditTags). Charter job 3: metadata is fixed in
// the library and then synced — nothing here touches the iPod, and the next
// sync copies the retagged files because their mtimes moved.
//
// Only what was changed is written. The editor opens on the files' own
// values; a field left as it opened is not part of the edit, so an album
// whose tracks disagree about the genre keeps each track's genre unless the
// genre field is typed into. Every other tag in the files — ReplayGain, the
// cover, the MusicBrainz ids — is the writers' business to keep (and their
// tests' to prove).

import (
	"context"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
	"github.com/BrandonDedolph/core-desktop-app/internal/id3"
	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
)

// albumTags is an album's tags as the editor opens on them.
type albumTags struct {
	Album, AlbumArtist, Genre, Year string
	// Mixed are the album fields whose tracks disagree; such a field opens
	// empty and is written only when something is typed into it.
	Mixed  map[string]bool
	Tracks []trackTags
}

// trackTags is one track's three fields.
type trackTags struct {
	Path                  string
	Title, Artist, Number string
}

// readAlbumTags is ReadAlbumTags, a variable for the tests.
var readAlbumTags = ReadAlbumTags

// ReadAlbumTags reads the editor's fields off the sheet's tracks. A file
// whose tags cannot be read opens with empty fields, and the error says
// which one; a tracklist with no files (a canned state) is an error.
func ReadAlbumTags(tracks []Track) (albumTags, error) {
	at := albumTags{Mixed: map[string]bool{}}
	var firstErr error
	album := map[string][]string{}
	for _, t := range tracks {
		if t.Path == "" {
			return at, errors.New("this album has no files to edit")
		}
		tt := trackTags{Path: t.Path}
		var get func(keys ...string) string
		if strings.EqualFold(filepath.Ext(t.Path), ".mp3") {
			m, err := id3.ReadFile(t.Path)
			if err != nil {
				firstErr = errors.Join(firstErr, err)
				m = &id3.Meta{}
			}
			get = m.Tag
		} else {
			m, err := flac.ReadFile(t.Path)
			if err != nil {
				firstErr = errors.Join(firstErr, err)
				m = &flac.Meta{}
			}
			get = m.Tag
		}
		tt.Title = strings.TrimSpace(get("title"))
		tt.Artist = strings.TrimSpace(get("artist"))
		tt.Number = trackNumber(get("tracknumber", "track"))
		at.Tracks = append(at.Tracks, tt)
		album[librarian.TagAlbum] = append(album[librarian.TagAlbum], strings.TrimSpace(get("album")))
		album[librarian.TagAlbumArtist] = append(album[librarian.TagAlbumArtist],
			strings.TrimSpace(get("albumartist", "album_artist", "album artist")))
		album[librarian.TagGenre] = append(album[librarian.TagGenre], strings.TrimSpace(get("genre")))
		album[librarian.TagYear] = append(album[librarian.TagYear], strings.TrimSpace(get("date", "year")))
	}
	one := func(field string) string {
		vs := album[field]
		for _, v := range vs[1:] {
			if v != vs[0] {
				at.Mixed[field] = true
				return ""
			}
		}
		if len(vs) == 0 {
			return ""
		}
		return vs[0]
	}
	at.Album = one(librarian.TagAlbum)
	at.AlbumArtist = one(librarian.TagAlbumArtist)
	at.Genre = one(librarian.TagGenre)
	at.Year = one(librarian.TagYear)
	return at, firstErr
}

// trackNumber is the number part of "3/12".
func trackNumber(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// albumEdit is the editor while it is open. UI goroutine only.
type albumEdit struct {
	orig                            albumTags
	album, albumArtist, genre, year widget.Editor
	rows                            []trackEdit
	list                            widget.List
	saveBtn, cancelBtn              widget.Clickable
	// note is the line in the foot: a refusal ("Track numbers are whole
	// numbers") or why the save failed.
	note string
	// saving is true from Save to the job's answer.
	saving bool
}

type trackEdit struct{ title, artist, number widget.Editor }

func newAlbumEdit(t albumTags) *albumEdit {
	e := &albumEdit{orig: t}
	e.list.Axis = layout.Vertical
	set := func(ed *widget.Editor, v string) {
		ed.SingleLine = true
		ed.SetText(v)
	}
	set(&e.album, t.Album)
	set(&e.albumArtist, t.AlbumArtist)
	set(&e.genre, t.Genre)
	set(&e.year, t.Year)
	e.rows = make([]trackEdit, len(t.Tracks))
	for i, tt := range t.Tracks {
		set(&e.rows[i].title, tt.Title)
		set(&e.rows[i].artist, tt.Artist)
		set(&e.rows[i].number, tt.Number)
	}
	return e
}

// edits is the change, file by file: only the fields that differ from what
// the editor opened on. err is a value that cannot be written.
func (e *albumEdit) edits() ([]librarian.TagEdit, error) {
	albumSet := map[string]string{}
	for _, f := range []struct {
		field string
		ed    *widget.Editor
		was   string
	}{
		{librarian.TagAlbum, &e.album, e.orig.Album},
		{librarian.TagAlbumArtist, &e.albumArtist, e.orig.AlbumArtist},
		{librarian.TagGenre, &e.genre, e.orig.Genre},
		{librarian.TagYear, &e.year, e.orig.Year},
	} {
		now := strings.TrimSpace(f.ed.Text())
		if e.orig.Mixed[f.field] && now == "" {
			continue // left alone: each track keeps its own
		}
		if now != f.was {
			albumSet[f.field] = now
		}
	}
	if v, ok := albumSet[librarian.TagAlbum]; ok && v == "" {
		return nil, errors.New("An album needs a title.")
	}
	var out []librarian.TagEdit
	for i, tt := range e.orig.Tracks {
		set := map[string]string{}
		for k, v := range albumSet {
			set[k] = v
		}
		r := &e.rows[i]
		if v := strings.TrimSpace(r.title.Text()); v != tt.Title {
			if v == "" {
				return nil, fmt.Errorf("Track %d needs a title.", i+1)
			}
			set[librarian.TagTitle] = v
		}
		if v := strings.TrimSpace(r.artist.Text()); v != tt.Artist {
			set[librarian.TagArtist] = v
		}
		if v := strings.TrimSpace(r.number.Text()); v != tt.Number {
			if n, err := strconv.Atoi(v); err != nil || n <= 0 || n > 999 {
				return nil, fmt.Errorf("Track numbers are whole numbers from 1 to 999 (row %d says %q).", i+1, v)
			}
			set[librarian.TagTrack] = v
		}
		if len(set) > 0 && tt.Path != "" { // a canned state's rows have no file
			out = append(out, librarian.TagEdit{Path: tt.Path, Set: set})
		}
	}
	return out, nil
}

// tagsRead is ReadAlbumTags's answer for one sheet, crossing back under mu.
type tagsRead struct {
	dir  string
	tags albumTags
	err  error
}

// editResult is the Save job's answer.
type editResult struct {
	dir   string // the album folder the edit was made in
	edits []librarian.TagEdit
	res   *librarian.EditResult
	err   error
}

// startEdit is Edit…: the fields are read off the files on a goroutine and
// the editor opens when they land.
func (u *UI) startEdit() {
	s := u.sheet
	if s == nil || !s.loaded || len(s.tracks) == 0 || s.editLoading {
		return
	}
	s.editLoading, s.note = true, ""
	tracks, dir, read := s.tracks, s.album.Dir, readAlbumTags
	go func() {
		t, err := read(tracks)
		u.mu.Lock()
		u.newTags = &tagsRead{dir: dir, tags: t, err: err}
		u.mu.Unlock()
		u.invalidate()
	}()
}

// applyTagsRead opens the editor on what was read.
func (u *UI) applyTagsRead(r *tagsRead) {
	s := u.sheet
	if s == nil || s.album.Dir != r.dir {
		return
	}
	s.editLoading = false
	if len(r.tags.Tracks) == 0 {
		msg := "nothing to edit"
		if r.err != nil {
			msg = r.err.Error()
		}
		s.note = "Could not read the tags: " + msg
		return
	}
	if r.err != nil {
		u.st.Logf("edit: some tags could not be read: %v", r.err)
	}
	s.edit = newAlbumEdit(r.tags)
}

// cancelEdit is Cancel (and Esc): the fields go, nothing is written.
func (u *UI) cancelEdit() {
	if s := u.sheet; s != nil && s.edit != nil && !s.edit.saving {
		s.edit = nil
	}
}

// saveEdit is Save: the change is worked out on this goroutine, the player
// lets go of a file about to be rewritten, and the job writes.
func (u *UI) saveEdit() {
	s := u.sheet
	if s == nil || s.edit == nil || s.edit.saving {
		return
	}
	e := s.edit
	edits, err := e.edits()
	if err != nil {
		e.note = err.Error()
		return
	}
	if len(edits) == 0 {
		s.edit, s.note = nil, "Nothing was changed."
		return
	}
	root := strings.TrimSpace(u.st.Source)
	if root == "" {
		e.note = "Choose the music folder on the Fix tab first."
		return
	}
	// A file the player has open is not one to rewrite under it (and on
	// Windows it cannot be renamed at all): the music stops, and says so.
	stopped := ""
	if np := u.st.NowPlaying; np != nil && u.npLive && !np.Ended &&
		np.TrackIndex >= 0 && np.TrackIndex < len(np.Album.TrackList) {
		playing := np.Album.TrackList[np.TrackIndex].Path
		for _, ed := range edits {
			if playing != "" && samePathUI(ed.Path, playing) {
				stopped = np.Album.TrackList[np.TrackIndex].Title
				u.stopPlaying()
				u.st.Logf("stopped playing %s: its tags are being rewritten", filepath.Base(playing))
				break
			}
		}
	}
	e.saving, e.note = true, ""
	s.stopped = stopped
	dir, after := s.album.Dir, u.st.Journal
	u.start(JobEditTags, func(ctx context.Context, emit func(Event)) error {
		emit(Event{Kind: EventProgress, Text: "writing the tags", Pct: -1})
		res, err := u.be.EditTags(ctx, root, albumRoot(dir), edits, after, emit)
		if res != nil {
			for _, f := range res.Failures {
				emit(Event{Kind: EventLog, Text: "could not write " + filepath.Base(f.Path) + ": " + f.Reason})
			}
			if res.Written > 0 {
				emit(Event{Kind: EventLog, Text: fmt.Sprintf("tags written to %d file%s; %d renamed, %d moved to match",
					res.Written, plural(res.Written), res.Renamed, res.Moved)})
			}
			if res.Journal != "" {
				emit(Event{Kind: EventLog, Text: "undo journal: " + res.Journal})
			}
		}
		u.mu.Lock()
		u.newEdit = &editResult{dir: dir, edits: edits, res: res, err: err}
		u.mu.Unlock()
		return err
	})
}

// foldCase is whether the filesystem ignores case in names.
var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// samePathUI is a path comparison that ignores case where the filesystem
// does.
func samePathUI(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if foldCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// editSaved folds the Save job's answer in: the sheet shows the album as it
// now is, the grid marks it CHANGED (the files' mtimes moved, so the next
// preview says so too), Undo knows the journal, and the library is scanned
// again so the Fix tab and the sync plan describe the renamed tree.
func (u *UI) editSaved(r *editResult) {
	s := u.sheet
	if r.err != nil && (r.res == nil || r.res.Written == 0) {
		if s != nil && s.edit != nil && s.album.Dir == r.dir {
			s.edit.saving = false
			s.edit.note = "Could not save: " + firstLine(r.err.Error())
		}
		return
	}
	res := r.res
	if res.Journal != "" {
		u.st.Journal = res.Journal
	}
	u.st.LibraryChanged = true
	newDir := res.Dir
	if newDir == "" {
		newDir = r.dir
	}
	var updated *Album
	for i := range u.st.Albums {
		a := &u.st.Albums[i]
		if a.Dir != r.dir && albumRoot(a.Dir) != albumRoot(r.dir) {
			continue
		}
		a.State = TileChanged
		if a.Copy < res.Written {
			a.Copy = res.Written
		}
		a.Changed = a.Copy - a.New
		a.Dir = newDir
		a.Title = retitled(a.Title, r.edits)
		updated = a
		break
	}
	if s == nil || s.album.Dir != r.dir {
		u.laterRescan()
		return
	}
	if updated != nil {
		s.album = *updated
	} else {
		s.album.Dir, s.album.Title = newDir, retitled(s.album.Title, r.edits)
	}
	s.album.TrackList = nil
	s.edit = nil
	s.note = savedNote(res, s.stopped)
	s.stopped = ""
	// The album re-read: the new titles, numbers and order.
	s.loaded, s.tracks, s.rows = false, nil, nil
	dir, read := s.album.Dir, readTracks
	go func() {
		ts, err := read(dir)
		u.mu.Lock()
		u.newTracks = &tracksResult{dir: dir, tracks: ts, err: err}
		u.mu.Unlock()
		u.invalidate()
	}()
	u.laterRescan()
}

// laterRescan queues the two read-only passes a change to the library
// needs: the Fix tab's scan, and the sync preview when an iPod is there.
func (u *UI) laterRescan() {
	src := u.st.Source
	u.later(func() { u.startInspect(src) })
	if u.st.Device.Volume != "" {
		u.later(func() { u.startSync(JobDryRun) })
	}
}

// retitled is the tile's "Artist - Album" with an edit's album title and
// album artist in it.
func retitled(title string, edits []librarian.TagEdit) string {
	if len(edits) == 0 {
		return title
	}
	set := edits[0].Set
	artist, album := title, ""
	if i := strings.Index(title, " - "); i > 0 {
		artist, album = title[:i], title[i+3:]
	}
	if v, ok := set[librarian.TagAlbum]; ok && v != "" {
		album = v
	}
	if v, ok := set[librarian.TagAlbumArtist]; ok && v != "" {
		artist = v
	}
	if album == "" {
		return artist
	}
	return artist + " - " + album
}

// savedNote is the foot's line after a save.
func savedNote(res *librarian.EditResult, stopped string) string {
	parts := []string{fmt.Sprintf("Saved: %d file%s retagged", res.Written, plural(res.Written))}
	if n := res.Renamed + res.Moved; n > 0 {
		parts = append(parts, fmt.Sprintf("%d renamed to match", n))
	}
	if n := len(res.Failures); n > 0 {
		parts = append(parts, fmt.Sprintf("%d could not be written (see the log)", n))
	}
	s := strings.Join(parts, ", ") + ". The next sync copies them; Undo on the Fix tab takes it back."
	if stopped != "" {
		s = "Stopped “" + stopped + "” to rewrite it. " + s
	}
	return s
}

// --- drawing --------------------------------------------------------------

// Editor geometry.
const (
	editFieldH  = 32 // one field
	editLabelH  = 18 // the small label above an album field
	editRowGap  = 6
	editHeadH   = 22 // "#  Title  Artist"
	editNumberW = 46
)

// editHeight is the editor's natural height: two rows of album fields, the
// column heads, one row per track.
func (u *UI) editHeight(gtx C) int {
	e := u.sheet.edit
	albumRow := gtx.Dp(editLabelH + editFieldH + editRowGap)
	return 2*albumRow + gtx.Dp(10) + gtx.Dp(editHeadH) + len(e.rows)*gtx.Dp(editFieldH+editRowGap)
}

// editForm is the fields, scrolling as the tracklist does.
func (u *UI) editForm(gtx C) D {
	e := u.sheet.edit
	n := 3 + len(e.rows) // two album rows, the heads, the tracks
	list := material.List(u.th, &e.list)
	list.Indicator.Color = u.pal.SelSub
	return list.Layout(gtx, n, func(gtx C, i int) D {
		// Room on the right for the scroll indicator.
		return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx C) D { return u.editItem(gtx, i) })
	})
}

// editItem is one line of the form.
func (u *UI) editItem(gtx C, i int) D {
	e := u.sheet.edit
	{
		switch i {
		case 0:
			return u.albumFieldRow(gtx, "Album", &e.album, e.hint(librarian.TagAlbum, "Album title"),
				"Album artist", &e.albumArtist, e.hint(librarian.TagAlbumArtist, "Same as the tracks' artist"))
		case 1:
			d := u.albumFieldRow(gtx, "Genre", &e.genre, e.hint(librarian.TagGenre, "Genre"),
				"Year", &e.year, e.hint(librarian.TagYear, "Year"))
			d.Size.Y += gtx.Dp(10)
			return d
		case 2:
			return u.editHeads(gtx)
		}
		return u.trackEditRow(gtx, i-3)
	}
}

// hint is a field's grey text: what it is, or that the tracks disagree.
func (e *albumEdit) hint(field, plain string) string {
	if e.orig.Mixed[field] {
		return "Differs between tracks — type to set all"
	}
	return plain
}

// albumFieldRow is two labelled fields side by side.
func (u *UI) albumFieldRow(gtx C, l1 string, e1 *widget.Editor, h1, l2 string, e2 *widget.Editor, h2 string) D {
	h := gtx.Dp(editLabelH + editFieldH + editRowGap)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	labelled := func(label string, ed *widget.Editor, hint string) layout.Widget {
		return func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(editLabelH)))
					l := material.Label(u.th, unit.Sp(11), label)
					l.Font.Weight = font.SemiBold
					l.Color = u.pal.Muted
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Max.Y = gtx.Dp(editFieldH)
					return u.field(gtx, ed, hint)
				}),
			)
		}
	}
	layout.Flex{}.Layout(gtx,
		layout.Flexed(1, labelled(l1, e1, h1)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Flexed(1, labelled(l2, e2, h2)),
	)
	return D{Size: gtx.Constraints.Max}
}

// editHeads is the column heads over the track fields.
func (u *UI) editHeads(gtx C) D {
	h := gtx.Dp(editHeadH)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	head := func(s string) layout.Widget {
		return func(gtx C) D {
			l := material.Label(u.th, unit.Sp(11), s)
			l.Font.Weight = font.SemiBold
			l.Color = u.pal.Muted
			return l.Layout(gtx)
		}
	}
	layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(editNumberW), h))
			return head("#")(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Flexed(3, head("Title")),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Flexed(2, head("Artist")),
	)
	return D{Size: gtx.Constraints.Max}
}

// trackEditRow is one track's number, title and artist.
func (u *UI) trackEditRow(gtx C, i int) D {
	r := &u.sheet.edit.rows[i]
	h := gtx.Dp(editFieldH + editRowGap)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	fieldIn := func(ed *widget.Editor, hint string) layout.Widget {
		return func(gtx C) D {
			gtx.Constraints.Max.Y = gtx.Dp(editFieldH)
			return u.field(gtx, ed, hint)
		}
	}
	layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(editNumberW), gtx.Dp(editFieldH)))
			return u.field(gtx, &r.number, "#")
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Flexed(3, fieldIn(&r.title, "Title")),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Flexed(2, fieldIn(&r.artist, "Artist")),
	)
	return D{Size: gtx.Constraints.Max}
}

// editFoot is the editor's foot: the note, Cancel, Save.
func (u *UI) editFoot(gtx C) D {
	e := u.sheet.edit
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	busy := e.saving || u.st.Busy()
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			var d D
			switch {
			case e.note != "":
				d = u.wrapN(12, u.pal.Accent, e.note, 2)(gtx)
			case e.saving:
				d = u.text(12, u.pal.Muted2, "Saving…")(gtx)
			default:
				d = u.wrapN(12, u.pal.Muted2, editHint, 2)(gtx)
			}
			d.Size.X = gtx.Constraints.Max.X
			return d
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx C) D {
			return u.buttonRow(gtx, btn{&e.cancelBtn, "Cancel", false, !e.saving},
				btn{&e.saveBtn, "Save", true, !busy})
		}),
	)
}

// editHint is the foot's line while the fields are open.
const editHint = "Save writes the tags into the files in your music folder and renames them to match. " +
	"The iPod gets them on the next sync."

// editEvents is the editor's half of the sheet's frame.
func (u *UI) editEvents(gtx C) {
	e := u.sheet.edit
	if e.cancelBtn.Clicked(gtx) {
		u.cancelEdit()
		return
	}
	if e.saveBtn.Clicked(gtx) {
		u.saveEdit()
	}
}
