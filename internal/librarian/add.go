package librarian

// add.go is Add music…: a folder from outside the library — a ripped CD, a
// download, a USB stick, a whole second library — copied in under the names
// the organizer gives a tree, verified, and only then cleared from where it
// came from.
//
// It is two calls, the way Inspect and Fix are. PlanAdd reads the folder and
// says what it found: the albums by their tags, the name each will get, the
// problems that stop one (no tags, no track numbers, a format the iPod cannot
// play), and the ones the library already has. It writes nothing. Add takes
// the plan and the albums a person ticked and does them one album at a time:
//
//	copy every file of the album to its place in the library (never over a
//	file that is there), hashing the original as it is read;
//	read every copy back and compare its hash with the original's;
//	only when EVERY file of the album verified, delete the originals (when
//	asked to) and then their folder, if that left it empty;
//	then a cover for an album with none.
//
// A copy that does not match, a disk that fills, a cancel: the copies of that
// album are removed and its originals were never touched. The journal is
// written and fsynced before each step, so a run that dies between the copy
// and the delete leaves both, and Undo removes the copies.
//
// # What Undo does
//
// Undo puts the originals back where they came from by moving the copies
// back (a rename on one drive, a copy-verify-remove across two), and every
// file it puts back is checked against the original's hash first: a file
// that does not hash the same is left where it is and listed. A cover Add
// embedded in a copy does not stop that: the copy's metadata before the
// cover went in — the few kilobytes in front of the audio, which the FLAC
// writer never moves — is kept beside the journal, and the original is
// rebuilt from it and the copy's audio, then hashed like the rest. A copy
// whose original is still there (delete was off, or the run stopped before
// it) is simply removed. The library folders Add made go too, when they are
// empty again.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/artfetch"
	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
	"github.com/BrandonDedolph/core-desktop-app/internal/id3"
	"github.com/BrandonDedolph/core-desktop-app/internal/library"
	"github.com/BrandonDedolph/core-desktop-app/internal/organizer"
)

// songExts are what Add copies as songs: what library.ScanTree reads, and
// what the iPod plays. The match ignores case — a ".FLAC" from outside is
// named ".flac" in the library, which is the spelling the scan sees.
var songExts = map[string]string{".flac": "FLAC", ".mp3": "MP3"}

// unplayableExts are music the iPod's firmware has no decoder for. An album
// of them is listed, with the reason, and never copied: a file the library
// cannot index is a file nobody will hear.
var unplayableExts = map[string]bool{
	".m4a": true, ".aac": true, ".alac": true, ".ogg": true, ".oga": true, ".opus": true,
	".wav": true, ".aif": true, ".aiff": true, ".wma": true, ".ape": true, ".wv": true,
	".mpc": true, ".dsf": true, ".dff": true,
}

// AddPlan is what PlanAdd found in a folder.
type AddPlan struct {
	// Source and Library are absolute.
	Source  string `json:"source"`
	Library string `json:"library"`
	// Albums are every album found, addable or not, sorted by name.
	Albums []AddAlbum `json:"albums"`
	// Songs and Bytes are every song found; Formats counts them by format
	// ("FLAC": 40, "MP3": 12, "M4A": 9 for the ones that cannot be added).
	Songs   int            `json:"songs"`
	Bytes   int64          `json:"bytes"`
	Formats map[string]int `json:"formats"`
	// Other counts the files that are neither music nor a cover (a .cue,
	// a .log). They are not copied and stay where they are, and so does
	// the folder they are in.
	Other int `json:"other,omitempty"`
}

// AddAlbum is one album of an AddPlan.
type AddAlbum struct {
	// Key names the album inside the plan: the folder it will get in the
	// library, or "?…<source folder>" for one that cannot be added.
	Key AlbumKey `json:"key"`
	// Name is the folder it will get in the library ("Album - Artist", the
	// organizer's rule), or the source folder's own name when it has none.
	Name   string `json:"name"`
	Album  string `json:"album,omitempty"`
	Artist string `json:"artist,omitempty"`
	// Source is the folder its first song is in.
	Source string `json:"source"`
	// Files are the songs and the cover pictures, each with the place it
	// will go. Empty for an album that cannot be added.
	Files []AddFile `json:"files,omitempty"`
	Songs int       `json:"songs"`
	Bytes int64     `json:"bytes"`
	// HasCover is whether the art source — the song the iPod reads a
	// cover from — carries one already.
	HasCover bool `json:"has_cover"`
	// Problem is why the album cannot be added; "" when it can.
	Problem string `json:"problem,omitempty"`
	// InLibrary is why the album looks like one the library already has
	// ("already in your library, as …"); it starts unticked, and a person
	// may tick it anyway.
	InLibrary string `json:"in_library,omitempty"`
}

// CanAdd is whether the album can be ticked at all.
func (a AddAlbum) CanAdd() bool { return a.Problem == "" }

// Ticked is whether the album starts ticked: it can be added and the
// library does not have it.
func (a AddAlbum) Ticked() bool { return a.Problem == "" && a.InLibrary == "" }

// Why is the reason an album starts unticked, or "".
func (a AddAlbum) Why() string {
	if a.Problem != "" {
		return a.Problem
	}
	return a.InLibrary
}

// AddFile is one file to copy.
type AddFile struct {
	From string `json:"from"`
	To   string `json:"to"`
	Size int64  `json:"size"`
	// Song is false for a cover picture travelling with its album.
	Song bool `json:"song"`
}

