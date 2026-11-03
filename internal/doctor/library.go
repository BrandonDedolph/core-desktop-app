// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/coreart"
	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
	"github.com/BrandonDedolph/core-desktop-app/internal/library"
)

// The library half, as a person reads it: not the lines CheckVolume prints
// but what is wrong with the library on the iPod, in words, and what puts it
// right. It is what the window's Summary tab shows and what its Repair button
// acts on.
//
// Every finding says which of three things fixes it, because the window's
// one rule is that it never patches the iPod by hand: the repair is the same
// write a sync is. So a finding is either something a plain sync puts right
// (a song missing from its folder, a device file the iPod refuses), something
// a sync with the covers re-rendered puts right (a missing or invalid
// sidecar), or something no sync does (a library over the iPod's caps, a
// torn On-The-Go save), in which case it says what does.
//
// Cost: one directory listing per album folder, one 12-byte read of each
// sidecar, the index (already read by CheckVolume) and the playlists. No song
// file is opened.

// Repair is what puts a finding right.
type Repair int

// The three answers, in increasing order of how much a repair writes: a
// report's Repair is the largest of its findings'.
const (
	// RepairNone: nothing a sync does. The finding's Fix says what does.
	RepairNone Repair = iota
	// RepairPlain: a sync.
	RepairPlain
	// RepairArt: a sync with every cover re-rendered (`core sync
	// --art-refresh`).
	RepairArt
)

func (r Repair) String() string {
	switch r {
	case RepairPlain:
		return "sync"
	case RepairArt:
		return "sync with covers re-rendered"
	default:
		return "none"
	}
}

// Finding is one problem: the sentence, what fixes it, and how many things it
// counts. Names are a few of them (album folders, "Folder/File", playlist
// lines), for the log; never more than findingNames.
type Finding struct {
	Text   string
	Fix    string
	Repair Repair
	N      int
	Names  []string
}

// findingNames is how many examples a finding carries.
const findingNames = 5

// LibraryReport is LibraryChecks' answer: the volume half it ran on the way
// (so a caller needs one call, not two), the three counts the checks of its
// own produce, and the findings in the order a person should read them.
type LibraryReport struct {
	Volume VolumeReport
	// MissingSongs is index records whose file is not on the iPod;
	// NoCover is album folders whose sidecars are missing or invalid;
	// DeadEntries is playlist lines that name no file.
	MissingSongs, NoCover, DeadEntries int
	Findings                           []Finding
}

// Repair is what one repair runs: the largest of the findings' answers, so a
// cover finding beside a missing song is one sync with the covers
// re-rendered, and a report whose findings no sync fixes is RepairNone.
func (r LibraryReport) Repair() Repair {
	best := RepairNone
	for _, f := range r.Findings {
		if f.Repair > best {
			best = f.Repair
		}
	}
	return best
}

