package librarian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
)

// --- fixtures ---------------------------------------------------------------

// song writes one FLAC with a length (8 kHz mono keeps it to a few kilobytes
// of real frames) and the tags given.
func song(t *testing.T, dir, name string, tags map[string]string, secs int, cover []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var pics []flac.Picture
	if cover != nil {
		pics = []flac.Picture{{Type: flac.PictureTypeFrontCover, MIME: "image/png", Data: cover}}
	}
	raw := flac.BuildFile(flac.StreamInfo{SampleRate: 8000, Channels: 1, BitsPerSample: 16,
		TotalSamples: uint64(secs) * 8000}, tags, pics)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func tagged(album, artist, title string, n int) map[string]string {
	return map[string]string{"album": album, "artist": artist, "title": title, "tracknumber": fmt.Sprint(n)}
}

// addFixture is a folder to add and a library to add it to:
//
//	src/Good Rip/          "Good" by Band, two songs, a cover.jpg, no embedded cover
//	src/untagged/          one song with no tags at all
//	src/numberless/        "Numberless" by Band, a song with no track number
//	src/Phone/             two .m4a files
//	src/Old/               "Old Thing" by Band: the same songs the library has
//	                       under another folder name
//	src/Same/              "Same" by Band: different songs, but the library
//	                       has a "Same - Band" folder
//	lib/Old Thing (2011 Remaster) - Band/
//	lib/Same - Band/
func addFixture(t *testing.T) (src, lib string) {
	t.Helper()
	base := t.TempDir()
	src, lib = filepath.Join(base, "src"), filepath.Join(base, "lib")
	good := filepath.Join(src, "Good Rip")
	song(t, good, "track1.flac", tagged("Good", "Band", "One", 1), 61, nil)
	song(t, good, "track2.flac", tagged("Good", "Band", "Two", 2), 62, nil)
	if err := os.WriteFile(filepath.Join(good, "cover.jpg"), jpegSquare(t, 640), 0o644); err != nil {
		t.Fatal(err)
	}
	song(t, filepath.Join(src, "untagged"), "x.flac", map[string]string{}, 30, nil)
	song(t, filepath.Join(src, "numberless"), "a.flac",
		map[string]string{"album": "Numberless", "artist": "Band", "title": "A"}, 30, nil)
	phone := filepath.Join(src, "Phone")
	if err := os.MkdirAll(phone, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"1.m4a", "2.m4a"} {
		if err := os.WriteFile(filepath.Join(phone, n), []byte("not for the iPod"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cover := smallPNG(t)
	for i, s := range []string{"Uno", "Dos", "Tres"} {
		song(t, filepath.Join(src, "Old"), fmt.Sprintf("%d.flac", i+1), tagged("Old Thing", "Band", s, i+1), 100+i, cover)
		song(t, filepath.Join(lib, "Old Thing (2011 Remaster) - Band"), fmt.Sprintf("%02d - Band - %s.flac", i+1, s),
			tagged("Old Thing (2011 Remaster)", "Band", s, i+1), 100+i, cover)
	}
	song(t, filepath.Join(src, "Same"), "1.flac", tagged("Same", "Band", "New Song", 1), 77, cover)
	song(t, filepath.Join(lib, "Same - Band"), "01 - Band - Old Song.flac", tagged("Same", "Band", "Old Song", 1), 99, cover)
	return src, lib
}

// goodOnly is a folder holding the one good album and nothing else.
func goodOnly(t *testing.T) (src, lib string) {
	t.Helper()
	base := t.TempDir()
	src, lib = filepath.Join(base, "Downloads", "Good Rip"), filepath.Join(base, "Music")
	song(t, src, "track1.flac", tagged("Good", "Band", "One", 1), 61, nil)
	song(t, src, "track2.flac", tagged("Good", "Band", "Two", 2), 62, nil)
	song(t, src, "track3.flac", tagged("Good", "Band", "Three", 3), 63, nil)
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	return src, lib
}

func mustPlan(t *testing.T, src, lib string, o Options) *AddPlan {
	t.Helper()
	p, err := PlanAdd(context.Background(), src, lib, o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// --- the plan ---------------------------------------------------------------

func TestPlanAddSortsWhatItFinds(t *testing.T) {
	src, lib := addFixture(t)
	p := mustPlan(t, src, lib, opts(t))

	got := map[string]AddAlbum{}
	for _, a := range p.Albums {
		got[a.Name] = a
	}
	if len(p.Albums) != 6 {
		t.Fatalf("albums = %d, want 6: %+v", len(p.Albums), p.Albums)
	}

	good := got["Good - Band"]
	if !good.Ticked() || good.Songs != 2 || good.HasCover {
		t.Errorf("the good album = %+v", good)
	}
	wantTo := []string{
		filepath.Join(lib, "Good - Band", "01 - Band - One.flac"),
		filepath.Join(lib, "Good - Band", "02 - Band - Two.flac"),
		filepath.Join(lib, "Good - Band", "cover.jpg"),
	}
	if len(good.Files) != len(wantTo) {
		t.Fatalf("files = %+v", good.Files)
	}
	for i, f := range good.Files {
		if f.To != wantTo[i] {
			t.Errorf("file %d goes to %s, want %s", i, f.To, wantTo[i])
		}
	}

	for name, why := range map[string]string{
		"untagged":          "no tags",
		"Numberless - Band": "no track numbers",
		"Phone":             "2 .m4a: a format the iPod cannot play",
	} {
		a, ok := got[name]
		if !ok || a.CanAdd() || !strings.Contains(a.Problem, why) {
			t.Errorf("%s = %+v, want a problem saying %q", name, a, why)
		}
	}

	old := got["Old Thing - Band"]
	if !old.CanAdd() || old.Ticked() || !strings.Contains(old.InLibrary, "Old Thing (2011 Remaster) - Band") {
		t.Errorf("the duplicate of a library album = %+v, want it found by its songs and unticked", old)
	}
	same := got["Same - Band"]
	if same.Ticked() || !strings.Contains(same.InLibrary, "already in your library") {
		t.Errorf("an album whose folder the library has = %+v", same)
	}
	if k := p.Ticked(); len(k) != 1 || k[0] != "Good - Band" {
		t.Errorf("ticked = %v, want only the good album", k)
	}
	if p.Formats["FLAC"] != 8 || p.Formats["M4A"] != 2 {
		t.Errorf("formats = %v", p.Formats)
	}
}

func TestPlanAddSkipDuplicatesKeepsTheFolderCheck(t *testing.T) {
	src, lib := addFixture(t)
	o := opts(t)
	o.SkipDuplicates = true
	p := mustPlan(t, src, lib, o)
	old, _ := p.Album("Old Thing - Band")
	same, _ := p.Album("Same - Band")
	if !old.Ticked() || same.Ticked() {
		t.Errorf("with the duplicate check off: old ticked=%v (want true), same ticked=%v (want false)",
			old.Ticked(), same.Ticked())
	}
}

func TestPlanAddRefusesTheLibraryItself(t *testing.T) {
	src, lib := addFixture(t)
	for _, c := range [][2]string{{lib, lib}, {filepath.Join(lib, "Same - Band"), lib}, {filepath.Dir(lib), lib}} {
		if _, err := PlanAdd(context.Background(), c[0], c[1], opts(t)); err == nil {
			t.Errorf("PlanAdd(%s, %s) was allowed", c[0], c[1])
		}
	}
	_ = src
}

// --- the add ----------------------------------------------------------------

func TestAddCopiesVerifiesThenClearsTheOriginals(t *testing.T) {
	src, lib := goodOnly(t)
	before := tree(t, src)
	o := opts(t)
	p := mustPlan(t, src, lib, o)
	res, err := Add(context.Background(), p, p.Ticked(), true, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Albums() != 1 || res.Deleted != 3 || len(res.Failures) != 0 || res.Journal == "" {
		t.Fatalf("result = %+v", res)
	}
	dir := filepath.Join(lib, "Good - Band")
	after := tree(t, dir)
	for i, name := range []string{"01 - Band - One.flac", "02 - Band - Two.flac", "03 - Band - Three.flac"} {
		if after[name] != before[fmt.Sprintf("track%d.flac", i+1)] {
			t.Errorf("%s is not a byte-for-byte copy of its original", name)
		}
	}
	if exists(src) {
		t.Errorf("the source folder is still there after its files went: %v", tree(t, src))
	}
	if !exists(filepath.Dir(src)) {
		t.Errorf("the folder above the one chosen went too")
	}
}

func TestAddLeavesTheOriginalsWhenAskedTo(t *testing.T) {
	src, lib := goodOnly(t)
	before := tree(t, src)
	o := opts(t)
	p := mustPlan(t, src, lib, o)
	res, err := Add(context.Background(), p, p.Ticked(), false, o)
	if err != nil || res.Albums() != 1 || res.Deleted != 0 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if !sameTree(tree(t, src), before) {
		t.Errorf("the source changed with delete off")
	}
	if _, err := Undo(context.Background(), res.Journal, o); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(lib, "Good - Band")) {
		t.Errorf("Undo left the copies: %v", tree(t, lib))
	}
	if !sameTree(tree(t, src), before) {
		t.Errorf("Undo touched the originals")
	}
}

// A copy whose bytes are not the original's is not counted as added: the
// album's copies go, and every original stays — including the ones whose
// copies were fine, because the delete waits for the whole album.
func TestAddACopyThatDoesNotMatchIsNotAdded(t *testing.T) {
	src, lib := goodOnly(t)
	before := tree(t, src)
	o := opts(t)
	p := mustPlan(t, src, lib, o)
	testAfterCopy = func(path string) {
		if strings.HasPrefix(filepath.Base(path), "03") {
			b, _ := os.ReadFile(path)
			b[len(b)-1] ^= 0xff
			_ = os.WriteFile(path, b, 0o644)
		}
	}
	defer func() { testAfterCopy = nil }()
	res, err := Add(context.Background(), p, p.Ticked(), true, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Albums() != 0 || len(res.Failures) != 1 || !strings.Contains(res.Failures[0].Reason, "did not match") {
		t.Fatalf("result = %+v", res)
	}
	if res.Deleted != 0 || !sameTree(tree(t, src), before) {
		t.Errorf("an original went although its album did not verify")
	}
	if exists(filepath.Join(lib, "Good - Band")) {
		t.Errorf("the partial copies stayed: %v", tree(t, lib))
	}
	if res.Journal != "" || len(journalsIn(t, o.JournalDir)) != 0 {
		t.Errorf("a journal was kept for an add that put nothing in the library")
	}
}

// A run that dies after the copies verified and before a single delete
// leaves both, and Undo takes the copies away.
func TestAddACrashBetweenCopyAndDeleteLeavesBoth(t *testing.T) {
	src, lib := goodOnly(t)
	before := tree(t, src)
	o := opts(t)
	p := mustPlan(t, src, lib, o)
	testStopAt = func(stage string) bool { return stage == "verified" }
	defer func() { testStopAt = nil }()
	if _, err := Add(context.Background(), p, p.Ticked(), true, o); !errors.Is(err, errStopped) {
		t.Fatalf("err = %v", err)
	}
	if !sameTree(tree(t, src), before) {
		t.Errorf("the originals changed")
	}
	dir := filepath.Join(lib, "Good - Band")
	if len(tree(t, dir)) != 3 {
		t.Errorf("the copies = %v", tree(t, dir))
	}
	js := journalsIn(t, o.JournalDir)
	if len(js) != 1 {
		t.Fatalf("journals = %v", js)
	}
	var j fixJournal
	b, _ := os.ReadFile(js[0])
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatal(err)
	}
	for _, op := range j.Adds {
		if !op.Verified || op.Deleted {
			t.Errorf("journal op = %+v, want verified and not deleted", op)
		}
	}
	if _, err := Undo(context.Background(), js[0], o); err != nil {
		t.Fatal(err)
	}
	if exists(dir) || !sameTree(tree(t, src), before) {
		t.Errorf("after Undo: library %v, source changed=%v", tree(t, lib), !sameTree(tree(t, src), before))
	}
}

// Undo puts every original back exactly — hash for hash, mtime for mtime —
// even though a cover went into the copies after the originals were gone.
func TestAddUndoRestoresTheOriginalsExactly(t *testing.T) {
	src, lib := goodOnly(t)
	if err := os.WriteFile(filepath.Join(src, "cover.jpg"), jpegSquare(t, 640), 0o644); err != nil {
		t.Fatal(err)
	}
	before := tree(t, src)
	stamps := map[string]int64{}
	for name := range before {
		fi, _ := os.Stat(filepath.Join(src, name))
		stamps[name] = fi.ModTime().UnixNano()
	}
	o := opts(t)
	p := mustPlan(t, src, lib, o)
	res, err := Add(context.Background(), p, p.Ticked(), true, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Covers != 1 || res.Added[0].Cover != "cover.jpg" {
		t.Fatalf("result = %+v, want the folder's cover embedded", res)
	}
	first := filepath.Join(lib, "Good - Band", "01 - Band - One.flac")
	if !hasCover(t, first) {
		t.Fatalf("the copy has no cover")
	}
	if exists(src) {
		t.Fatalf("the originals were not cleared")
	}

	rep, err := Undo(context.Background(), res.Journal, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 0 || rep.Undone != 4 {
		t.Fatalf("undo = %+v", rep)
	}
	if got := tree(t, src); !sameTree(got, before) {
		t.Errorf("restored %v, want %v", got, before)
	}
	for name, ns := range stamps {
		fi, _ := os.Stat(filepath.Join(src, name))
		if fi.ModTime().UnixNano() != ns {
			t.Errorf("%s came back with another mtime", name)
		}
	}
	if exists(filepath.Join(lib, "Good - Band")) {
		t.Errorf("the album folder is still in the library: %v", tree(t, lib))
	}
}

// Undo refuses a copy that is not what was added, rather than "restoring"
// the wrong bytes.
func TestAddUndoLeavesACopyThatChanged(t *testing.T) {
	src, lib := goodOnly(t)
	o := opts(t)
	p := mustPlan(t, src, lib, o)
	res, err := Add(context.Background(), p, p.Ticked(), true, o)
	if err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(lib, "Good - Band", "02 - Band - Two.flac")
	if err := os.WriteFile(changed, []byte("somebody else's"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Undo(context.Background(), res.Journal, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Undone != 2 || len(rep.Skipped) != 1 || rep.Skipped[0].Op.To != changed {
		t.Fatalf("undo = %+v", rep)
	}
	if !exists(changed) || exists(filepath.Join(src, "track2.flac")) {
		t.Errorf("the changed copy was moved back over the original's place")
	}
}

// An album the library has is unticked, never silently dropped: it is in the
// plan with its reason, and a person who ticks it anyway gets it — or, when
// its files would land on files the library has, a failure and nothing
// written over.
func TestAddAnAlbumAlreadyThere(t *testing.T) {
	src, lib := addFixture(t)
	o := opts(t)
	p := mustPlan(t, src, lib, o)
	same, ok := p.Album("Same - Band")
	if !ok || same.Ticked() || same.InLibrary == "" {
		t.Fatalf("same = %+v", same)
	}
	res, err := Add(context.Background(), p, []AlbumKey{"Same - Band", "Old Thing - Band"}, true, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Albums() != 2 {
		t.Fatalf("ticked anyway, both are added: %+v", res)
	}
	if !exists(filepath.Join(lib, "Same - Band", "01 - Band - Old Song.flac")) ||
		!exists(filepath.Join(lib, "Same - Band", "01 - Band - New Song.flac")) {
		t.Errorf("the library's own file or the new one is missing: %v", tree(t, filepath.Join(lib, "Same - Band")))
	}

	// The same album again, from a copy of the folder: every file would
	// land on one that is there.
	src2 := t.TempDir()
	song(t, src2, "1.flac", tagged("Same", "Band", "New Song", 1), 77, smallPNG(t))
	libBefore := tree(t, lib)
	p2 := mustPlan(t, src2, lib, o)
	a, _ := p2.Album("Same - Band")
	if a.Ticked() {
		t.Fatalf("the second time it is already there: %+v", a)
	}
	res2, err := Add(context.Background(), p2, []AlbumKey{"Same - Band"}, true, o)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Albums() != 0 || len(res2.Failures) != 1 || !strings.Contains(res2.Failures[0].Reason, "already in the library") {
		t.Fatalf("res2 = %+v", res2)
	}
	if !sameTree(tree(t, lib), libBefore) || !exists(filepath.Join(src2, "1.flac")) {
		t.Errorf("a refused add changed something")
	}
}

func TestAddRefusesAProblemAlbum(t *testing.T) {
	src, lib := addFixture(t)
	p := mustPlan(t, src, lib, opts(t))
	if _, err := Add(context.Background(), p, []AlbumKey{"?untagged"}, true, opts(t)); err == nil {
		t.Error("an album with no tags was added")
	}
}

func TestAddDryRunWritesNothing(t *testing.T) {
	src, lib := goodOnly(t)
	before, libBefore := tree(t, src), tree(t, lib)
	o := opts(t)
	o.DryRun = true
	p := mustPlan(t, src, lib, o)
	res, err := Add(context.Background(), p, p.Ticked(), true, o)
	if err != nil || res.Albums() != 1 || res.Journal != "" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if !sameTree(tree(t, src), before) || !sameTree(tree(t, lib), libBefore) {
		t.Error("a dry run wrote")
	}
}

func TestAddChainsTheSessionsJournal(t *testing.T) {
	src, lib := goodOnly(t)
	o := opts(t)
	o.After = filepath.Join(o.JournalDir, "earlier.json")
	if err := os.WriteFile(o.After, []byte(`{"kind":"core-fix","format":1,"root":"/x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, src, lib, o)
	res, err := Add(context.Background(), p, p.Ticked(), false, o)
	if err != nil {
		t.Fatal(err)
	}
	j, ok, err := readFixJournal(res.Journal)
	if err != nil || !ok || j.Previous != o.After || j.Format != addJournalFormat {
		t.Fatalf("journal = %+v ok=%v err=%v", j, ok, err)
	}
}