// Ticked is the keys of the albums that start ticked, in plan order.
func (p *AddPlan) Ticked() []AlbumKey {
	var out []AlbumKey
	for _, a := range p.Albums {
		if a.Ticked() {
			out = append(out, a.Key)
		}
	}
	return out
}

// Album is the album with this key.
func (p *AddPlan) Album(key AlbumKey) (AddAlbum, bool) {
	for _, a := range p.Albums {
		if a.Key == key {
			return a, true
		}
	}
	return AddAlbum{}, false
}

// readAny reads a FLAC's metadata, or an MP3's as the same shape: the tags
// under the names flac.Meta.Tag asks for, the pictures, and a length.
func readAny(path string) (*flac.Meta, error) {
	if !strings.EqualFold(filepath.Ext(path), ".mp3") {
		return flac.ReadFile(path)
	}
	m, err := id3.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &flac.Meta{
		Info:     flac.StreamInfo{SampleRate: m.SampleRate, TotalSamples: m.TotalSamples},
		Tags:     m.Tags,
		Pictures: m.Pictures,
	}, nil
}

func (o Options) anyMeta() func(string) (*flac.Meta, error) {
	if o.Meta != nil {
		return o.Meta
	}
	return readAny
}

// addSong is one song as the plan reads it.
type addSong struct {
	path, srcAlbum string // the file, and its album folder in the source
	ext            string // lower case
	size           int64
	meta           *flac.Meta
	album, title   string
	albumArtist    string
	artist         string
	track, disc    int
	folderDisc     bool // it sits in a "Disc N" folder in the source
}

// addGroup is the songs of one album, as they are gathered.
type addGroup struct {
	album  string
	songs  []*addSong
	srcs   []string // the source album folders, in the order met
	artist string
	folder string
}

// PlanAdd reads src and says what adding it to library would do. It writes
// nothing and makes no network request.
//
// o is the library's own options: Meta (tests), DiscFolders (a flat album
// whose tags carry several discs gets "Disc N" folders when it is on) and
// SkipDuplicates (the same-songs comparison with the library is left out;
// the same-folder check stays).
func PlanAdd(ctx context.Context, src, lib string, o Options) (*AddPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("add: no folder to add")
	}
	if strings.TrimSpace(lib) == "" {
		return nil, errors.New("add: no music folder to add to (choose it on the Fix tab, or pass --to)")
	}
	root, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	libRoot, err := filepath.Abs(lib)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("add: %s is not a folder", src)
	}
	if st, err := os.Stat(libRoot); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("add: the music folder %s is not there", lib)
	}
	switch {
	case samePath(root, libRoot):
		return nil, errors.New("add: that is the music folder itself; the Fix tab sorts what is already in it")
	case within(libRoot, root):
		return nil, fmt.Errorf("add: %s is inside the music folder already; the Fix tab sorts it into place", root)
	case within(root, libRoot):
		return nil, fmt.Errorf("add: %s holds the music folder; choose a folder beside it", root)
	}

	plan := &AddPlan{Source: root, Library: libRoot, Formats: map[string]int{}}
	var songs []*addSong
	unplayable := map[string]map[string]int{} // source album folder -> ext -> count
	unplayableBytes := map[string]int64{}
	covers := map[string][]string{} // source folder -> cover files
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir // .core-trash, .git, a Mac's .Trashes
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() || strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case songExts[ext] != "":
			dir := filepath.Dir(p)
			songs = append(songs, &addSong{path: p, srcAlbum: albumDirOf(p), ext: ext, size: fi.Size(),
				folderDisc: isDiscDir(dir) && dir != root})
		case unplayableExts[ext]:
			a := albumDirOf(p)
			if unplayable[a] == nil {
				unplayable[a] = map[string]int{}
			}
			unplayable[a][ext]++
			unplayableBytes[a] += fi.Size()
			plan.Formats[strings.ToUpper(strings.TrimPrefix(ext, "."))]++
		case isCoverFile(name):
			covers[filepath.Dir(p)] = append(covers[filepath.Dir(p)], p)
		default:
			plan.Other++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	read := o.anyMeta()
	unread := map[string]int{}
	untagged := map[string][]*addSong{}
	groups := map[string]*addGroup{}
	var order []string
	for i, s := range songs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		o.progress(JobAdd, "reading the tags", i+1, len(songs))
		plan.Formats[songExts[s.ext]]++
		m, err := read(s.path)
		if err != nil || m == nil {
			unread[s.srcAlbum]++
			continue
		}
		s.meta = m
		s.album = strings.TrimSpace(m.Tag("album"))
		s.albumArtist = strings.TrimSpace(m.Tag("albumartist", "album_artist", "album artist"))
		s.artist = strings.TrimSpace(m.Tag("artist"))
		s.title = strings.TrimSpace(m.Tag("title"))
		s.track = library.LeadInt(m.Tag("tracknumber", "track"))
		s.disc = library.LeadInt(m.Tag("discnumber", "disc"))
		if s.album == "" || (s.albumArtist == "" && s.artist == "") {
			untagged[s.srcAlbum] = append(untagged[s.srcAlbum], s)
			continue
		}
		// One album per source folder and album tag, the organizer's unit;
		// two folders holding one album (CD 1, CD 2) meet below, by name.
		k := foldKey(s.srcAlbum) + "\x00" + library.NormKey(s.album)
		g := groups[k]
		if g == nil {
			g = &addGroup{album: s.album}
			groups[k] = g
			order = append(order, k)
		}
		g.songs = append(g.songs, s)
	}

	// The artist each group's folder is named after, then the groups that
	// name the same folder, merged.
	merged := map[string]*addGroup{}
	var names []string
	for _, k := range order {
		g := groups[k]
		g.artist = decideAddArtist(g.songs)
		if g.artist == "" {
			// A split vote is a question for a person: every track of
			// the album would carry whichever name was guessed.
			a := problemAlbum(root, g.songs[0].srcAlbum, g.songs,
				"the songs name different artists and no album artist")
			a.Key = AlbumKey("?artists:" + rel(root, g.songs[0].srcAlbum) + "/" + g.album)
			a.Name, a.Album = g.album, g.album
			plan.Albums = append(plan.Albums, a)
			continue
		}
		g.folder = organizer.CanonicalFolder(g.album, g.artist)
		mk := library.NormKey(g.folder)
		if m := merged[mk]; m != nil {
			m.songs = append(m.songs, g.songs...)
			continue
		}
		merged[mk] = g
		names = append(names, mk)
	}

	sources := map[string]map[string]bool{} // source folder -> the album keys its songs went to
	for _, mk := range names {
		g := merged[mk]
		for _, s := range g.songs {
			dir := filepath.Dir(s.path)
			if sources[dir] == nil {
				sources[dir] = map[string]bool{}
			}
			sources[dir][mk] = true
		}
	}
	for _, mk := range names {
		plan.Albums = append(plan.Albums, planAlbum(root, libRoot, merged[mk], covers, sources, mk, o.DiscFolders))
	}
	for dir, list := range untagged {
		plan.Albums = append(plan.Albums, problemAlbum(root, dir, list,
			fmt.Sprintf("no tags: %s no album or artist", songsCarry(len(list)))))
	}
	for dir, n := range unread {
		a := problemAlbum(root, dir, nil, fmt.Sprintf("%s could not be read", countSongs(n)))
		a.Key = AlbumKey("?unreadable:" + rel(root, dir))
		a.Songs = n
		plan.Albums = append(plan.Albums, a)
	}
	for dir, exts := range unplayable {
		var parts []string
		n := 0
		for _, e := range sortedKeysOf(exts) {
			parts = append(parts, fmt.Sprintf("%d %s", exts[e], e))
			n += exts[e]
		}
		a := problemAlbum(root, dir, nil, strings.Join(parts, ", ")+": a format the iPod cannot play")
		a.Key = AlbumKey("?format:" + rel(root, dir))
		a.Songs, a.Bytes = n, unplayableBytes[dir]
		plan.Albums = append(plan.Albums, a)
	}

	if err := markInLibrary(ctx, plan, o); err != nil {
		return nil, err
	}
	for _, a := range plan.Albums {
		plan.Songs += a.Songs
		plan.Bytes += a.Bytes
	}
	sort.SliceStable(plan.Albums, func(i, j int) bool {
		ni, nj := strings.ToLower(plan.Albums[i].Name), strings.ToLower(plan.Albums[j].Name)
		if ni != nj {
			return ni < nj
		}
		return plan.Albums[i].Key < plan.Albums[j].Key
	})
	o.log(JobAdd, "%s: %d album(s), %d song(s); %d to add, %d already in the library, %d that cannot be added",
		root, len(plan.Albums), plan.Songs, countWhere(plan.Albums, AddAlbum.Ticked),
		countWhere(plan.Albums, func(a AddAlbum) bool { return a.CanAdd() && a.InLibrary != "" }),
		countWhere(plan.Albums, func(a AddAlbum) bool { return !a.CanAdd() }))
	return plan, nil
}