// LibraryChecks runs every volume check (CheckVolume) and then the library's
// own: every index record's file present, every album folder's sidecars
// valid, every playlist line naming a file. Read-only. An empty volume, or
// one with no Music/ folder, has no findings — there is no library to be
// wrong — and Volume.Present says which.
func LibraryChecks(volume string) LibraryReport {
	rep := LibraryReport{Volume: CheckVolume(volume)}
	v := &rep.Volume
	if !v.Present {
		return rep
	}
	music := filepath.Join(volume, devicefs.MusicDir)
	add := func(f Finding) { rep.Findings = append(rep.Findings, f) }

	// The index.
	switch {
	case v.IndexOK:
	case fileMissing(filepath.Join(music, devicefs.IndexName)):
		add(Finding{Text: "The iPod has no song index", Repair: RepairPlain, N: 1,
			Fix: "a sync writes one; without it the iPod reads every song's tags each time it starts"})
	default:
		add(Finding{Text: "The iPod's song index is damaged", Repair: RepairPlain, N: 1,
			Fix: "a sync writes a new one; until then the iPod reads every song's tags each time it starts"})
	}

	// The tree: every file under each album folder, by the two hashes an
	// index record carries — the binding the firmware itself uses, so a
	// file whose name differs only in case or in a curly quote is present.
	dirs := dirCache{}
	type pair struct{ folder, file uint32 }
	files := map[pair]struct{}{}
	var noCover, unreadable []string
	top, err := dirs.list(music)
	if err != nil {
		unreadable = append(unreadable, devicefs.MusicDir)
	}
	for _, e := range top {
		if !e.IsDir() || strings.EqualFold(e.Name(), devicefs.PlaylistDir) {
			continue
		}
		dir := filepath.Join(music, e.Name())
		ents, err := dirs.list(dir)
		if err != nil {
			unreadable = append(unreadable, e.Name())
			continue
		}
		fh, audio := library.NameHash(e.Name()), false
		for _, f := range ents {
			if f.IsDir() {
				continue
			}
			files[pair{fh, library.NameHash(f.Name())}] = struct{}{}
			audio = audio || isAudioName(f.Name())
		}
		if audio && !sidecarsOK(dir) {
			noCover = append(noCover, e.Name())
		}
	}

	var missing []string
	for _, r := range v.recs {
		if _, ok := files[pair{r.FolderHash, r.FileHash}]; !ok {
			missing = append(missing, r.Folder+"/"+r.File)
		}
	}
	rep.MissingSongs, rep.NoCover = len(missing), len(noCover)
	if n := len(missing); n > 0 {
		add(Finding{Text: count(n, "song in the index is", "songs in the index are") + " not on the iPod",
			Repair: RepairPlain, N: n, Names: firstN(missing),
			Fix: "a sync copies them back from your music folder, or takes them out of the index if the folder no longer has them"})
	}
	if n := len(noCover); n > 0 {
		add(Finding{Text: count(n, "album has", "albums have") + " no cover on the iPod",
			Repair: RepairArt, N: n, Names: firstN(noCover),
			Fix: "a sync renders the covers again from your music folder; an album with no cover there gets one on the Fix tab"})
	}

	// Playlists.
	dead := deadEntries(volume, filepath.Join(music, devicefs.PlaylistDir), dirs)
	rep.DeadEntries = len(dead)
	if n := len(dead); n > 0 {
		add(Finding{Text: count(n, "playlist entry points", "playlist entries point") + " at nothing",
			Repair: RepairPlain, N: n, Names: firstN(dead),
			Fix: "a sync copies back the songs your music folder still has and rewrites the playlists made there"})
	}

	// The device files. Each one a sync writes when the iPod would refuse
	// it, and each one is a different loss while it is wrong.
	deviceFile := func(ok bool, name, what, missingFix, damagedFix string) {
		switch {
		case ok:
		case fileMissing(filepath.Join(volume, name)):
			add(Finding{Text: "The iPod's " + what + " is missing", Repair: RepairPlain, N: 1, Fix: missingFix})
		default:
			add(Finding{Text: "The iPod's " + what + " is damaged", Repair: RepairPlain, N: 1, Fix: damagedFix})
		}
	}
	deviceFile(v.ConfigValid, devicefs.ConfigName, "settings file",
		"a sync writes one; until then a setting changed on the iPod is lost when it restarts",
		"a sync writes a fresh one, with the default settings")
	deviceFile(v.LogValid, devicefs.LogName, "event log",
		"a sync writes one; until then the iPod keeps no log",
		"a sync writes a fresh one; until then the iPod keeps no log")
	deviceFile(v.OTGValid, devicefs.OTGName, "On-The-Go file",
		"a sync writes one; until then the On-The-Go list is lost when the iPod restarts",
		"a sync writes a fresh one; until then the On-The-Go list is lost when the iPod restarts")
	slotFindings(filepath.Join(music, devicefs.PlaylistDir), add)

	// The caps. Past one the iPod drops records with no word, and a sync
	// writes what the folder has, so only a smaller folder fixes it.
	for _, c := range []struct {
		n, max int
		what   string
	}{
		{v.Songs, library.MaxSongs, "songs"},
		{v.Albums, library.MaxAlbums, "albums"},
		{v.Genres, library.MaxGenres, "genres"},
	} {
		if c.n > c.max {
			add(Finding{Text: fmt.Sprintf("The library has %d %s, over the iPod's %d", c.n, c.what, c.max),
				Repair: RepairNone, N: c.n - c.max,
				Fix: fmt.Sprintf("the iPod shows the first %d and drops the rest; a sync cannot fix it — take some albums out of your music folder, then sync", c.max)})
		}
	}

	if n := len(unreadable); n > 0 {
		add(Finding{Text: count(n, "folder", "folders") + " on the iPod could not be read",
			Repair: RepairNone, N: n, Names: firstN(unreadable),
			Fix: "a sync cannot fix that; `core doctor` and the log say more"})
	}
	return rep
}

