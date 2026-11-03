package librarian

// edit.go is the album sheet's Edit…: tags written in the library, then the
// album renamed to match (charter job 3 — metadata is fixed in the library
// and then synced, never on the iPod).
//
// The order is Fix's, for Fix's reason: the tags go into the files FIRST,
// then the organizer renames the album from them. organizer's journal
// records each file's size and mtime as it moves it, and a tag write after
// the move would make that move un-undoable.
//
// The journal is a librarian journal (journal.go) with a Tags list: for each
// file, the values its edited keys had before — every value, aliases
// included, so a key that was repeated or spelled ALBUM ARTIST comes back
// exactly — and the file as it was after the write. Undo puts the names back
// first (organizer's half) and then the tag values, into the files that are
// by then back at the paths the tags were written at.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
	"github.com/BrandonDedolph/core-desktop-app/internal/id3"
	"github.com/BrandonDedolph/core-desktop-app/internal/organizer"
)

// JobEdit is the job name EditTags reports progress under.
const JobEdit = "edit"

// The fields an edit names. They are this package's words, not a format's:
// each maps onto the FLAC comment or the ID3 frame that carries it (fieldKeys).
const (
	TagTitle       = "title"
	TagArtist      = "artist"
	TagAlbum       = "album"
	TagAlbumArtist = "albumartist"
	TagGenre       = "genre"
	TagYear        = "year"
	TagTrack       = "track"
)

// TagFields are the fields EditTags writes.
var TagFields = []string{TagTitle, TagArtist, TagAlbum, TagAlbumArtist, TagGenre, TagYear, TagTrack}

// TagEdit is one file's change: field -> new value, "" to remove the field.
type TagEdit struct {
	Path string
	Set  map[string]string
}

// EditResult is what EditTags did.
type EditResult struct {
	// Journal undoes all of it (Undo), or "" when nothing was written.
	Journal string
	// Written is how many files had their tags rewritten.
	Written int
	// Renamed and Moved count the tracks that changed name in place and
	// the ones that changed folder, as Fix counts them.
	Renamed, Moved int
	// Dir is the album folder afterwards: the one it was, or the one the
	// organizer moved it to.
	Dir string
	// Moves is where each moved file went (old path -> new path).
	Moves map[string]string
	// Failures are the files whose tags could not be written; the rest
	// were.
	Failures []Failure
	// Attention is why a file of the album could not be renamed to match
	// its new tags (a name another file holds, a tag still missing).
	Attention []organizer.Attention
}

// fieldKeys is, per format, the keys a field is read from: the first is the
// one written, the rest are aliases the readers also honour, which go — or a
// stale ALBUM ARTIST would still win over the new ALBUMARTIST in a reader
// that asks for it first.
var fieldKeys = map[string]map[string][]string{
	"flac": {
		TagTitle:       {"title"},
		TagArtist:      {"artist"},
		TagAlbum:       {"album"},
		TagAlbumArtist: {"albumartist", "album artist", "album_artist"},
		TagGenre:       {"genre"},
		TagYear:        {"date", "year"},
		TagTrack:       {"tracknumber", "track"},
	},
	"mp3": {
		TagTitle:       {"title"},
		TagArtist:      {"artist"},
		TagAlbum:       {"album"},
		TagAlbumArtist: {"album_artist"},
		TagGenre:       {"genre"},
		TagYear:        {"date"},
		TagTrack:       {"track"},
	},
}

func formatOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		return "flac"
	case ".mp3":
		return "mp3"
	}
	return ""
}

