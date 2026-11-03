package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/flac"
	"github.com/BrandonDedolph/core-desktop-app/internal/library"
	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// album is one canned album by artist with songs of the given lead artists
// ("" is the album's own artist).
func catAlbum(artist, title string, st TileState, songArtists ...string) Album {
	a := Album{Device: artist + " - " + title, Title: artist + " - " + title, Dir: "/m/" + title + " - " + artist,
		Artist: artist, State: st, Tracks: len(songArtists)}
	for i, sa := range songArtists {
		if sa == "" {
			sa = artist
		}
		a.Songs = append(a.Songs, Song{Title: fmt.Sprintf("%s %d", title, i+1), Artist: sa, Number: i + 1,
			Disc: 1, Length: time.Duration(100+i) * time.Second})
	}
	return a
}

func artistNames(c *Catalog) []string {
	var out []string
	for _, a := range c.Artists {
		out = append(out, a.Name)
	}
	return out
}

// The iPod's artist order: a leading "The " is not part of the sort, case is
// not either, and "The Kid LAROI" and "Kid LAROI" — like "blackbear" and
// "Blackbear" — are one artist.
func TestArtistsSortAsTheIPodSorts(t *testing.T) {
	albums := []Album{
		catAlbum("The Blue Nile", "Hats", TileOnDevice, ""),
		catAlbum("Burial", "Untrue", TileOnDevice, ""),
		catAlbum("blackbear", "deadroses", TileOnDevice, ""),
		catAlbum("Blackbear", "digital druglord", TileOnDevice, ""),
		catAlbum("The Kid LAROI", "F*CK LOVE", TileOnDevice, ""),
		catAlbum("Kid LAROI", "Thousand Miles", TileOnDevice, ""),
		catAlbum("Beach House", "Teen Dream", TileOnDevice, ""),
		catAlbum("Theo Katzman", "Modern Johnny Sings", TileOnDevice, ""),
	}
	c := BuildCatalog(albums)
	want := []string{"Beach House", "blackbear", "The Blue Nile", "Burial", "The Kid LAROI", "Theo Katzman"}
	if got := artistNames(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("artists = %q\n          want %q", got, want)
	}
	if bb := c.Artists[1]; len(bb.Albums) != 2 || bb.Facts != "2 albums · 2 songs" {
		t.Errorf("blackbear = %+v, want both spellings' albums", bb)
	}
	// The mosaic's order is the album titles', not the folder order.
	if kid := c.Artists[4]; len(kid.Albums) != 2 || tileTitle(albums[kid.Albums[0]]) != "F*CK LOVE" {
		t.Errorf("The Kid LAROI's albums = %v", kid.Albums)
	}
}

// A split album is under every artist that leads a track of it; a guest
// ("feat.", a second name in a multi-value tag) is not an artist with an
// album. The album's own artist counts all its songs, the other its own.
func TestAMultiArtistAlbumIsUnderEachArtist(t *testing.T) {
	split := catAlbum("Alpha", "Split", TileNew, "", "Alpha feat. Gamma", "Beta", "Beta; Delta", "Beta (with Epsilon)")
	other := catAlbum("Beta", "Solo", TileOnDevice, "")
	c := BuildCatalog([]Album{split, other})
	if got, want := artistNames(c), []string{"Alpha", "Beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("artists = %q, want %q (no tile for a guest)", got, want)
	}
	alpha, beta := c.Artists[0], c.Artists[1]
	if !reflect.DeepEqual(alpha.Albums, []int{0}) || alpha.Songs != 5 {
		t.Errorf("Alpha = %+v, want the split album and all 5 of its songs", alpha)
	}
	if !reflect.DeepEqual(beta.Albums, []int{1, 0}) || beta.Songs != 4 || beta.Facts != "2 albums · 4 songs" {
		t.Errorf("Beta = %+v, want Solo then Split (by title), 1 + 3 songs", beta)
	}
	// The roll-up: Beta's split album is NEW, so Beta is.
	if rollUp([]Album{split, other}, beta.Albums) != TileNew {
		t.Error("an artist with a new album is not NEW")
	}
	changed := split
	changed.State = TileChanged
	if rollUp([]Album{changed, other}, beta.Albums) != TileChanged {
		t.Error("an artist with a changed album is not CHANGED")
	}
	if rollUp([]Album{other, other}, []int{0, 1}) != TileOnDevice {
		t.Error("an artist wholly on the iPod has a badge")
	}
}

