// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"fmt"
	stdimage "image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/cidx"
	"github.com/BrandonDedolph/core-desktop-app/internal/coreart"
	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
	"github.com/BrandonDedolph/core-desktop-app/internal/library"
)

// The library checks run on a volume built the way a sync builds one: the
// device files from devicefs.Ensure*, album folders of song files and their
// two sidecars, a CORELIB.IDX whose records carry the folder and file hashes,
// a playlist of /Music/ lines and a saved On-The-Go slot. Then one thing at a
// time is broken.

type song struct{ folder, file string }

var volSongs = []song{
	{"Artist - One", "01. A.flac"},
	{"Artist - One", "02. B.flac"},
	{"Artist - Two", "01. C.mp3"},
}

func mkfile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeIndex(t *testing.T, root string, songs []song, genre func(int) string) {
	t.Helper()
	var recs []cidx.Record
	for i, s := range songs {
		g := "Rock"
		if genre != nil {
			g = genre(i)
		}
		recs = append(recs, cidx.Record{
			Track: uint16(i + 1), Disc: 1, Folder: s.folder, File: s.file,
			Title: s.file, Artist: "Artist", Genre: g,
			FolderHash: library.NameHash(s.folder), FileHash: library.NameHash(s.file),
		})
	}
	mkfile(t, filepath.Join(root, devicefs.MusicDir, devicefs.IndexName), cidx.Encode(recs))
}

// cleanVolume is a volume with nothing wrong with it.
func cleanVolume(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	music := filepath.Join(root, devicefs.MusicDir)
	for _, ensure := range []func() error{
		func() error { _, err := devicefs.EnsureConfig(root); return err },
		func() error { _, err := devicefs.EnsureLog(root, 0); return err },
		func() error { _, err := devicefs.EnsureOTG(root); return err },
		func() error {
			if err := os.MkdirAll(music, 0o755); err != nil {
				return err
			}
			_, err := devicefs.EnsureOTGSlots(music)
			return err
		},
	} {
		if err := ensure(); err != nil {
			t.Fatal(err)
		}
	}
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	for _, s := range volSongs {
		mkfile(t, filepath.Join(music, s.folder, s.file), []byte("audio"))
		mkfile(t, filepath.Join(music, s.folder, coreart.ArtName), coreart.Render(img, coreart.ArtSize))
		mkfile(t, filepath.Join(music, s.folder, coreart.ThumbName), coreart.Render(img, coreart.ThumbSize))
	}
	writeIndex(t, root, volSongs, nil)
	mkfile(t, filepath.Join(music, devicefs.PlaylistDir, "Road Trip.m3u8"), []byte(
		"\xEF\xBB\xBF#EXTM3U\r\n#EXTINF:200,A\r\n/Music/Artist - One/01. A.flac\r\n../Artist - Two/01. C.mp3\r\n"))
	mkfile(t, filepath.Join(music, devicefs.PlaylistDir, devicefs.OTGSlotName(2)),
		savedSlot(t, 3, "/Music/Artist - One/02. B.flac"))
	return root
}

func musicPath(root string, parts ...string) string {
	return filepath.Join(append([]string{root, devicefs.MusicDir}, parts...)...)
}

func texts(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Text)
	}
	return out
}

// only asserts the report has exactly one finding and returns it.
func only(t *testing.T, rep LibraryReport) Finding {
	t.Helper()
	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %q, want exactly one", texts(rep.Findings))
	}
	return rep.Findings[0]
}

func TestLibraryChecksClean(t *testing.T) {
	rep := LibraryChecks(cleanVolume(t))
	if len(rep.Findings) != 0 {
		t.Fatalf("a clean volume has findings: %q", texts(rep.Findings))
	}
	if !rep.Volume.Present || !rep.Volume.IndexOK || rep.Volume.Songs != 3 || rep.Volume.Albums != 2 {
		t.Errorf("volume = %+v", rep.Volume)
	}
	if rep.Repair() != RepairNone {
		t.Errorf("Repair = %v for nothing wrong", rep.Repair())
	}
}

// A file is bound to its record by the name hashes, which fold ASCII case and
// curly quotes: a file whose name differs from the record only so is THERE —
// the iPod plays it — and calling it missing would send a person to a repair
// that changes nothing.
func TestLibraryChecksBindsByHashNotSpelling(t *testing.T) {
	root := cleanVolume(t)
	songs := append([]song(nil), volSongs...)
	songs = append(songs, song{"Artist - Two", "02. Don’t.mp3"})
	mkfile(t, musicPath(root, "Artist - Two", "02. Don't.MP3"), []byte("audio"))
	if err := os.Rename(musicPath(root, "Artist - One"), musicPath(root, "ARTIST - one")); err != nil {
		t.Fatal(err)
	}
	writeIndex(t, root, songs, nil)
	if rep := LibraryChecks(root); len(rep.Findings) != 0 {
		t.Errorf("findings = %q", texts(rep.Findings))
	}
}