// EditTags writes edits into the files of one album (albumDir, inside root)
// and then renames that album — its files, its folder, its sidecars — to
// match, journaled for Undo. after is the session's previous journal, which
// the new one names, so one Undo walks back through it too.
//
// Every edit is checked before anything is written: a path outside the
// album, a format the iPod does not play or a field this package does not
// know is an error and nothing changes. A file whose write fails (truncated,
// a tag the writer refuses) is a Failure and the others are still written.
//
// The rename is organizer's, limited to this album (organizer.Options.Albums),
// and it is FLAC-only, as the organizer always has been: an MP3's tags are
// written and its name stays.
func EditTags(ctx context.Context, root, albumDir string, edits []TagEdit, after string, o Options) (*EditResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	albumDir, err = filepath.Abs(albumDir)
	if err != nil {
		return nil, err
	}
	if !under(root, albumDir) || samePath(root, albumDir) {
		return nil, fmt.Errorf("edit: %s is not an album folder inside %s", albumDir, root)
	}
	var todo []TagEdit
	for _, e := range edits {
		if len(e.Set) == 0 {
			continue
		}
		p, err := filepath.Abs(e.Path)
		if err != nil {
			return nil, err
		}
		if !samePath(albumDirOf(p), albumDir) {
			return nil, fmt.Errorf("edit: %s is not in the album %s", p, albumDir)
		}
		if formatOf(p) == "" {
			return nil, fmt.Errorf("edit: %s is not a FLAC or an MP3", filepath.Base(p))
		}
		set := map[string]string{}
		for k, v := range e.Set {
			if _, ok := fieldKeys["flac"][k]; !ok {
				return nil, fmt.Errorf("edit: %q is not a field this can write", k)
			}
			set[k] = strings.TrimSpace(v)
		}
		if _, err := os.Stat(p); err != nil {
			return nil, err
		}
		todo = append(todo, TagEdit{Path: p, Set: set})
	}
	res := &EditResult{Dir: albumDir}
	if len(todo) == 0 {
		return res, nil
	}

	j, err := newFixJournal(root, o)
	if err != nil {
		return nil, err
	}
	j.Format = tagJournalFormat
	if after != "" && after != j.Path {
		j.Previous = after
	}
	res.Journal = j.Path
	defer func() {
		_ = j.write()
		j.close()
	}()

	key := AlbumKey(rel(root, albumDir))
	for i, e := range todo {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		o.progress(JobEdit, "writing the tags of "+filepath.Base(e.Path), i, len(todo))
		if err := j.writeTags(key, e); err != nil {
			res.Failures = append(res.Failures, Failure{Album: key, Path: e.Path, Reason: err.Error()})
			o.log(JobEdit, "could not write the tags of %s: %v", filepath.Base(e.Path), err)
			continue
		}
		res.Written++
	}
	if res.Written == 0 {
		return res, errors.New("edit: no file's tags could be written")
	}

	plan, err := organizer.Scan(organizer.Options{Root: root, Meta: o.Meta, Albums: []string{albumDir},
		DiscFolders: o.DiscFolders})
	if err != nil {
		return res, err
	}
	res.Attention = plan.Attention
	for _, a := range plan.Attention {
		o.log(JobEdit, "not renamed: %s — %s", rel(root, a.Path), a.Reason)
	}
	if len(plan.Moves) == 0 {
		return res, nil
	}
	oj, err := organizer.Apply(ctx, plan, organizer.Options{
		Root:       root,
		Meta:       o.Meta,
		JournalDir: o.JournalDir,
		Progress: func(e organizer.Event) {
			if e.Phase == "apply" {
				o.progress(JobEdit, e.Album, e.Done, e.Total)
			}
		},
	})
	if oj != nil {
		j.Moves = oj.Path
		_ = j.write()
		res.Moves = journalMoves(oj)
		var fix Result
		for _, op := range oj.Ops {
			if op.Done {
				countMove(&fix, op.Reason, op.From, op.To)
			}
		}
		res.Renamed, res.Moved = fix.Renamed, fix.Moved
		res.Dir = albumDirOf(follow(res.Moves, todo[0].Path))
	}
	if err != nil {
		return res, err
	}
	o.log(JobEdit, "renamed %d file(s), re-foldered %d to match the new tags", res.Renamed, res.Moved)
	return res, nil
}

