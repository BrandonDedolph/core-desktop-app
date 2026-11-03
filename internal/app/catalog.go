package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/library"
	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// The Music tab's views (library plan, L1): Albums, Artists, Genres and
// Songs, the iPod's own top menu. All four come from the one plan the grid is
// drawn from — the scan behind it carries every track's artist, genre (through
// the genre map a sync uses), number and length — so nothing is read twice.
//
// BuildCatalog is pure: the grid's albums in, the groupings out, so "which
// artists, in which order, with which albums" is a table test and not a
// screenshot. The catalog holds indices into State.Albums rather than copies,
// so the NEW/CHANGED roll-up is read live from the albums (markSynced changes
// them in place) and costs nothing to keep in step.

// Song is one track of an album, as the Songs view lists it.
type Song struct {
	// Path is the file; empty in a canned state, where nothing plays.
	Path  string
	Title string
	// Artist is the track's own artist (its tag, a "feat." tail kept),
	// or the album's when the tag has none.
	Artist string
	// Genre is the genre the iPod shows: the tag's, through the genre map.
	Genre        string
	Disc, Number int
	Length       time.Duration
	Size         int64
	// State is this file's side of the plan: NEW (the iPod does not
	// have it), CHANGED (it has an older copy) or on the iPod.
	State TileState
}

// MusicView is one of the Music tab's four views.
type MusicView int

// The views, in the switch's order.
const (
	ViewAlbums MusicView = iota
	ViewArtists
	ViewGenres
	ViewSongs
)

// musicViews is the switch, left to right.
var musicViews = [...]MusicView{ViewAlbums, ViewArtists, ViewGenres, ViewSongs}

// String is the view's word in the switch.
func (v MusicView) String() string {
	switch v {
	case ViewArtists:
		return "Artists"
	case ViewGenres:
		return "Genres"
	case ViewSongs:
		return "Songs"
	}
	return "Albums"
}

// viewKey is the view as config.json's music_view spells it.
func viewKey(v MusicView) string { return strings.ToLower(v.String()) }

// viewFromKey reads music_view; anything unknown is Albums.
func viewFromKey(s string) MusicView {
	for _, v := range musicViews {
		if strings.EqualFold(strings.TrimSpace(s), viewKey(v)) {
			return v
		}
	}
	return ViewAlbums
}

// songsFromScan fills each album's Artist and Songs from the plan's scan,
// matched by the device folder (the plan's album name). A track's state is
// the plan's decision about its file.
func songsFromScan(albums []Album, plan *syncer.Plan) {
	if plan == nil || plan.Scan == nil {
		return
	}
	type fileFacts struct {
		state TileState
		size  int64
	}
	files := map[string]fileFacts{}
	for _, op := range plan.Copy {
		st := TileChanged
		if op.Why == syncer.WhyNew {
			st = TileNew
		}
		files[op.Src] = fileFacts{st, op.Size}
	}
	for _, ops := range [][]syncer.FileOp{plan.Skip, plan.Rename} {
		for _, op := range ops {
			files[op.Src] = fileFacts{TileOnDevice, op.Size}
		}
	}
	scanned := map[string]*library.Album{}
	for i := range plan.Scan.Albums {
		sa := &plan.Scan.Albums[i]
		scanned[sa.DeviceFolder] = sa
	}
	for i := range albums {
		a := &albums[i]
		sa := scanned[a.Device]
		if sa == nil {
			continue
		}
		a.Artist = sa.FolderArtist
		a.Songs = make([]Song, 0, len(sa.Tracks))
		for _, t := range sa.Tracks {
			artist := t.TagArtist
			if artist == "" {
				artist = sa.FolderArtist
			}
			f := files[t.SrcPath]
			a.Songs = append(a.Songs, Song{
				Path: t.SrcPath, Title: t.Title, Artist: artist, Genre: t.Genre,
				Disc: t.Disc, Number: t.Track, Length: time.Duration(t.DurationS) * time.Second,
				Size: f.size, State: f.state,
			})
		}
	}
}

// songTracks is an album's songs as the player's tracklist.
func songTracks(ss []Song) []Track {
	out := make([]Track, len(ss))
	for i, s := range ss {
		out[i] = Track{Path: s.Path, Disc: s.Disc, Number: s.Number, Title: s.Title, Length: s.Length, Size: s.Size}
	}
	return out
}

// --- the groupings ---------------------------------------------------------