func TestLibraryChecksSongNotOnTheIPod(t *testing.T) {
	root := cleanVolume(t)
	if err := os.Remove(musicPath(root, "Artist - One", "02. B.flac")); err != nil {
		t.Fatal(err)
	}
	// The saved On-The-Go list names that song too: one missing file, two
	// findings, because the playlist line now points at nothing as well.
	rep := LibraryChecks(root)
	if rep.MissingSongs != 1 || rep.NoCover != 0 || rep.DeadEntries != 1 {
		t.Fatalf("counts = %d/%d/%d, findings %q", rep.MissingSongs, rep.NoCover, rep.DeadEntries, texts(rep.Findings))
	}
	f := rep.Findings[0]
	if f.Text != "1 song in the index is not on the iPod" || f.Repair != RepairPlain ||
		len(f.Names) != 1 || f.Names[0] != "Artist - One/02. B.flac" {
		t.Errorf("finding = %+v", f)
	}
	if rep.Repair() != RepairPlain {
		t.Errorf("Repair = %v", rep.Repair())
	}

	// Plural, and a whole album gone: its folder has no songs, so it is
	// not also an album without a cover.
	if err := os.RemoveAll(musicPath(root, "Artist - Two")); err != nil {
		t.Fatal(err)
	}
	rep = LibraryChecks(root)
	if rep.Findings[0].Text != "2 songs in the index are not on the iPod" || rep.NoCover != 0 {
		t.Errorf("findings = %q", texts(rep.Findings))
	}
}

func TestLibraryChecksCovers(t *testing.T) {
	root := cleanVolume(t)
	if err := os.Remove(musicPath(root, "Artist - Two", coreart.ThumbName)); err != nil {
		t.Fatal(err)
	}
	// Valid to the firmware, but the 120 where the 28 belongs: the sync
	// re-renders it, so the doctor must call it wrong too.
	art, err := os.ReadFile(musicPath(root, "Artist - One", coreart.ArtName))
	if err != nil {
		t.Fatal(err)
	}
	mkfile(t, musicPath(root, "Artist - One", coreart.ThumbName), art)
	// A folder of no songs is not an album, cover or not.
	mkfile(t, musicPath(root, "Scans", "notes.txt"), []byte("x"))

	f := only(t, LibraryChecks(root))
	if f.Text != "2 albums have no cover on the iPod" || f.Repair != RepairPlain || f.N != 2 ||
		strings.Join(f.Names, "|") != "Artist - One|Artist - Two" {
		t.Errorf("finding = %+v", f)
	}

	// One truncated sidecar: the header says 120×120 and the file is short.
	root = cleanVolume(t)
	p := musicPath(root, "Artist - Two", coreart.ArtName)
	b, _ := os.ReadFile(p)
	mkfile(t, p, b[:len(b)-2])
	if f := only(t, LibraryChecks(root)); f.Text != "1 album has no cover on the iPod" {
		t.Errorf("finding = %+v", f)
	}
}

func TestLibraryChecksPlaylistEntries(t *testing.T) {
	root := cleanVolume(t)
	mkfile(t, musicPath(root, devicefs.PlaylistDir, "Mixed.m3u"), []byte(strings.Join([]string{
		"#EXTM3U",
		`D:\MUSIC\artist - one\01. a.flac`,  // there: drive letter, backslashes, case
		"/Music/Artist - One/09. Gone.flac", // not there
		"Artist - One/01. A.flac",           // relative to Music/Playlists: not there
		"/../../escape.flac",                // the firmware rejects it
		"/Music/Artist - One",               // a folder, not a song
		"",
	}, "\n")))
	f := only(t, LibraryChecks(root))
	if f.Text != "4 playlist entries point at nothing" || f.Repair != RepairPlain || f.N != 4 {
		t.Errorf("finding = %+v", f)
	}
	if !strings.HasPrefix(f.Names[0], "Mixed: /Music/Artist - One/09. Gone.flac") {
		t.Errorf("names = %q", f.Names)
	}

	root = cleanVolume(t)
	mkfile(t, musicPath(root, devicefs.PlaylistDir, "Gym.m3u8"), []byte("#EXTM3U\n/Music/Nope/01.flac\n"))
	if f := only(t, LibraryChecks(root)); f.Text != "1 playlist entry points at nothing" {
		t.Errorf("finding = %+v", f)
	}
}

