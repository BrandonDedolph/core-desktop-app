package artfetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// deezerServer answers /search/artist from results (keyed by the q
// parameter) and serves one 600 px JPEG under /images/artist/<hash>/...; it
// counts both kinds of request.
func deezerServer(t *testing.T, results map[string]string) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var searches, images int32
	pic := coverJPEG(t, 600)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/artist":
			atomic.AddInt32(&searches, 1)
			w.Header().Set("Content-Type", "application/json")
			body, ok := results[r.URL.Query().Get("q")]
			if !ok {
				fmt.Fprint(w, `{"data":[],"total":0}`)
				return
			}
			fmt.Fprintf(w, body, srv.URL)
		default:
			atomic.AddInt32(&images, 1)
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(pic)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &searches, &images
}

const (
	// A hit: the second result is the artist, the first is a namesake.
	deezerHit = `{"data":[
		{"name":"Sade Adu Tribute","picture_xl":"%[1]s/images/artist/aaaa/1000x1000.jpg"},
		{"name":"Sade","picture_xl":"%[1]s/images/artist/5f1e/1000x1000-000000-80-0-0.jpg",
		 "picture_big":"%[1]s/images/artist/5f1e/500x500-000000-80-0-0.jpg"}]}`
	// The artist is there with Deezer's silhouette: an empty hash.
	deezerBlank = `{"data":[{"name":"Arthur Russell",
		"picture_xl":"%[1]s/images/artist//1000x1000-000000-80-0-0.jpg",
		"picture_big":"%[1]s/images/artist//500x500-000000-80-0-0.jpg"}]}`
	// picture_xl missing, picture_big there.
	deezerBigOnly = `{"data":[{"name":"The Blue Nile","picture_xl":"",
		"picture_big":"%[1]s/images/artist/77aa/500x500-000000-80-0-0.jpg"}]}`
)

func TestDeezerArtistPicture(t *testing.T) {
	srv, _, _ := deezerServer(t, map[string]string{
		"Sade": deezerHit, "Arthur Russell": deezerBlank, "Blue Nile": deezerBigOnly,
	})
	d := &Deezer{BaseURL: srv.URL}
	ctx := context.Background()

	got, err := d.ArtistPicture(ctx, "Sade")
	if err != nil || got != srv.URL+"/images/artist/5f1e/1000x1000-000000-80-0-0.jpg" {
		t.Errorf("hit: %q, %v — want the named artist's picture_xl, not the first result's", got, err)
	}
	if _, err := d.ArtistPicture(ctx, "Nobody At All"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("miss: %v, want ErrNoMatch", err)
	}
	if got, err := d.ArtistPicture(ctx, "Arthur Russell"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("empty hash: %q, %v — the silhouette is no picture", got, err)
	}
	// "The " is the iPod's rule both ways: "Blue Nile" finds "The Blue Nile".
	if got, err := d.ArtistPicture(ctx, "Blue Nile"); err != nil || got != srv.URL+"/images/artist/77aa/500x500-000000-80-0-0.jpg" {
		t.Errorf("picture_big fallback: %q, %v", got, err)
	}
}

// The store asks Deezer once per artist: a hit is kept on disk and a miss is
// remembered, so a second Photo for either is no request at all — and a new
// store over the same folder (the next start) reads the disk too.
func TestArtistPhotosAskOncePerArtist(t *testing.T) {
	srv, searches, images := deezerServer(t, map[string]string{"Sade": deezerHit, "Arthur Russell": deezerBlank})
	dir := t.TempDir()
	p := &ArtistPhotos{Deezer: &Deezer{BaseURL: srv.URL}, Dir: dir}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		b, err := p.Photo(ctx, "Sade")
		if err != nil || len(b) == 0 {
			t.Fatalf("Photo(Sade) #%d: %d bytes, %v", i, len(b), err)
		}
		if _, err := p.Photo(ctx, "Arthur Russell"); !errors.Is(err, ErrNoMatch) {
			t.Fatalf("Photo(Arthur Russell) #%d: %v", i, err)
		}
	}
	if s, im := atomic.LoadInt32(searches), atomic.LoadInt32(images); s != 2 || im != 1 {
		t.Fatalf("three rounds made %d searches and %d image fetches, want 2 and 1", s, im)
	}
	ents, _ := os.ReadDir(dir)
	jpgs := 0
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".jpg" {
			jpgs++
		}
	}
	if jpgs != 1 {
		t.Errorf("the cache holds %d photos, want 1: %v", jpgs, ents)
	}

	again := &ArtistPhotos{Deezer: &Deezer{BaseURL: srv.URL}, Dir: dir}
	if _, err := again.Photo(ctx, "sade"); err != nil {
		t.Fatalf("next start: %v", err)
	}
	if _, err := again.Photo(ctx, "Arthur Russell"); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("next start, the miss: %v", err)
	}
	if s := atomic.LoadInt32(searches); s != 2 {
		t.Errorf("the next start searched again (%d searches): the disk is the cache", s)
	}
}

// A failure that leaves nothing on disk (Deezer down) is not retried by the
// same process: a grid redrawn at a new size asks again for every tile.
func TestArtistPhotosDoNotHammerAFailingService(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()
	p := &ArtistPhotos{Deezer: &Deezer{BaseURL: srv.URL}, Dir: t.TempDir()}
	for i := 0; i < 4; i++ {
		if _, err := p.Photo(context.Background(), "Sade"); err == nil {
			t.Fatal("a 403 came back as a photo")
		}
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("%d requests for one artist, want 1", got)
	}
}