// Artist is one tile of the Artists view.
type Artist struct {
	Name string
	// Albums are indices into State.Albums, by album title: the mosaic's
	// order and the artist sheet's.
	Albums []int
	Songs  int
	// Facts is "3 albums · 31 songs", made once here and not per frame.
	Facts string
	// pic is the picture's cache key without its size (artistPicKey).
	pic string
}

// Genre is one row of the Genres view.
type Genre struct {
	Name string
	// Albums are indices into State.Albums, in the grid's order.
	Albums  []int
	Artists int
	Facts   string
}

// SongRow is one row of the Songs view: the song's place, and the strings
// the row and the search read, made once per catalog.
type SongRow struct {
	Album, Song int
	Num, Time   string
	AlbumTitle  string
	// fold is title, artist and album lower-cased, for the search; the
	// three keys are the sort's, folded once rather than per comparison.
	fold                       string
	titleKey, artistKey, album string
}

// Catalog is the four views' groupings of one grid.
type Catalog struct {
	Artists []Artist
	Genres  []Genre
	Songs   []SongRow
}

// artistSortKey is the iPod's artist key (core/library/names.c artist_key):
// a leading "The " ignored, then case folded — so "The Kid LAROI" and "Kid
// LAROI" are one artist, sorted under K, and "blackbear" and "Blackbear" are
// one too.
func artistSortKey(name string) string {
	s := strings.TrimSpace(name)
	if len(s) > 4 && strings.EqualFold(s[:4], "the ") {
		s = s[4:]
	}
	return strings.ToUpper(strings.TrimSpace(s))
}

// leadMarks are where a track artist's lead ends and the rest begins: a
// multi-value tag's separators (";", " / ") and the guest spellings.
var leadMarks = []string{";", " / ", " feat. ", " feat ", " ft. ", " ft ", " featuring ",
	" (feat", " [feat", " (ft", " (with ", " [with "}