// planAlbum names every file of one album: the organizer's rules for a tree,
// applied to files arriving from outside it.
func planAlbum(root, lib string, g *addGroup, covers map[string][]string, sources map[string]map[string]bool,
	mk string, discFolders bool) AddAlbum {
	a := AddAlbum{
		Key:    AlbumKey(g.folder),
		Name:   g.folder,
		Album:  g.album,
		Artist: g.artist,
		Source: g.songs[0].srcAlbum,
	}
	for _, s := range g.songs {
		a.Songs++
		a.Bytes += s.size
	}

	discOf := func(s *addSong) int {
		if s.disc > 0 {
			return s.disc
		}
		return 1
	}
	multi, hasDiscDirs, width := false, false, 2
	first := discOf(g.songs[0])
	var noTitle, noNumber int
	for _, s := range g.songs {
		if discOf(s) != first {
			multi = true
		}
		if s.folderDisc {
			hasDiscDirs = true
		}
		if s.track >= 100 {
			width = 3
		}
		if s.title == "" {
			noTitle++
		}
		if s.track <= 0 {
			noNumber++
		}
	}
	switch {
	case noNumber > 0:
		a.Problem = fmt.Sprintf("no track numbers: %s none", songsCarry(noNumber))
		return a
	case noTitle > 0:
		a.Problem = fmt.Sprintf("no titles: %s none", songsCarry(noTitle))
		return a
	}
	seen := map[[2]int]int{}
	for _, s := range g.songs {
		k := [2]int{discOf(s), s.track}
		seen[k]++
		if seen[k] == 2 {
			a.Problem = fmt.Sprintf("two songs are both disc %d, track %d", k[0], k[1])
			if !multi {
				a.Problem = fmt.Sprintf("two songs are both track %d", k[1])
			}
			return a
		}
	}

	useDisc := multi && (discFolders || hasDiscDirs)
	flatMulti := multi && !useDisc
	dir := filepath.Join(lib, g.folder)
	for _, s := range g.songs {
		number := organizer.TrackNumber(s.track, width)
		if flatMulti {
			number = fmt.Sprintf("%d-%s", discOf(s), number)
		}
		to := filepath.Join(dir, organizer.CanonicalDisc(discOf(s), useDisc),
			organizer.CanonicalNumbered(number, g.artist, s.title, s.ext))
		a.Files = append(a.Files, AddFile{From: s.path, To: to, Size: s.size, Song: true})
	}
	sortEnumeration(a.Files, dir)
	for _, s := range g.songs {
		if s.path == a.Files[0].From {
			a.HasCover = s.meta.FrontCover() != nil
		}
	}

	// The pictures of a source folder go with the album when every song in
	// that folder is this album's: a folder of two albums' songs has no
	// cover that belongs to either.
	taken := map[string]bool{}
	for _, srcDir := range sortedSet(g.songs) {
		if len(sources[srcDir]) != 1 || !sources[srcDir][mk] {
			continue
		}
		for _, c := range covers[srcDir] {
			name := strings.ToLower(filepath.Base(c))
			if taken[name] {
				continue
			}
			taken[name] = true
			fi, err := os.Stat(c)
			if err != nil {
				continue
			}
			a.Files = append(a.Files, AddFile{From: c, To: filepath.Join(dir, name), Size: fi.Size()})
			a.Bytes += fi.Size()
		}
	}
	return a
}

