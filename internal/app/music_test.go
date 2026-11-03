package app

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/artfetch"
)

// The view is config.json's music_view: saved when it changes, read back by
// the next window, and Albums — the default — is no key at all.
func TestTheMusicViewIsRemembered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	u := NewUI(Options{Backend: &fakeBackend{}, ConfigPath: path})
	u.SetState(DemoState())
	u.Layout(newTestContext(900, 600))
	u.viewClicks[3].Click()
	u.Layout(newTestContext(900, 600))
	if u.st.View != ViewSongs {
		t.Fatalf("view = %v after clicking Songs", u.st.View)
	}
	if got := string(readKeys(t, path)["music_view"]); got != `"songs"` {
		t.Fatalf("music_view = %s, want \"songs\"", got)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	again := NewUI(Options{Backend: &fakeBackend{}, Config: cfg, ConfigPath: path})
	if again.st.View != ViewSongs {
		t.Fatalf("the next window opened on %v", again.st.View)
	}
	again.SetState(DemoState())
	again.st.View = ViewSongs
	again.savedView = ViewSongs
	again.Layout(newTestContext(900, 600))
	again.viewClicks[0].Click()
	again.Layout(newTestContext(900, 600))
	if _, ok := readKeys(t, path)["music_view"]; ok {
		t.Error("back on Albums, music_view is still in the file")
	}
}

// photoFake is a backend with a Deezer photo for every artist, counting the
// asks: the only door to the internet the artist tiles have.
type photoFake struct {
	fakeBackend
	pmu   sync.Mutex
	asked []string
}

func (f *photoFake) ArtistPhoto(ctx context.Context, name string, px int) (image.Image, error) {
	f.pmu.Lock()
	f.asked = append(f.asked, name)
	f.pmu.Unlock()
	return solid(mRed, px, px), nil
}

// No request for an artist's photo goes out unless both switches are on:
// "Look for covers on the internet" and "Artist pictures from Deezer". With
// either off the tiles are mosaics, made from the covers alone.
func TestArtistPhotosNeedBothSwitches(t *testing.T) {
	no := false
	for _, c := range []struct {
		online, artists bool
		ask             bool
	}{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		f := &photoFake{}
		cfg := Config{ArtistPictures: c.artists}
		if !c.online {
			cfg.OnlineArt = &no
		}
		u := NewUI(Options{Backend: f, Config: cfg})
		u.SetState(ArtistsState())
		u.Layout(newTestContext(900, 600))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := u.artistThumbs.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		f.pmu.Lock()
		n := len(f.asked)
		f.pmu.Unlock()
		if c.ask && n == 0 {
			t.Errorf("online %v, Deezer %v: no artist was asked for", c.online, c.artists)
		}
		if !c.ask && n != 0 {
			t.Errorf("online %v, Deezer %v: %d photo request(s) went out", c.online, c.artists, n)
		}
		if u.artistThumbs.Loads() == 0 {
			t.Errorf("online %v, Deezer %v: the tiles asked for no picture at all", c.online, c.artists)
		}
	}
}

// The real backend is the second lock on the same door: it asks Deezer only
// with both of its own switches on, whatever the window asks it.
func TestRealBackendAsksDeezerOnlyWithBothOn(t *testing.T) {
	var searches int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&searches, 1)
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()
	for _, c := range []struct{ noOnline, artists bool }{{true, false}, {false, false}, {true, true}} {
		b := &RealBackend{NoOnlineArt: c.noOnline, ArtistPictures: c.artists,
			ArtistPhotos: &artfetch.ArtistPhotos{Deezer: &artfetch.Deezer{BaseURL: srv.URL}, Dir: t.TempDir()}}
		if _, err := b.ArtistPhoto(context.Background(), "Sade", 100); err == nil {
			t.Errorf("%+v: a photo with a switch off", c)
		}
	}
	if n := atomic.LoadInt32(&searches); n != 0 {
		t.Fatalf("%d request(s) with a switch off", n)
	}
	b := &RealBackend{ArtistPhotos: &artfetch.ArtistPhotos{Deezer: &artfetch.Deezer{BaseURL: srv.URL}, Dir: t.TempDir()}}
	b.SetArtistPictures(true)
	_, _ = b.ArtistPhoto(context.Background(), "Sade", 100)
	if n := atomic.LoadInt32(&searches); n != 1 {
		t.Errorf("both on: %d request(s), want 1", n)
	}
}