func TestLibraryChecksIndex(t *testing.T) {
	root := cleanVolume(t)
	idx := musicPath(root, devicefs.IndexName)
	b, _ := os.ReadFile(idx)
	b[len(b)-1] ^= 0xFF // the CRC no longer matches
	mkfile(t, idx, b)
	f := only(t, LibraryChecks(root))
	if f.Text != "The iPod's song index is damaged" || f.Repair != RepairPlain {
		t.Errorf("finding = %+v", f)
	}

	if err := os.Remove(idx); err != nil {
		t.Fatal(err)
	}
	if f := only(t, LibraryChecks(root)); f.Text != "The iPod has no song index" || f.Repair != RepairPlain {
		t.Errorf("finding = %+v", f)
	}
}

func TestLibraryChecksDeviceFiles(t *testing.T) {
	root := cleanVolume(t)
	mkfile(t, filepath.Join(root, devicefs.ConfigName), make([]byte, devicefs.ConfigFileBytes))
	if err := os.Remove(filepath.Join(root, devicefs.LogName)); err != nil {
		t.Fatal(err)
	}
	rep := LibraryChecks(root)
	if got := strings.Join(texts(rep.Findings), "|"); got !=
		"The iPod's settings file is damaged|The iPod's event log is missing" {
		t.Fatalf("findings = %s", got)
	}
	// A sync's Ensure* rewrites both — which is why they are a sync's to fix.
	for _, f := range rep.Findings {
		if f.Repair != RepairPlain || f.Fix == "" {
			t.Errorf("%+v", f)
		}
	}
	if !strings.Contains(rep.Findings[0].Fix, "default settings") {
		t.Errorf("a fresh settings file loses the person's settings and must say so: %q", rep.Findings[0].Fix)
	}
}

// What no sync fixes says so, and does not make the report's Repair a sync.
func TestLibraryChecksWhatASyncCannotFix(t *testing.T) {
	root := cleanVolume(t)
	mkfile(t, musicPath(root, devicefs.PlaylistDir, devicefs.OTGSlotName(3)), tornSlot(t))
	f := only(t, LibraryChecks(root))
	if f.Repair != RepairNone || f.Text != "On-The-Go 3 is a save that was interrupted" ||
		!strings.Contains(f.Fix, "Delete Playlist") {
		t.Errorf("finding = %+v", f)
	}
	if r := LibraryChecks(root).Repair(); r != RepairNone {
		t.Errorf("Repair = %v for a torn slot", r)
	}

	// Beside a missing slot (a sync's) the report's Repair is a sync, and
	// the torn one is still listed.
	if err := os.Remove(musicPath(root, devicefs.PlaylistDir, devicefs.OTGSlotName(5))); err != nil {
		t.Fatal(err)
	}
	rep := LibraryChecks(root)
	if rep.Repair() != RepairPlain || len(rep.Findings) != 2 ||
		rep.Findings[1].Text != "1 On-The-Go save slot is missing" {
		t.Errorf("Repair %v, findings %q", rep.Repair(), texts(rep.Findings))
	}

	// Over a cap: 129 genres.
	root = cleanVolume(t)
	var songs []song
	for i := 0; i <= library.MaxGenres; i++ {
		s := song{"Artist - Many", fmt.Sprintf("%03d. T.flac", i+1)}
		songs = append(songs, s)
		mkfile(t, musicPath(root, s.folder, s.file), []byte("audio"))
	}
	for _, n := range []string{coreart.ArtName, coreart.ThumbName} {
		b, _ := os.ReadFile(musicPath(root, "Artist - One", n))
		mkfile(t, musicPath(root, "Artist - Many", n), b)
	}
	writeIndex(t, root, songs, func(i int) string { return fmt.Sprintf("G%d", i) })
	rep = LibraryChecks(root)
	var cap *Finding
	for i := range rep.Findings {
		if strings.Contains(rep.Findings[i].Text, "genres") {
			cap = &rep.Findings[i]
		}
	}
	if cap == nil || cap.Repair != RepairNone || cap.Text != "The library has 129 genres, over the iPod's 128" {
		t.Fatalf("findings = %q", texts(rep.Findings))
	}
}

// Covers and a missing song together are ONE repair, a sync: it renders
// the missing covers and copies the song.
func TestLibraryReportRepairIsTheLargest(t *testing.T) {
	root := cleanVolume(t)
	_ = os.Remove(musicPath(root, "Artist - Two", coreart.ArtName))
	_ = os.Remove(musicPath(root, "Artist - One", "01. A.flac"))
	rep := LibraryChecks(root)
	if rep.Repair() != RepairPlain {
		t.Errorf("Repair = %v, findings %q", rep.Repair(), texts(rep.Findings))
	}
}

// No library is not a broken one: no Music/ folder, and no volume at all,
// have no findings.
func TestLibraryChecksNoLibrary(t *testing.T) {
	for _, vol := range []string{"", t.TempDir()} {
		rep := LibraryChecks(vol)
		if rep.Volume.Present || len(rep.Findings) != 0 {
			t.Errorf("%q: present %v, findings %q", vol, rep.Volume.Present, texts(rep.Findings))
		}
	}
}