// slotFindings reports the five On-The-Go save slots. A missing one is a
// sync's (it puts back an empty one); a torn, foreign or misshapen one is
// not — a sync never touches a slot that exists, because it may be a list
// the person saved, so the way out is on the iPod or by hand.
func slotFindings(dir string, add func(Finding)) {
	var absent []string
	for n := 1; n <= devicefs.OTGPlaylistSlots; n++ {
		name := devicefs.OTGSlotName(n)
		label := fmt.Sprintf("On-The-Go %d", n)
		info, err := devicefs.OTGSlotFileState(filepath.Join(dir, name))
		switch {
		case err != nil:
			add(Finding{Text: label + " cannot be read, so the iPod never saves into it", Repair: RepairNone, N: 1,
				Names: []string{name},
				Fix:   "delete it from Music/Playlists on the iPod and sync to put an empty one back; `core doctor` says more"})
		case info.State == devicefs.OTGSlotAbsent:
			absent = append(absent, name)
		case info.State == devicefs.OTGSlotDamaged:
			add(Finding{Text: label + " is a save that was interrupted", Repair: RepairNone, N: 1,
				Names: []string{name},
				Fix:   "open it on the iPod and choose Delete Playlist; a sync never rewrites a saved list"})
		case info.State == devicefs.OTGSlotForeign:
			add(Finding{Text: label + " is a playlist the iPod will not save into", Repair: RepairNone, N: 1,
				Names: []string{name},
				Fix:   "if you did not put it there, delete it from Music/Playlists on the iPod and sync; `core doctor` says more"})
		case info.Size < devicefs.OTGSlotFileMin || info.Size%devicefs.OTGSlotSizeGrain != 0:
			add(Finding{Text: label + " is the wrong size, so the iPod never saves into it", Repair: RepairNone, N: 1,
				Names: []string{name},
				Fix:   "delete it from Music/Playlists on the iPod and sync to put an empty one back"})
		}
	}
	if n := len(absent); n > 0 {
		add(Finding{Text: count(n, "On-The-Go save slot is", "On-The-Go save slots are") + " missing",
			Repair: RepairPlain, N: n, Names: absent,
			Fix: "a sync puts back empty ones"})
	}
}

// deadEntries is every playlist line under dir that names no file, as
// "Playlist: line". A line is resolved the way the firmware resolves it
// (devicefs.ResolveM3ULine), then walked from the volume root one folder at a
// time. A line the firmware rejects outright (one that climbs out of the
// volume) names nothing too. A slot the iPod calls torn is the slot check's,
// not this one's.
func deadEntries(volume, dir string, dirs dirCache) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".m3u8", ".m3u":
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	sort.Strings(names)
	var dead []string
	for _, name := range names {
		path := filepath.Join(dir, name)
		var lines []string
		if devicefs.OTGSlotIndex(name) > 0 {
			// A torn slot's lines are not a list the iPod plays.
			l, info, _ := devicefs.ReadOTGSlotFile(path)
			if info.State == devicefs.OTGSlotDamaged {
				continue
			}
			lines = l
		} else {
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			b = bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
			lines = strings.Split(string(b), "\n")
		}
		label := strings.TrimSuffix(name, filepath.Ext(name))
		for _, line := range lines {
			line = strings.Trim(strings.TrimSuffix(line, "\r"), " \t")
			if line == "" || line[0] == '#' {
				continue
			}
			rel, ok := devicefs.ResolveM3ULine(line)
			if !ok || !dirs.fileExists(volume, rel) {
				dead = append(dead, label+": "+line)
			}
		}
	}
	return dead
}

// dirCache lists each folder once, keyed by NormKey so a lookup matches the
// way FAT does (case does not matter) and the firmware's name fold does (a
// curly quote is a straight one). The album walk fills it, so resolving a
// playlist of songs that are there costs no further listing.
type dirCache map[string]map[string]os.DirEntry

func (c dirCache) list(dir string) ([]os.DirEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	m := make(map[string]os.DirEntry, len(ents))
	for _, e := range ents {
		m[library.NormKey(e.Name())] = e
	}
	c[dir] = m
	return ents, nil
}

// fileExists walks rel (forward slashes, volume-relative) from root.
func (c dirCache) fileExists(root, rel string) bool {
	segs := strings.Split(rel, "/")
	cur := root
	for i, seg := range segs {
		m, ok := c[cur]
		if !ok {
			if _, err := c.list(cur); err != nil {
				return false
			}
			m = c[cur]
		}
		e, ok := m[library.NormKey(seg)]
		if !ok {
			return false
		}
		if i == len(segs)-1 {
			return !e.IsDir()
		}
		if !e.IsDir() {
			return false
		}
		cur = filepath.Join(cur, e.Name())
	}
	return false
}

// sidecarsOK is the sync's own sidecarsValid asked of the headers alone:
// both present and each the exact square the firmware expects.
func sidecarsOK(dir string) bool {
	return sidecarOK(filepath.Join(dir, coreart.ArtName), coreart.ArtSize) &&
		sidecarOK(filepath.Join(dir, coreart.ThumbName), coreart.ThumbSize)
}

func sidecarOK(path string, size int) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	head := make([]byte, coreart.HeaderLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false
	}
	return coreart.ValidHeader(head[:n], st.Size(), size)
}

func isAudioName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, want := range library.AudioExts {
		if ext == want {
			return true
		}
	}
	return false
}

func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

// count is "1 album has" / "3 albums have".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func firstN(s []string) []string {
	if len(s) > findingNames {
		s = s[:findingNames]
	}
	return append([]string(nil), s...)
}
