package artfetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Artist photos, from Deezer — the one source with a picture per artist and
// no key. It is opt-in (Settings → "Artist pictures from Deezer", off by
// default, and only with "Look for covers on the internet" on too): Deezer's
// terms limit the API to private, non-commercial use and keep the photos
// Deezer's. So a photo is written to exactly one place, the user's own cache
// (<UserCacheDir>/core/artists), never into the music folder and never onto
// the iPod.
//
//	GET https://api.deezer.com/search/artist?q=<name>
//	→ {"data": [{"name": …, "picture_xl": 1000×1000, "picture_big": 500×500}, …]}
//
// A result whose picture URL has an empty hash (".../images/artist//...") is
// Deezer's grey silhouette: no photo, which is a miss, not a picture.

// DeezerBaseURL is the API root, a variable so a test can point every
// client at an httptest server.
var DeezerBaseURL = "https://api.deezer.com"

// Deezer is the artist search.
type Deezer struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
}

func (d *Deezer) base() string {
	if d.BaseURL != "" {
		return strings.TrimSuffix(d.BaseURL, "/")
	}
	return strings.TrimSuffix(DeezerBaseURL, "/")
}

func (d *Deezer) client() *Client {
	return &Client{HTTP: d.HTTP, UserAgent: d.UserAgent}
}

// deezerArtist is the part of one search result this reads.
type deezerArtist struct {
	Name       string `json:"name"`
	PictureXL  string `json:"picture_xl"`
	PictureBig string `json:"picture_big"`
}

// ArtistPicture is the URL of name's photo: picture_xl (1000 px), else
// picture_big (500 px), of the first result whose name is name's (case,
// punctuation and a leading "The " aside). ErrNoMatch when Deezer has no
// such artist or only the silhouette for them: a search's first hit for an
// obscure name is often somebody else, and somebody else's face on the tile
// is the one mistake this must not make.
func (d *Deezer) ArtistPicture(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrNoMatch
	}
	v := url.Values{}
	v.Set("q", name)
	body, err := d.client().do(ctx, d.base()+"/search/artist?"+v.Encode(), "application/json", 2<<20)
	if err != nil {
		return "", err
	}
	var res struct {
		Data  []deezerArtist `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("deezer: %w", err)
	}
	if res.Error != nil {
		// Deezer answers its own errors (a quota, say) with a 200.
		return "", fmt.Errorf("deezer: %s", res.Error.Message)
	}
	want := artistKey(name)
	for _, a := range res.Data {
		if artistKey(a.Name) != want {
			continue
		}
		for _, u := range []string{a.PictureXL, a.PictureBig} {
			if hasPicture(u) {
				return u, nil
			}
		}
		return "", ErrNoMatch
	}
	return "", ErrNoMatch
}

// hasPicture is false for an empty URL and for Deezer's placeholder, whose
// path has no hash: ".../images/artist//1000x1000-000000-80-0-0.jpg".
func hasPicture(u string) bool {
	return strings.TrimSpace(u) != "" && !strings.Contains(u, "/artist//")
}

// artistKey is how two spellings of one artist compare: NormKey, which
// folds case, punctuation and a leading "The " (the iPod's own artist rule).
func artistKey(s string) string { return NormKey(s) }

// ArtistPhotos is the photo store: the disk cache and, behind it, at most
// one Deezer request per artist for as long as the program runs. A photo on
// disk is kept until the cache folder is deleted; "Deezer has none" is kept
// for NegativeTTL, like an album with no cover.
type ArtistPhotos struct {
	Deezer *Deezer
	// Dir is <UserCacheDir>/core/artists; empty keeps nothing on disk.
	Dir         string
	NegativeTTL time.Duration
	Now         func() time.Time

	mu sync.Mutex
	// asked is every artist this process has sent a request for. A
	// failure that left nothing on disk (no network) is not asked again
	// until the next start: a grid redrawn at a new size asks for every
	// tile again, and that must not be a request per tile per resize.
	asked map[string]bool
}

// DefaultArtistPhotos is the shared store, beside the cover cache.
func DefaultArtistPhotos() (*ArtistPhotos, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("artfetch: locating the user cache directory: %w", err)
	}
	return &ArtistPhotos{Deezer: &Deezer{}, Dir: filepath.Join(dir, "core", "artists")}, nil
}

func (p *ArtistPhotos) cache() *Cache {
	return &Cache{Dir: p.Dir, NegativeTTL: p.NegativeTTL, Now: p.Now}
}

// Photo is name's photo, as JPEG or PNG bytes: from the disk if it is
// there, else from Deezer (once). ErrNoMatch when there is none.
func (p *ArtistPhotos) Photo(ctx context.Context, name string) ([]byte, error) {
	k := artistKey(name)
	if k == "" {
		return nil, ErrNoMatch
	}
	c := p.cache()
	file := key("deezer-artist", k)
	if c.ok() {
		for _, ext := range []string{".jpg", ".png"} {
			if b, err := os.ReadFile(filepath.Join(p.Dir, file+ext)); err == nil && len(b) > 0 {
				return b, nil
			}
		}
		if c.Missed(Query{Artist: k, Album: "\x00artist"}) {
			return nil, ErrNoMatch
		}
	}
	p.mu.Lock()
	if p.asked[k] {
		p.mu.Unlock()
		return nil, ErrNoMatch
	}
	if p.asked == nil {
		p.asked = map[string]bool{}
	}
	p.asked[k] = true
	p.mu.Unlock()

	d := p.Deezer
	if d == nil {
		d = &Deezer{}
	}
	u, err := d.ArtistPicture(ctx, name)
	if errors.Is(err, ErrNoMatch) {
		_ = c.PutMiss(Query{Artist: k, Album: "\x00artist"})
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	cl := d.client()
	cl.MinPixels = 200
	body, err := cl.do(ctx, u, "image/*", MaxImageBytes)
	if err != nil {
		return nil, err
	}
	mime, err := cl.validate(body)
	if err != nil {
		return nil, err
	}
	ext := ".jpg"
	if mime == "image/png" {
		ext = ".png"
	}
	if c.ok() {
		_ = c.write(file+ext, body)
	}
	return body, nil
}