// An artist tile opens the artist's sheet; one of its albums opens the album
// sheet over it, and closing that leaves the artist's.
func TestArtistTileOpensTheArtistThenTheAlbum(t *testing.T) {
	u, _ := sheetUI(t)
	u.st.View = ViewArtists
	u.Layout(newTestContext(900, 600))
	cat := u.catalog()
	idx := -1
	for i, a := range cat.Artists {
		if a.Name == "Talking Heads" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no Talking Heads in %q", artistNames(cat))
	}
	u.artistClicks[idx].Click()
	u.Layout(newTestContext(900, 600))
	if u.artist == nil || u.sheetArtist() == nil || u.sheetArtist().Name != "Talking Heads" {
		t.Fatalf("artist sheet = %+v", u.artist)
	}
	u.Layout(newTestContext(900, 600)) // the sheet's album tiles
	u.artist.albums[0].Click()
	u.Layout(newTestContext(900, 600))
	if u.sheet == nil || u.sheet.album.Device != demoSheetAlbum {
		t.Fatalf("album sheet = %+v", u.sheet)
	}
	u.sheet.closeBtn.Click()
	u.Layout(newTestContext(900, 600))
	if u.sheet != nil || u.artist == nil {
		t.Errorf("after closing the album: album %v, artist %v", u.sheet != nil, u.artist != nil)
	}
}

// A genre row narrows the grid to its albums, the switch reads its name and
// the tab row its counts; ‹ Genres goes back to the list.
func TestAGenreFiltersTheGrid(t *testing.T) {
	u, _ := sheetUI(t)
	u.st.View = ViewGenres
	u.Layout(newTestContext(900, 600))
	cat := u.catalog()
	hip := -1
	for i, g := range cat.Genres {
		if g.Name == "Hip-Hop" {
			hip = i
		}
	}
	if hip < 0 {
		t.Fatal("no Hip-Hop in the canned genres")
	}
	u.genreClicks[hip].Click()
	u.Layout(newTestContext(900, 600))
	if u.st.Genre != "Hip-Hop" || u.tabNote() != "2 artists · 2 albums" {
		t.Fatalf("genre %q, note %q", u.st.Genre, u.tabNote())
	}
	// The grid's tiles are those two albums: a click opens one of them.
	for _, i := range cat.Genres[hip].Albums {
		if u.st.Albums[i].Songs[0].Genre != "Hip-Hop" {
			t.Errorf("%s is in Hip-Hop", u.st.Albums[i].Title)
		}
	}
	u.tileClicks[cat.Genres[hip].Albums[0]].Click()
	u.Layout(newTestContext(900, 600))
	if u.sheet == nil || u.sheet.album.Device != u.st.Albums[cat.Genres[hip].Albums[0]].Device {
		t.Fatalf("the filtered grid's tile opened %+v", u.sheet)
	}
	u.closeSheet()
	u.genreBack.Click()
	u.Layout(newTestContext(900, 600))
	if u.st.Genre != "" || u.st.View != ViewGenres {
		t.Errorf("after ‹ Genres: genre %q view %v", u.st.Genre, u.st.View)
	}
}

// A song plays on this computer, as a track in the album sheet does: its
// album's songs are the tracklist, and the footer's Now Playing holds it.
// The search narrows the table as it is typed; the head sorts it.
func TestTheSongsTablePlaysSortsAndSearches(t *testing.T) {
	u, fa := sheetUI(t)
	st := SongsState()
	st.NowPlaying = nil
	for i := range st.Albums {
		for j := range st.Albums[i].Songs {
			st.Albums[i].Songs[j].Path = fmt.Sprintf("/m/%d/%02d.flac", i, j+1)
		}
	}
	u.SetState(st)
	u.Layout(newTestContext(900, 600))
	cat := u.catalog()
	row := -1
	for i, r := range cat.Songs {
		if u.st.Albums[r.Album].Songs[r.Song].Title == "Once in a Lifetime" {
			row = i
		}
	}
	u.songClicks[row].Click()
	u.Layout(newTestContext(900, 600))
	r := cat.Songs[row]
	if len(fa.played) != 1 || fa.played[0] != u.st.Albums[r.Album].Songs[r.Song].Path {
		t.Fatalf("played %q", fa.played)
	}
	np := u.st.NowPlaying
	if np == nil || np.Album.Device != demoSheetAlbum || np.TrackIndex != r.Song ||
		np.Album.TrackList[np.TrackIndex].Title != "Once in a Lifetime" {
		t.Fatalf("now playing = %+v", np)
	}

	u.songSearch.SetText("Talking")
	u.Layout(newTestContext(900, 600))
	if got := u.tabNote(); got != "8 of 268 songs" {
		t.Errorf("searching \"Talking\": note %q", got)
	}
	first := func() string {
		o := u.songOrder()
		r := cat.Songs[o[0]]
		return u.st.Albums[r.Album].Songs[r.Song].Title
	}
	if got := first(); got != "Born Under Punches (The Heat Goes On)" {
		t.Errorf("first by title = %q", got)
	}
	u.songHeads[SortTitle].Click()
	u.Layout(newTestContext(900, 600))
	if got := first(); got != "The Overload" || !u.songs.desc {
		t.Errorf("title again (descending): first = %q", got)
	}
	u.songHeads[SortTime].Click()
	u.Layout(newTestContext(900, 600))
	if got := first(); got != "Seen and Not Seen" || u.songs.desc {
		t.Errorf("by time: first = %q", got)
	}
	if !strings.Contains(u.tabNote(), "of 268") {
		t.Errorf("the search was lost: %q", u.tabNote())
	}
	// A frame that changes nothing sorts nothing: the rows are kept.
	kept := &u.songOrder()[0]
	u.Layout(newTestContext(900, 600))
	if &u.songOrder()[0] != kept {
		t.Error("an idle frame sorted the table again")
	}
}