// leadArtist is a track artist's lead: "blackbear; G-Eazy" and "Kanye West
// feat. Jay-Z" are blackbear's and Kanye West's tracks. A guest on one song
// is not an artist with an album, and an Artists view with a tile per
// "feat." spelling — a real library's tags make one per collaboration — is a
// view nobody can find anything in. "&" and "," are left alone: they are in
// too many single names ("Simon & Garfunkel", "Tyler, The Creator").
func leadArtist(s string) string {
	low := strings.ToLower(s)
	cut := len(s)
	for _, m := range leadMarks {
		if i := strings.Index(low, m); i > 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(s[:cut])
}

// BuildCatalog groups the grid's albums.
//
// Artists: every album is under its own artist (the folder's, the one the
// iPod groups by), and also under each other artist that leads a track of it
// (leadArtist) — a compilation is under every artist on it, a split album
// under both. The album's own artist counts all its songs; another artist
// counts the songs they lead. Sorted the iPod's way (artistSortKey); the name shown is
// the first spelling met, the album artist's before a track's.
//
// Genres: an album is under every genre one of its tracks has; albums with no
// genre are in no row, as on the iPod. Sorted by name, case folded.
//
// Songs: every song, in the grid's order; the view sorts its own copy.
func BuildCatalog(albums []Album) *Catalog {
	cat := &Catalog{}
	type acc struct {
		name   string
		albums map[int]bool
		songs  int
	}
	artists := map[string]*acc{}
	at := func(name string) *acc {
		k := artistSortKey(name)
		if k == "" {
			return nil
		}
		a := artists[k]
		if a == nil {
			a = &acc{name: strings.TrimSpace(name), albums: map[int]bool{}}
			artists[k] = a
		}
		return a
	}
	own := make([]string, len(albums))
	for i, al := range albums {
		own[i] = al.Artist
		if own[i] == "" {
			own[i] = albumArtist(al)
		}
		if a := at(own[i]); a != nil {
			a.albums[i] = true
			a.songs += albumSongs(al)
		}
	}
	for i, al := range albums {
		ownKey := artistSortKey(own[i])
		for _, s := range al.Songs {
			p := leadArtist(s.Artist)
			if artistSortKey(p) == ownKey {
				continue
			}
			if a := at(p); a != nil {
				a.albums[i] = true
				a.songs++
			}
		}
	}
	for _, a := range artists {
		ar := Artist{Name: a.name, Songs: a.songs}
		for i := range a.albums {
			ar.Albums = append(ar.Albums, i)
		}
		sort.Slice(ar.Albums, func(x, y int) bool {
			tx, ty := strings.ToLower(tileTitle(albums[ar.Albums[x]])), strings.ToLower(tileTitle(albums[ar.Albums[y]]))
			if tx != ty {
				return tx < ty
			}
			return ar.Albums[x] < ar.Albums[y]
		})
		ar.Facts = nOf(len(ar.Albums), "album") + " · " + nOf(ar.Songs, "song")
		cat.Artists = append(cat.Artists, ar)
	}
	sort.Slice(cat.Artists, func(x, y int) bool {
		kx, ky := artistSortKey(cat.Artists[x].Name), artistSortKey(cat.Artists[y].Name)
		if kx != ky {
			return kx < ky
		}
		return cat.Artists[x].Name < cat.Artists[y].Name
	})
	for i := range cat.Artists {
		cat.Artists[i].pic = artistPicKey(cat.Artists[i], albums)
	}

	genres := map[string]*Genre{}
	var order []string
	for i, al := range albums {
		seen := map[string]bool{}
		for _, s := range al.Songs {
			g := strings.TrimSpace(s.Genre)
			k := strings.ToLower(g)
			if g == "" || seen[k] {
				continue
			}
			seen[k] = true
			gr := genres[k]
			if gr == nil {
				gr = &Genre{Name: g}
				genres[k] = gr
				order = append(order, k)
			}
			gr.Albums = append(gr.Albums, i)
		}
	}
	sort.Strings(order)
	for _, k := range order {
		g := genres[k]
		who := map[string]bool{}
		for _, i := range g.Albums {
			who[artistSortKey(own[i])] = true
		}
		g.Artists = len(who)
		g.Facts = nOf(g.Artists, "artist") + " · " + nOf(len(g.Albums), "album")
		cat.Genres = append(cat.Genres, *g)
	}

	for i, al := range albums {
		title := tileTitle(al)
		for j, s := range al.Songs {
			r := SongRow{Album: i, Song: j, AlbumTitle: title,
				fold:     strings.ToLower(s.Title + "\x00" + s.Artist + "\x00" + title),
				titleKey: strings.ToLower(s.Title), artistKey: artistSortKey(s.Artist), album: strings.ToLower(title)}
			if s.Number > 0 {
				r.Num = itoa(s.Number)
			}
			if s.Length > 0 {
				r.Time = clock(s.Length)
			}
			cat.Songs = append(cat.Songs, r)
		}
	}
	return cat
}

// albumSongs is how many songs an album has: its Songs when it carries them,
// else its track count.
func albumSongs(a Album) int {
	if len(a.Songs) > 0 {
		return len(a.Songs)
	}
	return a.Tracks
}

// nOf is "1 album", "3 albums".
func nOf(n int, word string) string {
	return fmt.Sprintf("%d %s%s", n, word, plural(n))
}

// rollUp is an artist's (or a genre's) badge: NEW when any of its albums is
// new, else CHANGED when any is changed — the plan's "something of this is
// going over".
func rollUp(albums []Album, idx []int) TileState {
	st := TileOnDevice
	for _, i := range idx {
		if i >= len(albums) {
			continue
		}
		switch albums[i].State {
		case TileNew:
			return TileNew
		case TileChanged:
			st = TileChanged
		}
	}
	return st
}

// --- the Songs table's order and search -----------------------------------

// SongSort is the Songs table's sort column.
type SongSort int

// The sortable columns.
const (
	SortTitle SongSort = iota
	SortArtist
	SortAlbum
	SortTime
)

// SongOrder is the Songs table's rows, sorted by col (descending with desc)
// and narrowed by query: a case-folded match anywhere in the title, the
// artist or the album. Ties keep the album's own order (album, disc, track),
// so a column of one artist reads as their records do. It allocates the one
// slice it returns; the view calls it when the sort or the query changes,
// never per frame.
func SongOrder(albums []Album, rows []SongRow, col SongSort, desc bool, query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]int, 0, len(rows))
	for i, r := range rows {
		if q == "" || strings.Contains(r.fold, q) {
			out = append(out, i)
		}
	}
	length := func(i int) time.Duration { r := rows[i]; return albums[r.Album].Songs[r.Song].Length }
	less := func(a, b int) int {
		x, y := &rows[a], &rows[b]
		switch col {
		case SortArtist:
			return strings.Compare(x.artistKey, y.artistKey)
		case SortAlbum:
			return strings.Compare(x.album, y.album)
		case SortTime:
			switch la, lb := length(a), length(b); {
			case la < lb:
				return -1
			case la > lb:
				return 1
			}
			return 0
		}
		return strings.Compare(x.titleKey, y.titleKey)
	}
	sort.SliceStable(out, func(i, j int) bool {
		c := less(out[i], out[j])
		if c == 0 {
			return out[i] < out[j] // the catalog's own order: album, disc, track
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
	return out
}