// tagOp is one file whose tags an edit rewrote.
type tagOp struct {
	Album  AlbumKey `json:"album"`
	Path   string   `json:"path"`
	Format string   `json:"format"` // "flac" | "mp3"
	// Before is every value each touched key had, in the format's own key
	// names; a key with no values was not there, and Undo removes it.
	Before map[string][]string `json:"before"`
	// Size and MTimeNS are the file AFTER the write: Undo refuses a file
	// that has changed since.
	Size    int64 `json:"size"`
	MTimeNS int64 `json:"mtime_ns"`
	Done    bool  `json:"done"`
}

// writeTags journals one file's old values, writes the new ones, and stamps
// the result.
func (j *fixJournal) writeTags(key AlbumKey, e TagEdit) error {
	format := formatOf(e.Path)
	keys := fieldKeys[format]
	op := tagOp{Album: key, Path: e.Path, Format: format, Before: map[string][]string{}}

	switch format {
	case "flac":
		old, err := flac.ReadTagValues(e.Path)
		if err != nil {
			return err
		}
		values := map[string][]string{}
		for _, f := range sortedFields(e.Set) {
			v := e.Set[f]
			if f == TagTrack {
				v = keepTotal(v, first(old, keys[f]))
			}
			for i, k := range keys[f] {
				op.Before[k] = old[k]
				if i == 0 && v != "" {
					values[k] = []string{v}
				} else {
					values[k] = nil
				}
			}
		}
		if err := j.recordTag(op); err != nil {
			return err
		}
		if err := flac.WriteTagValues(e.Path, values); err != nil {
			j.Tags = j.Tags[:len(j.Tags)-1]
			_ = j.write()
			return err
		}
	case "mp3":
		m, err := id3.ReadFile(e.Path)
		if err != nil {
			return err
		}
		set := map[string]string{}
		for _, f := range sortedFields(e.Set) {
			k := keys[f][0]
			v := e.Set[f]
			if f == TagTrack {
				v = keepTotal(v, m.Tags[k])
			}
			if old, ok := m.Tags[k]; ok {
				op.Before[k] = []string{old}
			} else {
				op.Before[k] = nil
			}
			set[k] = v
		}
		if err := j.recordTag(op); err != nil {
			return err
		}
		if err := id3.WriteTags(e.Path, set); err != nil {
			j.Tags = j.Tags[:len(j.Tags)-1]
			_ = j.write()
			return err
		}
	}
	last := &j.Tags[len(j.Tags)-1]
	size, mt, err := stampOf(e.Path)
	if err != nil {
		return err
	}
	last.Size, last.MTimeNS, last.Done = size, mt, true
	return j.write()
}

// recordTag adds op to the journal and puts it on disk before the write it
// describes.
func (j *fixJournal) recordTag(op tagOp) error {
	j.Tags = append(j.Tags, op)
	return j.write()
}

// restoreTags is Undo's half for one tagOp.
func restoreTags(op tagOp) error {
	switch op.Format {
	case "flac":
		return flac.WriteTagValues(op.Path, op.Before)
	case "mp3":
		set := map[string]string{}
		for k, vs := range op.Before {
			set[k] = ""
			if len(vs) > 0 {
				set[k] = vs[0]
			}
		}
		return id3.WriteTags(op.Path, set)
	}
	return fmt.Errorf("unknown format %q", op.Format)
}

// keepTotal keeps a "/12" total on a track number when the new value does
// not give one: changing track 3 of 12 to 4 is "4/12", not "4".
func keepTotal(v, old string) string {
	if v == "" || strings.Contains(v, "/") {
		return v
	}
	if i := strings.IndexByte(old, '/'); i >= 0 && strings.TrimSpace(old[i+1:]) != "" {
		return v + "/" + strings.TrimSpace(old[i+1:])
	}
	return v
}

func first(values map[string][]string, keys []string) string {
	for _, k := range keys {
		if vs := values[k]; len(vs) > 0 && vs[0] != "" {
			return vs[0]
		}
	}
	return ""
}

func sortedFields(set map[string]string) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func under(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