// sortEnumeration puts the songs in the order library enumerates them — the
// album folder's own files, then each "Disc N" folder's — so Files[0] is the
// art source.
func sortEnumeration(files []AddFile, dir string) {
	key := func(f AddFile) string {
		d := filepath.Dir(f.To)
		if d == dir {
			return "0\x00" + filepath.Base(f.To)
		}
		return "1\x00" + filepath.Base(d) + "\x00" + filepath.Base(f.To)
	}
	sort.SliceStable(files, func(i, j int) bool { return key(files[i]) < key(files[j]) })
}

func sortedSet(songs []*addSong) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range songs {
		d := filepath.Dir(s.path)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// problemAlbum is an album that cannot be added, named after its folder.
func problemAlbum(root, dir string, songs []*addSong, why string) AddAlbum {
	a := AddAlbum{Key: AlbumKey("?" + rel(root, dir)), Name: filepath.Base(dir), Source: dir, Problem: why}
	if samePath(dir, root) {
		a.Name = filepath.Base(root)
	}
	for _, s := range songs {
		a.Songs++
		a.Bytes += s.size
	}
	return a
}

// decideAddArtist is the organizer's rule: the majority album artist, else
// the majority artist, and a tie is no answer.
func decideAddArtist(songs []*addSong) string {
	pick := func(vals []string) (string, bool) {
		count, first := map[string]int{}, map[string]string{}
		var keys []string
		for _, v := range vals {
			k := library.NormKey(library.Straighten(v))
			if count[k] == 0 {
				first[k] = v
				keys = append(keys, k)
			}
			count[k]++
		}
		best, bestN, ties := "", 0, 0
		for _, k := range keys {
			switch n := count[k]; {
			case n > bestN:
				best, bestN, ties = k, n, 1
			case n == bestN:
				ties++
			}
		}
		if bestN == 0 || ties > 1 {
			return "", false
		}
		return first[best], true
	}
	var albumArtists, artists []string
	for _, s := range songs {
		if s.albumArtist != "" {
			albumArtists = append(albumArtists, s.albumArtist)
		}
		if s.artist != "" {
			artists = append(artists, s.artist)
		}
	}
	if len(albumArtists) > 0 {
		a, _ := pick(albumArtists)
		return a
	}
	a, _ := pick(artists)
	return a
}

// markInLibrary marks the albums the library already has: the same folder
// (as the iPod's own name comparison sees it), or the duplicate check's
// same-songs rule against every album in the library.
func markInLibrary(ctx context.Context, plan *AddPlan, o Options) error {
	ents, err := os.ReadDir(plan.Library)
	if err != nil {
		return err
	}
	folders := map[string]string{}
	for _, e := range ents {
		if e.IsDir() {
			folders[library.NormKey(e.Name())] = e.Name()
		}
	}
	var idx []int
	for i := range plan.Albums {
		a := &plan.Albums[i]
		if !a.CanAdd() {
			continue
		}
		if have, ok := folders[library.NormKey(a.Name)]; ok {
			a.InLibrary = "already in your library, as " + have
			continue
		}
		idx = append(idx, i)
	}
	if o.SkipDuplicates || len(idx) == 0 {
		return nil
	}

	o.progress(JobAdd, "comparing with your library", 0, 0)
	libAlbums, err := albumsInExt(plan.Library, ".flac", ".mp3")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	memo := newTrackMemo(o.anyMeta())
	all := dupAlbums(plan.Library, libAlbums, memo)
	nLib := len(all)
	for _, i := range idx {
		a := plan.Albums[i]
		d := &dupAlbum{ref: AlbumRef{Key: a.Key}}
		for _, f := range a.Files {
			if !f.Song {
				continue
			}
			info := memo.get(f.From)
			d.tracks = append(d.tracks, dupTrack{trackInfo: info})
			d.bytes += info.size
		}
		all = append(all, d)
	}
	dm := matchAll(all)
	for k, i := range idx {
		for j := 0; j < nLib; j++ {
			if _, in := dm.contained(nLib+k, j); in {
				plan.Albums[i].InLibrary = "the same songs are in your library, in " + filepath.Base(all[j].ref.Dir)
				break
			}
		}
	}
	return nil
}

// --------------------------------------------------------------- the add

// AddResult is what Add did.
type AddResult struct {
	// Journal is the undo journal, or "" when nothing was written.
	Journal string `json:"journal,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
	// Added are the albums now in the library, copies verified.
	Added []AddedAlbum `json:"added"`
	// Deleted counts the originals removed; DirsRemoved the source folders
	// that were then empty and went too.
	Deleted     int      `json:"deleted"`
	DirsRemoved []string `json:"dirs_removed,omitempty"`
	// Covers counts the albums that got a cover.
	Covers int `json:"covers"`
	// Failures are the albums that were not added (their originals are
	// untouched) and the steps after the copy that did not finish (an
	// original that would not delete, a cover that would not download).
	Failures []Failure `json:"failures,omitempty"`
}

// AddedAlbum is one album Add put in the library.
type AddedAlbum struct {
	Key   AlbumKey `json:"key"`
	Name  string   `json:"name"`
	Dir   string   `json:"dir"`
	Songs int      `json:"songs"`
	Bytes int64    `json:"bytes"`
	// Cover is where the cover the album was given came from ("cover.jpg",
	// "itunes"), or "".
	Cover string `json:"cover,omitempty"`
}

// Albums is how many albums were added.
func (r *AddResult) Albums() int {
	if r == nil {
		return 0
	}
	return len(r.Added)
}

// The two seams the tests reach in through: a copy that is not what was read
// (the hash check has to catch it), and a run that stops dead at a stage, the
// way a pulled plug or a killed process does — no clean-up, no journal update.
var (
	testAfterCopy func(path string)
	testStopAt    func(stage string) bool
)

// errStopped is the test seam's "the process died here".
var errStopped = errors.New("add: stopped (test)")

// Add copies the chosen albums of plan into the library. deleteOriginals
// removes each album's originals once every one of its copies verified.
//
// One album failing is a Failure, and the others are still done; a cancel
// stops after cleaning up the album it was in. o.Art (nil: folder covers
// only) is asked for a cover for an album with none; o.DryRun decides and
// writes nothing.
func Add(ctx context.Context, plan *AddPlan, chosen []AlbumKey, deleteOriginals bool, o Options) (*AddResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if plan == nil {
		return nil, errors.New("add: no plan")
	}
	var albums []AddAlbum
	seen := map[AlbumKey]bool{}
	for _, k := range chosen {
		if seen[k] {
			continue
		}
		seen[k] = true
		a, ok := plan.Album(k)
		if !ok {
			return nil, fmt.Errorf("add: %s is not in this plan", k)
		}
		if !a.CanAdd() {
			return nil, fmt.Errorf("add: %s cannot be added: %s", a.Name, a.Problem)
		}
		albums = append(albums, a)
	}
	res := &AddResult{DryRun: o.DryRun}
	if len(albums) == 0 {
		return res, nil
	}
	var total int64
	for _, a := range albums {
		total += a.Bytes
	}
	if o.DryRun {
		for _, a := range albums {
			o.log(JobAdd, "would add %s (%s) to %s", a.Name, countSongs(a.Songs), plan.Library)
			res.Added = append(res.Added, AddedAlbum{Key: a.Key, Name: a.Name,
				Dir: filepath.Join(plan.Library, a.Name), Songs: a.Songs, Bytes: a.Bytes})
		}
		return res, nil
	}

	j, err := newFixJournal(plan.Library, o)
	if err != nil {
		return nil, err
	}
	j.Format = addJournalFormat
	j.Previous = o.After
	if err := j.write(); err != nil {
		j.close()
		return nil, err
	}
	defer j.close()

	var done int64
	var stop error
	for i, a := range albums {
		if err := ctx.Err(); err != nil {
			stop = err
			break
		}
		step := fmt.Sprintf("adding %d of %d", i+1, len(albums))
		progress := func(n int64) {
			done += n
			pct := float32(-1)
			if total > 0 {
				pct = float32(done) / float32(total)
			}
			o.emit(Event{Kind: EventProgress, Job: JobAdd, Text: step, Pct: pct})
		}
		progress(0)
		added, err := addAlbum(ctx, j, plan, a, deleteOriginals, o, res, progress)
		if errors.Is(err, errStopped) {
			return res, err
		}
		if err != nil {
			res.Failures = append(res.Failures, Failure{Album: a.Key, Reason: err.Error()})
			o.log(JobAdd, "%s: not added — %v; the originals are where they were", a.Name, err)
			if ctx.Err() != nil {
				stop = ctx.Err()
				break
			}
			continue
		}
		res.Added = append(res.Added, added)
	}
	if err := j.write(); err != nil {
		return res, err
	}
	if len(j.Adds) == 0 {
		// Nothing landed: no journal, and the session's previous one is
		// still the one Undo takes back.
		j.close()
		_ = os.Remove(j.Path)
		_ = os.RemoveAll(j.blobDir())
	} else {
		res.Journal = j.Path
	}
	o.log(JobAdd, "added %d album(s); %d original file(s) deleted; %d cover(s); %d not added",
		len(res.Added), res.Deleted, res.Covers, countFailedAlbums(res, albums))
	return res, stop
}

func countFailedAlbums(res *AddResult, albums []AddAlbum) int {
	added := map[AlbumKey]bool{}
	for _, a := range res.Added {
		added[a.Key] = true
	}
	n := 0
	for _, a := range albums {
		if !added[a.Key] {
			n++
		}
	}
	return n
}

// addAlbum is one album: copy, verify, delete, cover. An error means the
// album is not in the library and its originals are untouched.
func addAlbum(ctx context.Context, j *fixJournal, plan *AddPlan, a AddAlbum, deleteOriginals bool, o Options,
	res *AddResult, progress func(int64)) (AddedAlbum, error) {
	dir := filepath.Join(plan.Library, a.Name)
	for _, f := range a.Files {
		fi, err := os.Stat(f.From)
		if err != nil || fi.Size() != f.Size {
			return AddedAlbum{}, fmt.Errorf("%s changed since the folder was read (read it again)", filepath.Base(f.From))
		}
		// A destination that is there already is found now, before any file
		// of the album is copied or journaled: an op in the journal that
		// names a file this run did not make is one a crash would hand to
		// Undo as its own to remove.
		if _, err := os.Lstat(f.To); err == nil {
			return AddedAlbum{}, fmt.Errorf("%s is already in the library; nothing was written over it", f.To)
		}
	}

	first := len(j.Adds)
	firstDir := len(j.AddDirs)
	var copied int64
	clean := func(err error) (AddedAlbum, error) {
		for i := len(j.Adds) - 1; i >= first; i-- {
			if j.Adds[i].created {
				_ = os.Remove(j.Adds[i].To)
			}
		}
		for i := len(j.AddDirs) - 1; i >= firstDir; i-- {
			_ = os.Remove(j.AddDirs[i])
		}
		j.Adds, j.AddDirs = j.Adds[:first], j.AddDirs[:firstDir]
		_ = j.write()
		progress(-copied)
		return AddedAlbum{}, err
	}

	// The folders, each one Add makes recorded before it is made.
	for _, f := range a.Files {
		if err := j.makeDirs(plan.Library, filepath.Dir(f.To)); err != nil {
			return clean(err)
		}
	}

	// The copies. Each is in the journal before its first byte is written.
	for _, f := range a.Files {
		if err := ctx.Err(); err != nil {
			return clean(err)
		}
		fi, err := os.Stat(f.From)
		if err != nil {
			return clean(err)
		}
		j.Adds = append(j.Adds, addOp{Album: a.Key, From: f.From, To: f.To, Size: f.Size,
			MTimeNS: fi.ModTime().UnixNano()})
		op := &j.Adds[len(j.Adds)-1]
		if err := j.write(); err != nil {
			return clean(err)
		}
		sum, made, err := copyHashed(f.From, f.To, fi)
		op.created = made
		if err != nil {
			return clean(err)
		}
		op.SHA256, op.Copied = sum, true
		copied += f.Size
		progress(f.Size)
		if testAfterCopy != nil {
			testAfterCopy(f.To)
		}
	}
	if err := j.write(); err != nil {
		return clean(err)
	}

	// Every copy read back from the disk and compared with the original.
	for i := first; i < len(j.Adds); i++ {
		op := &j.Adds[i]
		got, err := hashFile(op.To)
		if err != nil {
			return clean(fmt.Errorf("reading the copy of %s back: %w", filepath.Base(op.From), err))
		}
		if got != op.SHA256 {
			return clean(fmt.Errorf("the copy of %s did not match the original; nothing of this album was kept",
				filepath.Base(op.From)))
		}
		op.Verified = true
	}
	if err := j.write(); err != nil {
		return clean(err)
	}
	if testStopAt != nil && testStopAt("verified") {
		return AddedAlbum{}, errStopped
	}
	o.log(JobAdd, "%s: %d file(s) copied to %s and verified", a.Name, len(a.Files), dir)

	// Only now: the originals, then their folders when that empties them.
	if deleteOriginals {
		dirs := map[string]bool{}
		for i := first; i < len(j.Adds); i++ {
			op := &j.Adds[i]
			if err := os.Remove(op.From); err != nil {
				res.Failures = append(res.Failures, Failure{Album: a.Key, Path: op.From,
					Reason: "the original could not be deleted, so it is still there: " + err.Error()})
				continue
			}
			op.Deleted = true
			res.Deleted++
			if err := j.write(); err != nil {
				return AddedAlbum{}, err
			}
			dirs[filepath.Dir(op.From)] = true
		}
		for _, d := range emptyFolders(plan.Source, dirs) {
			if os.Remove(d) == nil {
				res.DirsRemoved = append(res.DirsRemoved, d)
			}
		}
	}

	added := AddedAlbum{Key: a.Key, Name: a.Name, Dir: dir, Songs: a.Songs, Bytes: a.Bytes}
	if !a.HasCover {
		from, err := addCover(ctx, j, a, first, o)
		switch {
		case err != nil:
			res.Failures = append(res.Failures, Failure{Album: a.Key, Reason: "no cover: " + err.Error()})
			o.log(JobAdd, "%s: added without a cover — %v", a.Name, err)
		case from != "":
			added.Cover = from
			res.Covers++
			o.log(JobAdd, "%s: cover from %s", a.Name, from)
		}
	}
	return added, nil
}

// emptyFolders is the folders to try removing once their files are gone,
// deepest first, each one's parents up to (and including) the folder that
// was chosen. os.Remove refuses a folder with anything left in it, which is
// the rule: a .cue or a .log keeps its folder.
func emptyFolders(root string, dirs map[string]bool) []string {
	all := map[string]bool{}
	for d := range dirs {
		for p := d; within(root, p) || samePath(root, p); p = filepath.Dir(p) {
			all[p] = true
			if samePath(root, p) {
				break
			}
		}
	}
	var out []string
	for d := range all {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// addCover finds a cover for an album with none and embeds it in every FLAC
// of the copies: the album's own picture file first, then the art client
// (the internet, when Settings allows it). It returns where the cover came
// from, or "" when there was none to have.
func addCover(ctx context.Context, j *fixJournal, a AddAlbum, first int, o Options) (string, error) {
	var flacs []int
	for i := first; i < len(j.Adds); i++ {
		if strings.EqualFold(filepath.Ext(j.Adds[i].To), ".flac") {
			flacs = append(flacs, i)
		}
	}
	if len(flacs) == 0 {
		return "", nil // an MP3 album: there is no ID3 writer to put one in with
	}
	var data []byte
	var mime, from string
	if p, _, _ := localCover(filepath.Join(j.Root, a.Name)); p != "" {
		d, m, err := readLocal(artfetch.Candidate{FullURL: p})
		if err == nil {
			data, mime, from = d, m, filepath.Base(p)
		}
	}
	if data == nil && o.Art != nil {
		cands, err := o.Art.Find(ctx, artfetch.Query{Artist: a.Artist, Album: a.Album})
		if err != nil && !errors.Is(err, artfetch.ErrNoMatch) {
			return "", err
		}
		var top *artfetch.Candidate
		for i := range cands {
			if top == nil || cands[i].Score > top.Score {
				top = &cands[i]
			}
		}
		if top == nil || top.Score+1e-9 < DefaultMinScore {
			return "", nil // nothing sure enough to put on the iPod unasked
		}
		d, m, err := o.Art.Fetch(ctx, *top)
		if err != nil {
			return "", err
		}
		data, mime, from = d, m, top.Provider
	}
	if data == nil {
		return "", nil
	}
	write := o.writePicture()
	for _, i := range flacs {
		op := &j.Adds[i]
		off, err := flac.AudioOffset(op.To)
		if err != nil {
			return "", err
		}
		head, err := readHead(op.To, off)
		if err != nil {
			return "", err
		}
		if op.Head, err = j.keep(head, ".head"); err != nil {
			return "", err
		}
		if err := j.write(); err != nil {
			return "", err
		}
		if err := write(op.To, data, mime); err != nil {
			return "", err
		}
		if op.CoverSize, op.CoverMTimeNS, err = stampOf(op.To); err != nil {
			return "", err
		}
		op.Covered = true
		if err := j.write(); err != nil {
			return "", err
		}
	}
	return from, nil
}

func readHead(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, err
	}
	return b, nil
}

// copyHashed copies from to a NEW file at to (never over one that is there),
// hashing the bytes as they are read, and gives the copy the original's
// modification time. made says the file at to is this call's.
func copyHashed(from, to string, fi os.FileInfo) (sum string, made bool, err error) {
	in, err := os.Open(from)
	if err != nil {
		return "", false, err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return "", false, fmt.Errorf("%s is already in the library; nothing was written over it", to)
	}
	if err != nil {
		return "", false, err
	}
	h := sha256.New()
	if _, err := io.Copy(out, io.TeeReader(in, h)); err != nil {
		out.Close()
		return "", true, err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return "", true, err
	}
	if err := out.Close(); err != nil {
		return "", true, err
	}
	_ = os.Chtimes(to, fi.ModTime(), fi.ModTime())
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ------------------------------------------------------------- the journal

// addJournalFormat is the format of a journal that carries Adds. A build
// before Add music… would read the list as nothing and report an undo that
// put nothing back, so it refuses the journal instead. (3, not 2: 2 is the
// tag editor's.)
const addJournalFormat = 3

// addOp is one file Add copied.
type addOp struct {
	Album AlbumKey `json:"album"`
	// From is the original, To the copy in the library.
	From string `json:"from"`
	To   string `json:"to"`
	// SHA256 is the original's content as it was read for the copy; Size
	// and MTimeNS are the original's, which the copy is given too.
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size"`
	MTimeNS int64  `json:"mtime_ns"`
	// Copied: the copy is complete. Verified: it was read back and hashed
	// the same. Deleted: the original is gone.
	Copied   bool `json:"copied,omitempty"`
	Verified bool `json:"verified,omitempty"`
	Deleted  bool `json:"deleted,omitempty"`
	// Head is the blob holding the copy's metadata before a cover was
	// embedded in it; Covered says the cover went in, and CoverSize and
	// CoverMTimeNS are the copy as the cover left it.
	Head         string `json:"head,omitempty"`
	Covered      bool   `json:"covered,omitempty"`
	CoverSize    int64  `json:"cover_size,omitempty"`
	CoverMTimeNS int64  `json:"cover_mtime_ns,omitempty"`

	// created is this run's own knowledge that the file at To is its own.
	created bool
}

// makeDirs creates dir and the folders above it that are missing, up to the
// library, recording each in the journal before it is made.
func (j *fixJournal) makeDirs(lib, dir string) error {
	var missing []string
	for p := dir; within(lib, p); p = filepath.Dir(p) {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		missing = append(missing, p)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		j.AddDirs = append(j.AddDirs, missing[i])
		if err := j.write(); err != nil {
			return err
		}
		if err := os.Mkdir(missing[i], 0o755); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

// undoAdds takes the adds back, newest first.
func undoAdds(ctx context.Context, j *fixJournal, rep *organizer.UndoReport, o Options) error {
	skip := func(op addOp, why string) {
		rep.Skipped = append(rep.Skipped, organizer.Skip{
			Op: organizer.Op{From: op.From, To: op.To, Reason: "add"}, Reason: why})
	}
	for i := len(j.Adds) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		op := j.Adds[i]
		o.progress(JobAdd, "putting back "+filepath.Base(op.From), len(j.Adds)-i, len(j.Adds))
		_, errTo := os.Lstat(op.To)
		_, errFrom := os.Lstat(op.From)
		switch {
		case errTo != nil && errFrom == nil:
			// The copy is gone and the original is where it was.
			continue
		case errTo != nil:
			skip(op, "neither the original nor the copy is there any more")
			continue
		case errFrom == nil:
			// The original never left: the copy only has to go, and only
			// if it is still the one Add made.
			if op.Verified && !copyUnchanged(op) {
				skip(op, "the copy changed after it was added; left alone")
				continue
			}
			if err := os.Remove(op.To); err != nil {
				skip(op, err.Error())
				continue
			}
			rep.Undone++
			continue
		}
		if !op.Verified {
			skip(op, "the original is gone and the copy was never verified; left in the library")
			continue
		}
		if err := restoreOriginal(j, op); err != nil {
			skip(op, err.Error())
			continue
		}
		rep.Undone++
	}
	for i := len(j.AddDirs) - 1; i >= 0; i-- {
		if os.Remove(j.AddDirs[i]) == nil {
			rep.DirsRemoved = append(rep.DirsRemoved, j.AddDirs[i])
		}
	}
	return nil
}

// copyUnchanged is whether the copy is as Add left it: the original's size
// and time, or the stamp the cover left.
func copyUnchanged(op addOp) bool {
	size, mt, err := stampOf(op.To)
	if err != nil {
		return false
	}
	if op.Covered {
		return size == op.CoverSize && mt == op.CoverMTimeNS
	}
	return size == op.Size && mt == op.MTimeNS
}

// restoreOriginal puts the original back from its copy: rebuilt (its own
// metadata and the copy's audio) when a cover went into the copy, moved
// otherwise. The result must hash as the original did, or nothing changes.
func restoreOriginal(j *fixJournal, op addOp) error {
	if err := os.MkdirAll(filepath.Dir(op.From), 0o755); err != nil {
		return err
	}
	if !op.Covered {
		if !copyUnchanged(op) {
			return errors.New("the copy changed after it was added; left alone")
		}
		sum, err := hashFile(op.To)
		if err != nil {
			return err
		}
		if sum != op.SHA256 {
			return errors.New("the copy no longer matches the original; left alone")
		}
		if err := os.Rename(op.To, op.From); err == nil {
			return nil
		}
		// Another drive: copy, check, then remove.
		return rebuild(op, nil)
	}
	if !copyUnchanged(op) {
		return errors.New("the copy changed after its cover went in; left alone")
	}
	head, err := j.blob(op.Head)
	if err != nil {
		return fmt.Errorf("the original's tags kept beside the journal are gone: %w", err)
	}
	return rebuild(op, head)
}

// rebuild writes the original beside where it goes — head (when given) and
// then the copy from its audio on, or the whole copy — checks the hash, and
// only then renames it into place and removes the copy.
func rebuild(op addOp, head []byte) error {
	in, err := os.Open(op.To)
	if err != nil {
		return err
	}
	defer in.Close()
	if head != nil {
		off, err := flac.AudioOffset(op.To)
		if err != nil {
			return err
		}
		if _, err := in.Seek(off, io.SeekStart); err != nil {
			return err
		}
	}
	tmp := op.From + ".core-undo"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	w := io.MultiWriter(out, h)
	_, err = io.Copy(w, io.MultiReader(bytes.NewReader(head), in))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != op.SHA256 {
		err = errors.New("the original could not be rebuilt exactly from the copy; left alone")
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	when := timeNS(op.MTimeNS)
	_ = os.Chtimes(tmp, when, when)
	if err := os.Rename(tmp, op.From); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	in.Close()
	return os.Remove(op.To)
}

// --------------------------------------------------------------- helpers

// within reports whether p is strictly inside root.
func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	if err != nil || r == "." {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func timeNS(ns int64) time.Time { return time.Unix(0, ns) }

func isCoverFile(name string) bool {
	for _, n := range coverFileNames {
		if strings.EqualFold(name, n) {
			return true
		}
	}
	return false
}

// countSongs is "1 song" / "12 songs".
func countSongs(n int) string {
	if n == 1 {
		return "1 song"
	}
	return fmt.Sprintf("%d songs", n)
}

// songsCarry is "1 song carries" / "3 songs carry".
func songsCarry(n int) string {
	if n == 1 {
		return "1 song carries"
	}
	return fmt.Sprintf("%d songs carry", n)
}

func countWhere(albums []AddAlbum, f func(AddAlbum) bool) int {
	n := 0
	for _, a := range albums {
		if f(a) {
			n++
		}
	}
	return n
}

func sortedKeysOf(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FormatsText is the formats line: "FLAC 40, MP3 12, M4A 9", most first.
func (p *AddPlan) FormatsText() string {
	keys := sortedKeysOf(p.Formats)
	sort.SliceStable(keys, func(i, j int) bool { return p.Formats[keys[i]] > p.Formats[keys[j]] })
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, p.Formats[k]))
	}
	return strings.Join(parts, ", ")
}
