package librarian

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
	"github.com/BrandonDedolph/core-desktop-app/internal/id3"
	"github.com/BrandonDedolph/core-desktop-app/internal/organizer"
)

// flacAudio hashes a FLAC file's audio frames: everything after the last
// metadata block, which no tag edit may change.
func flacAudio(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := 4
	for {
		last := b[p]&0x80 != 0
		n := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
		p += 4 + n
		if last {
			return sha256.Sum256(b[p:])
		}
	}
}

// editAlbum is a canonical three-track album as a ripper leaves it: the
// tags, ReplayGain, a cover, and — on track 3 — no padding at all, so its
// edit is a rewrite while the other two land in place.
func editAlbum(t *testing.T) (root, dir string, files []string) {
	t.Helper()
	root = t.TempDir()
	dir = filepath.Join(root, "Remain in Light - Talking Heads")
	titles := []string{"Born Under Punches", "Crosseyed and Painless", "The Great Curve"}
	for i, title := range titles {
		tags := map[string]string{
			"ALBUM": "Remain in Light", "ARTIST": "Talking Heads", "TITLE": title,
			"TRACKNUMBER": string(rune('1'+i)) + "/8", "DATE": "1980", "GENRE": "New Wave",
			"REPLAYGAIN_TRACK_GAIN": "-7.10 dB", "REPLAYGAIN_ALBUM_GAIN": "-7.80 dB",
		}
		pad := 4096
		if i == 2 {
			pad = 0
		}
		raw := flac.BuildFileP(flac.StreamInfo{SampleRate: 44100, Channels: 2, BitsPerSample: 16, TotalSamples: 4410},
			tags, []flac.Picture{{Type: flac.PictureTypeFrontCover, MIME: "image/png", Data: pngSquare(t, 32)}}, pad)
		name := organizer.CanonicalFile("Talking Heads", title, i+1, 2)
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	if err := os.WriteFile(filepath.Join(dir, "folder.art"), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, dir, files
}

// TestEditTagsWritesRenamesAndUndoes is the slice end to end: the album
// title and one track's title change, the tags land with everything else
// in the files kept (ReplayGain, the cover, the audio bytes), the folder and
// the file follow the new tags, the sidecar follows the folder — and Undo
// puts the names back and then the old values, so the files that were
// edited in place are byte for byte what they were.
func TestEditTagsWritesRenamesAndUndoes(t *testing.T) {
	root, dir, files := editAlbum(t)
	o := opts(t)
	before := tree(t, root)
	audio := map[string][32]byte{}
	for _, f := range files {
		audio[filepath.Base(f)] = flacAudio(t, f)
	}

	var edits []TagEdit
	for _, f := range files {
		edits = append(edits, TagEdit{Path: f, Set: map[string]string{TagAlbum: "Remain in Light (Deluxe)"}})
	}
	edits[1].Set[TagTitle] = "Crosseyed & Painless"
	edits[1].Set[TagTrack] = "2"
	res, err := EditTags(context.Background(), root, dir, edits, "", o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 3 || res.Journal == "" {
		t.Fatalf("result %+v", res)
	}
	newDir := filepath.Join(root, "Remain in Light (Deluxe) - Talking Heads")
	if res.Dir != newDir {
		t.Errorf("Dir = %s, want %s", res.Dir, newDir)
	}
	want := []string{
		"01 - Talking Heads - Born Under Punches.flac",
		"02 - Talking Heads - Crosseyed & Painless.flac",
		"03 - Talking Heads - The Great Curve.flac",
		"folder.art",
	}
	got := tree(t, root)
	if len(got) != len(want) {
		t.Errorf("tree %v", got)
	}
	for _, w := range want {
		if _, ok := got["Remain in Light (Deluxe) - Talking Heads/"+w]; !ok {
			t.Errorf("missing %s in %v", w, got)
		}
	}
	for i, w := range want[:3] {
		p := filepath.Join(newDir, w)
		m, err := flac.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if m.Tag("album") != "Remain in Light (Deluxe)" || m.Tag("replaygain_track_gain") != "-7.10 dB" ||
			m.Tag("replaygain_album_gain") != "-7.80 dB" || m.Tag("genre") != "New Wave" || m.FrontCover() == nil {
			t.Errorf("%s: %v (cover %v)", w, m.Tags, m.FrontCover() != nil)
		}
		if flacAudio(t, p) != audio[filepath.Base(files[i])] {
			t.Errorf("%s: the audio changed", w)
		}
	}
	m, _ := flac.ReadFile(filepath.Join(newDir, want[1]))
	if m.Tag("title") != "Crosseyed & Painless" || m.Tag("tracknumber") != "2/8" {
		t.Errorf("track 2: title %q, number %q (the /8 total is kept)", m.Tag("title"), m.Tag("tracknumber"))
	}

	// The journal says format 2 and holds the old values.
	raw, _ := os.ReadFile(res.Journal)
	var j fixJournal
	if err := json.Unmarshal(raw, &j); err != nil || j.Format != tagJournalFormat || len(j.Tags) != 3 || j.Moves == "" {
		t.Fatalf("journal %s: %v", raw, err)
	}

	rep, err := Undo(context.Background(), res.Journal, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 0 {
		t.Errorf("skipped %+v", rep.Skipped)
	}
	after := tree(t, root)
	for name, sum := range before {
		if strings.HasSuffix(name, "03 - Talking Heads - The Great Curve.flac") {
			continue // rewritten with fresh padding: same tags, not the same bytes
		}
		if after[name] != sum {
			t.Errorf("%s: not put back byte for byte (%q -> %q)", name, sum, after[name])
		}
	}
	if len(after) != len(before) {
		t.Errorf("after undo %v\nbefore %v", after, before)
	}
	vals, err := flac.ReadTagValues(files[2])
	if err != nil {
		t.Fatal(err)
	}
	if vals["album"][0] != "Remain in Light" || vals["replaygain_track_gain"][0] != "-7.10 dB" || flacAudio(t, files[2]) != audio[filepath.Base(files[2])] {
		t.Errorf("track 3 after undo: %v", vals)
	}
}

// The album artist is written as ALBUMARTIST and the aliases a reader might
// prefer go; Undo brings the alias back exactly as it was.
func TestEditTagsClearsAliasesAndUndoRestoresThem(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Remain in Light - Talking Heads")
	p := writeTrack(t, dir, "01 - Talking Heads - Born Under Punches.flac", map[string]string{
		"album": "Remain in Light", "artist": "Talking Heads", "title": "Born Under Punches",
		"tracknumber": "1", "album artist": "The Heads"}, nil)
	o := opts(t)
	res, err := EditTags(context.Background(), root, dir, []TagEdit{{Path: p, Set: map[string]string{TagAlbumArtist: "Talking Heads"}}}, "", o)
	if err != nil {
		t.Fatal(err)
	}
	vals, _ := flac.ReadTagValues(p)
	if vals["albumartist"][0] != "Talking Heads" || len(vals["album artist"]) != 0 {
		t.Errorf("after: %v", vals)
	}
	if _, err := Undo(context.Background(), res.Journal, o); err != nil {
		t.Fatal(err)
	}
	vals, _ = flac.ReadTagValues(p)
	if len(vals["albumartist"]) != 0 || vals["album artist"][0] != "The Heads" {
		t.Errorf("after undo: %v", vals)
	}
}

// Every edit is checked first: a file of another album, a field nobody
// knows, a file that is not there — refused, and nothing is written, not
// even a journal.
func TestEditTagsRefusesBeforeWriting(t *testing.T) {
	root, dir, files := editAlbum(t)
	other := writeTrack(t, filepath.Join(root, "Other - Someone"), "01 - Someone - Song.flac",
		map[string]string{"album": "Other", "artist": "Someone", "title": "Song", "tracknumber": "1"}, nil)
	before := tree(t, root)
	cases := map[string][]TagEdit{
		"another album": {{Path: files[0], Set: map[string]string{TagTitle: "x"}}, {Path: other, Set: map[string]string{TagTitle: "x"}}},
		"unknown field": {{Path: files[0], Set: map[string]string{"lyrics": "x"}}},
		"missing file":  {{Path: filepath.Join(dir, "99 - nope.flac"), Set: map[string]string{TagTitle: "x"}}},
		"not audio":     {{Path: filepath.Join(dir, "folder.art"), Set: map[string]string{TagTitle: "x"}}},
	}
	for name, edits := range cases {
		o := opts(t)
		if _, err := EditTags(context.Background(), root, dir, edits, "", o); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if j := journalsIn(t, o.JournalDir); len(j) != 0 {
			t.Errorf("%s: a journal was written: %v", name, j)
		}
		if !sameTree(tree(t, root), before) {
			t.Errorf("%s: the tree changed", name)
		}
	}
	if _, err := EditTags(context.Background(), root, root, nil, "", opts(t)); err == nil {
		t.Error("the library root was accepted as an album")
	}
}

// A file the writer refuses (cut short) is a Failure and the others are
// still written and renamed.
func TestEditTagsCarriesOnPastABrokenFile(t *testing.T) {
	root, dir, files := editAlbum(t)
	raw, _ := os.ReadFile(files[1])
	cut := raw[:200]
	if err := os.WriteFile(files[1], cut, 0o644); err != nil {
		t.Fatal(err)
	}
	var edits []TagEdit
	for _, f := range files {
		edits = append(edits, TagEdit{Path: f, Set: map[string]string{TagGenre: "Funk"}})
	}
	res, err := EditTags(context.Background(), root, dir, edits, "", opts(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 2 || len(res.Failures) != 1 || res.Failures[0].Path != files[1] {
		t.Fatalf("result %+v", res)
	}
	if b, _ := os.ReadFile(files[1]); !bytes.Equal(b, cut) {
		t.Error("the broken file was modified")
	}
	if m, _ := flac.ReadFile(files[0]); m.Tag("genre") != "Funk" {
		t.Errorf("track 1 genre %q", m.Tag("genre"))
	}
}

// An MP3 album: the tags are written and Undo puts them back; the names
// stay, because the organizer renames FLACs only.
func TestEditTagsOnAnMP3(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Talking Heads 77 - Talking Heads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "11 - Talking Heads - Pulled Up.mp3")
	tag := []byte("ID3\x03\x00\x00\x00\x00\x00\x00")
	frame := func(id, text string) {
		body := append([]byte{0}, text...)
		n := len(body)
		tag = append(tag, id...)
		tag = append(tag, byte(n>>24), byte(n>>16), byte(n>>8), byte(n), 0, 0)
		tag = append(tag, body...)
	}
	frame("TIT2", "Pulled Up")
	frame("TALB", "Talking Heads: 77")
	frame("TRCK", "11/11")
	tag = append(tag, make([]byte, 256)...)
	size := len(tag) - 10
	tag[6], tag[7], tag[8], tag[9] = byte(size>>21)&0x7f, byte(size>>14)&0x7f, byte(size>>7)&0x7f, byte(size)&0x7f
	for i := 0; i < 20; i++ {
		f := make([]byte, 417)
		f[0], f[1], f[2], f[3] = 0xff, 0xfb, 0x90, 0x00
		tag = append(tag, f...)
	}
	if err := os.WriteFile(p, tag, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	o := opts(t)
	res, err := EditTags(context.Background(), root, dir, []TagEdit{{Path: p, Set: map[string]string{
		TagAlbum: "77", TagTrack: "1", TagGenre: "Punk"}}}, "", o)
	if err != nil {
		t.Fatal(err)
	}
	m, err := id3.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Tags["album"] != "77" || m.Tags["track"] != "1/11" || m.Tags["genre"] != "Punk" || m.Tags["title"] != "Pulled Up" {
		t.Errorf("after: %v", m.Tags)
	}
	if res.Dir != dir || res.Renamed+res.Moved != 0 {
		t.Errorf("an MP3 was renamed: %+v", res)
	}
	if _, err := Undo(context.Background(), res.Journal, o); err != nil {
		t.Fatal(err)
	}
	m, _ = id3.ReadFile(p)
	if m.Tags["album"] != "Talking Heads: 77" || m.Tags["track"] != "11/11" || m.Tags["genre"] != "" {
		t.Errorf("after undo: %v", m.Tags)
	}
	if after, _ := os.ReadFile(p); len(after) != len(before) || !bytes.Equal(after[len(after)-20*417:], before[len(before)-20*417:]) {
		t.Error("the audio moved")
	}
}

// A journal written before Edit… existed — format 1, no "tags" — still
// replays: it is undone exactly as it always was.
func TestAnOldJournalStillReplays(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Remain in Light - Talking Heads")
	p := writeTrack(t, dir, "junk name.flac", map[string]string{
		"album": "Remain in Light", "artist": "Talking Heads", "title": "Houses in Motion", "tracknumber": "6"}, nil)
	jdir := t.TempDir()
	plan, err := organizer.Scan(organizer.Options{Root: root})
	if err != nil || len(plan.Moves) != 1 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	oj, err := organizer.Apply(context.Background(), plan, organizer.Options{Root: root, JournalDir: jdir})
	if err != nil {
		t.Fatal(err)
	}
	// Byte for byte the shape a pre-L5 build wrote: no tags key.
	old := `{
  "kind": "core-fix",
  "format": 1,
  "root": ` + jsonString(root) + `,
  "when": "2026-10-31T12:00:00Z",
  "moves": ` + jsonString(oj.Path) + `
}
`
	jpath := filepath.Join(jdir, "20261031-120000-fix.json")
	if err := os.WriteFile(jpath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Undo(context.Background(), jpath, Options{JournalDir: jdir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Undone != 1 || len(rep.Skipped) != 0 {
		t.Errorf("report %+v", rep)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("the rename was not undone: %v", err)
	}

	// A format this build does not know is refused, not half-replayed.
	future := strings.Replace(old, `"format": 1`, `"format": 3`, 1)
	if err := os.WriteFile(jpath, []byte(future), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(context.Background(), jpath, Options{JournalDir: jdir}); err == nil {
		t.Error("a format 3 journal was accepted")
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestKeepTotal(t *testing.T) {
	for _, c := range [][3]string{
		{"4", "3/12", "4/12"}, {"4", "3", "4"}, {"4/10", "3/12", "4/10"}, {"", "3/12", ""}, {"4", "3/", "4"},
	} {
		if got := keepTotal(c[0], c[1]); got != c[2] {
			t.Errorf("keepTotal(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