// The Songs table at a big library's size costs what it costs at a small
// one's: only the visible rows are laid out, and the sort and search are
// kept between frames, not redone per frame.
func TestTheSongsTableDoesNotGrowPerFrame(t *testing.T) {
	cost := func(n int) uint64 {
		st := DemoState()
		st.View = ViewSongs
		st.Albums = nil
		for i := 0; i < n/10; i++ {
			a := Album{Device: fmt.Sprintf("Artist %d - Album %d", i%40, i), Title: fmt.Sprintf("Artist %d - Album %d", i%40, i),
				Dir: fmt.Sprintf("/m/%d", i), Tracks: 10, Artist: fmt.Sprintf("Artist %d", i%40)}
			for j := 0; j < 10; j++ {
				a.Songs = append(a.Songs, Song{Title: fmt.Sprintf("Song %d-%d", i, j), Artist: a.Artist,
					Genre: "Pop", Number: j + 1, Length: time.Duration(120+j) * time.Second})
			}
			st.Albums = append(st.Albums, a)
		}
		u := NewUI(Options{})
		u.SetState(st)
		u.Layout(newTestContext(900, 600))
		u.Layout(newTestContext(900, 600))
		return allocated(func() { u.Layout(newTestContext(900, 600)) })
	}
	small, big := cost(50), cost(1000)
	t.Logf("a frame of the Songs view: %s at 50 songs, %s at 1000", bytesHuman(small), bytesHuman(big))
	if big > small*3/2+64<<10 {
		t.Errorf("1000 songs cost %s a frame against %s for 50: the frame is doing per-song work",
			bytesHuman(big), bytesHuman(small))
	}
}

// End to end on real files: an artist with two albums is one tile, and its
// picture is the 2×2 of the two covers in title order — Blue then Red, so
// blue in the top-left and bottom-right corners, red in the other two.
func TestTheArtistTileIsAMosaicOfTheFolder(t *testing.T) {
	root := t.TempDir()
	shotAlbum(t, root, "Red", "Alpha", 2, 320, shotRed)
	shotAlbum(t, root, "Blue", "Alpha", 2, 320, shotBlue)
	lib, err := ReadShotLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	st := ArtistsState()
	UseShotLibrary(&st, lib)
	u := NewUI(Options{})
	u.coverLoad = AlbumPicture
	u.SetState(st)
	u.Layout(newTestContext(980, 700))
	if n := len(u.catalog().Artists); n != 1 {
		t.Fatalf("%d artists, want Alpha alone", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := u.artistThumbs.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	var img image.Image
	u.artistThumbs.mu.Lock()
	for _, it := range u.artistThumbs.items {
		if it.state == thumbReady {
			img = it.img
		}
	}
	u.artistThumbs.mu.Unlock()
	if img == nil {
		t.Fatal("the artist tile composed no picture")
	}
	b := img.Bounds()
	q := b.Dx() / 4
	for _, c := range []struct {
		at   image.Point
		want string
	}{{image.Pt(q, q), "blue"}, {image.Pt(3*q, q), "red"}, {image.Pt(q, 3*q), "red"}, {image.Pt(3*q, 3*q), "blue"}} {
		want := shotBlue
		if c.want == "red" {
			want = shotRed
		}
		if got := img.At(c.at.X, c.at.Y); !nearColour(got, want) {
			t.Errorf("at %v: %v, want %s", c.at, got, c.want)
		}
	}
}
