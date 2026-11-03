package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
)

// addCLIFixture is a download folder with one album and one file with no
// tags, and an empty library beside it.
func addCLIFixture(t *testing.T) (src, lib string) {
	t.Helper()
	base := t.TempDir()
	src, lib = filepath.Join(base, "Downloads"), filepath.Join(base, "Music")
	write := func(dir, name string, tags map[string]string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := flac.BuildFile(flac.StreamInfo{SampleRate: 8000, Channels: 1, BitsPerSample: 16,
			TotalSamples: 8000 * 60}, tags, nil)
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rip := filepath.Join(src, "rip")
	write(rip, "a.flac", map[string]string{"album": "Remain in Light", "artist": "Talking Heads",
		"title": "Born Under Punches", "tracknumber": "1"})
	write(rip, "b.flac", map[string]string{"album": "Remain in Light", "artist": "Talking Heads",
		"title": "Crosseyed and Painless", "tracknumber": "2"})
	write(filepath.Join(src, "mystery"), "x.flac", map[string]string{"title": "?"})
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	return src, lib
}

// TestAddDryRunPrintsThePlanOnly: the plan, ticks and whys, and nothing on
// disk changes.
func TestAddDryRunPrintsThePlanOnly(t *testing.T) {
	useConfig(t, "")
	src, lib := addCLIFixture(t)
	out, _, err := runCore(t, "add", src, "--to", lib, "--dry-run", "--journal-dir", t.TempDir())
	if err != nil {
		t.Fatalf("add --dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Adding to your library (" + lib + ")",
		"2 albums, 3 song(s) (FLAC 3)",
		"[x] Remain in Light - Talking Heads",
		"2 songs",
		"[ ] mystery",
		"no tags: 1 song carries no album or artist",
		"--dry-run: nothing was copied. Without it, 1 album would be copied and verified; " +
			"the originals are deleted once they are safely in the library",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not say %q:\n%s", want, out)
		}
	}
	if ents, _ := os.ReadDir(lib); len(ents) != 0 {
		t.Errorf("a dry run wrote into the library: %v", ents)
	}
	if _, err := os.Stat(filepath.Join(src, "rip", "a.flac")); err != nil {
		t.Errorf("a dry run moved an original: %v", err)
	}
}

// TestAddCopiesAndClears: without --dry-run the album lands under its
// canonical names, the originals and their emptied folder go, the album
// with no tags stays, and the undo line names the journal.
func TestAddCopiesAndClears(t *testing.T) {
	useConfig(t, "")
	src, lib := addCLIFixture(t)
	jd := t.TempDir()
	out, _, err := runCore(t, "add", src, "--to", lib, "--journal-dir", jd)
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	dir := filepath.Join(lib, "Remain in Light - Talking Heads")
	for _, n := range []string{"01 - Talking Heads - Born Under Punches.flac", "02 - Talking Heads - Crosseyed and Painless.flac"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s is not in the library: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(src, "rip")); !os.IsNotExist(err) {
		t.Errorf("the emptied folder is still there (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(src, "mystery", "x.flac")); err != nil {
		t.Errorf("the file nobody could name was touched: %v", err)
	}
	if !strings.Contains(out, "Undo: core fix --undo "+jd) || !strings.Contains(out, "2 original file(s) deleted") {
		t.Errorf("output:\n%s", out)
	}

	// The undo the line names puts it all back.
	j := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "Undo: core fix --undo ", 2)[1], "\n", 2)[0])
	if _, _, err := runCore(t, "fix", "--undo", j, "--journal-dir", jd); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "rip", "a.flac")); err != nil {
		t.Errorf("undo did not put the original back: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("undo left the album folder in the library (%v)", err)
	}
}

func TestAddKeepOriginalsAndTheConfiguredLibrary(t *testing.T) {
	src, lib := addCLIFixture(t)
	useConfig(t, `{"source": `+quoteJSON(lib)+`, "online_art": false}`)
	out, _, err := runCore(t, "add", src, "--keep-originals", "--journal-dir", t.TempDir())
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(lib, "Remain in Light - Talking Heads")); err != nil {
		t.Errorf("--to did not default to source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "rip", "a.flac")); err != nil {
		t.Errorf("--keep-originals deleted one: %v", err)
	}
	if !strings.Contains(out, "the originals stay where they are") {
		t.Errorf("output:\n%s", out)
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