func TestLeadArtist(t *testing.T) {
	for in, want := range map[string]string{
		"Kanye West feat. Jay-Z":    "Kanye West",
		"blackbear; G-Eazy":         "blackbear",
		"Simon & Garfunkel":         "Simon & Garfunkel",
		"Tyler, The Creator":        "Tyler, The Creator",
		"Lil Nas X":                 "Lil Nas X",
		"Daniel Caesar (feat. H)":   "Daniel Caesar",
		"Post Malone FT. 21 Savage": "Post Malone",
		"Feat. Nobody":              "Feat. Nobody",
	} {
		if got := leadArtist(in); got != want {
			t.Errorf("leadArtist(%q) = %q, want %q", in, got, want)
		}
	}
}

// writeFLACAlbum writes one "Album - Artist" folder of FLACs tagged with
// genre, and returns the folder.
func writeFLACAlbum(t *testing.T, root, album, artist, genre string, n int) string {
	t.Helper()
	dir := filepath.Join(root, album+" - "+artist)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		data := flac.BuildFile(
			flac.StreamInfo{SampleRate: 44100, Channels: 2, BitsPerSample: 16, TotalSamples: uint64(44100 * (60 + i))},
			map[string]string{"artist": artist, "album": album, "title": fmt.Sprintf("%s %d", album, i),
				"tracknumber": fmt.Sprint(i), "genre": genre}, nil)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d - %s - %s %d.flac", i, artist, album, i)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The views group by what the iPod will show: the tag's genre through the
// genre map a sync uses, read from the plan's own scan — and each song is
// its file's side of that plan.
func TestTheGenresAreThePlansThroughTheGenreMap(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeFLACAlbum(t, src, "Untrue", "Burial", "Electronic", 2)
	writeFLACAlbum(t, src, "24", "Arizona Zervas", "Rap, Trap", 3)           // mapped below
	writeFLACAlbum(t, src, "Sometimes", "Little Simz", "UK Hip Hop, Rap", 2) // unmapped: first of the list
	mapFile := filepath.Join(t.TempDir(), "genres.json")
	if err := os.WriteFile(mapFile, []byte(`{"genres": {"Arizona Zervas": "Hip-Hop"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	o := syncer.Options{Src: src, Dst: dst, DryRun: true, NoArt: true, GenreMap: mapFile}
	scan, err := syncer.ScanSource(o)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := syncer.MakePlan(o, scan)
	if err != nil {
		t.Fatal(err)
	}
	albums := AlbumsFromPlan(plan)
	c := BuildCatalog(albums)
	var got []string
	for _, g := range c.Genres {
		got = append(got, g.Name+": "+g.Facts)
	}
	want := []string{"Electronic: 1 artist · 1 album", "Hip-Hop: 1 artist · 1 album", "UK Hip Hop: 1 artist · 1 album"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("genres = %q\n      want %q", got, want)
	}
	if len(c.Songs) != 7 {
		t.Fatalf("%d songs, want 7", len(c.Songs))
	}
	for _, a := range albums {
		if a.Artist == "" || len(a.Songs) != a.Tracks {
			t.Errorf("%s: artist %q, %d songs of %d", a.Title, a.Artist, len(a.Songs), a.Tracks)
		}
		for _, s := range a.Songs {
			// The iPod has nothing yet: every file is a copy of a new one.
			if s.State != TileNew || s.Size == 0 || s.Length <= time.Minute || s.Path == "" {
				t.Errorf("%s / %s: %+v", a.Title, s.Title, s)
			}
		}
	}
}

// A song's state is its own file's in the plan, not its album's.
func TestASongsStateIsItsFiles(t *testing.T) {
	plan := &syncer.Plan{
		Copy: []syncer.FileOp{
			{Src: "/m/A/01.flac", Album: "X - A", Why: syncer.WhyNew, Size: 10},
			{Src: "/m/A/02.flac", Album: "X - A", Why: syncer.WhyTime, Size: 20},
		},
		Skip: []syncer.FileOp{{Src: "/m/A/03.flac", Album: "X - A", Why: syncer.WhySame, Size: 30}},
		Scan: &library.Scan{Albums: []library.Album{{SrcDir: "/m/A", FolderArtist: "X", DeviceFolder: "X - A",
			Tracks: []library.Track{
				{SrcPath: "/m/A/01.flac", Title: "One", Artist: "X", TagArtist: "X feat. Y", Track: 1, Disc: 1},
				{SrcPath: "/m/A/02.flac", Title: "Two", Artist: "X", Track: 2, Disc: 1},
				{SrcPath: "/m/A/03.flac", Title: "Three", Artist: "X", Track: 3, Disc: 1, Genre: "Rock"},
			}}}},
	}
	albums := AlbumsFromPlan(plan)
	if len(albums) != 1 || len(albums[0].Songs) != 3 {
		t.Fatalf("albums = %+v", albums)
	}
	s := albums[0].Songs
	if s[0].State != TileNew || s[1].State != TileChanged || s[2].State != TileOnDevice {
		t.Errorf("states = %v %v %v, want NEW, CHANGED, on the iPod", s[0].State, s[1].State, s[2].State)
	}
	if s[0].Artist != "X feat. Y" || s[1].Artist != "X" || s[2].Size != 30 {
		t.Errorf("songs = %+v", s)
	}
	// A sync that went through puts every song on the iPod.
	markSynced(albums)
	for _, x := range albums[0].Songs {
		if x.State != TileOnDevice {
			t.Errorf("after the sync %s is %v", x.Title, x.State)
		}
	}
}

func songTitles(albums []Album, rows []SongRow, order []int) []string {
	var out []string
	for _, i := range order {
		r := rows[i]
		out = append(out, albums[r.Album].Songs[r.Song].Title)
	}
	return out
}

// The table sorts by the column clicked, either way, ties in the album's own
// order; the search narrows by title, artist or album, ignoring case.
func TestTheSongsTableSortsAndSearches(t *testing.T) {
	mk := func(title, artist string, secs int) Song {
		return Song{Title: title, Artist: artist, Length: time.Duration(secs) * time.Second}
	}
	albums := []Album{
		{Device: "Talking Heads - Remain in Light", Title: "Talking Heads - Remain in Light", Songs: []Song{
			mk("Once in a Lifetime", "Talking Heads", 259), mk("The Great Curve", "Talking Heads", 386)}},
		{Device: "Burial - Untrue", Title: "Burial - Untrue", Songs: []Song{
			mk("Archangel", "Burial", 238), mk("Etched Headplate", "Burial", 359)}},
		{Device: "Beach House - Teen Dream", Title: "Beach House - Teen Dream", Songs: []Song{
			mk("Zebra", "Beach House", 286), mk("archangel", "Beach House", 238)}},
	}
	rows := BuildCatalog(albums).Songs
	cases := []struct {
		col   SongSort
		desc  bool
		query string
		want  []string
	}{
		{SortTitle, false, "", []string{"Archangel", "archangel", "Etched Headplate", "Once in a Lifetime", "The Great Curve", "Zebra"}},
		{SortTitle, true, "", []string{"Zebra", "The Great Curve", "Once in a Lifetime", "Etched Headplate", "Archangel", "archangel"}},
		// Artist: Beach House, Burial, Talking Heads; within one, the album's order.
		{SortArtist, false, "", []string{"Zebra", "archangel", "Archangel", "Etched Headplate", "Once in a Lifetime", "The Great Curve"}},
		{SortAlbum, false, "", []string{"Once in a Lifetime", "The Great Curve", "Zebra", "archangel", "Archangel", "Etched Headplate"}},
		{SortTime, false, "", []string{"Archangel", "archangel", "Once in a Lifetime", "Zebra", "Etched Headplate", "The Great Curve"}},
		{SortTitle, false, "ARCH", []string{"Archangel", "archangel"}},
		{SortTitle, false, "burial", []string{"Archangel", "Etched Headplate"}},
		{SortTitle, false, "teen", []string{"archangel", "Zebra"}},
		{SortTitle, false, "nothing like it", nil},
	}
	for _, c := range cases {
		got := songTitles(albums, rows, SongOrder(albums, rows, c.col, c.desc, c.query))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("sort %d desc %v %q:\n got  %q\n want %q", c.col, c.desc, c.query, got, c.want)
		}
	}
}

func TestMusicViewKeys(t *testing.T) {
	for _, v := range musicViews {
		if viewFromKey(viewKey(v)) != v || viewFromKey(strings.ToUpper(viewKey(v))) != v {
			t.Errorf("%v does not round-trip through %q", v, viewKey(v))
		}
	}
	if viewFromKey("") != ViewAlbums || viewFromKey("playlists") != ViewAlbums {
		t.Error("an unknown music_view is not Albums")
	}
}
