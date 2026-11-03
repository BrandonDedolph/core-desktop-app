package app

import (
	"context"
	"image"
	"image/draw"
	"strconv"
	"strings"
	"time"

	"gioui.org/op/clip"
	"gioui.org/op/paint"
	xdraw "golang.org/x/image/draw"
)

// An artist's picture.
//
// By default it is the mosaic: a 2×2 of the artist's album covers, in album
// title order — offline, deterministic, and it looks like the library. One
// cover alone when only one decodes; the plain tile (the colour and the
// initial, as an album with no cover gets) when none does. It is composed
// once, off the UI goroutine, into the artist picture cache, keyed by the
// artist and the size it is drawn at, with the albums' newest file time as
// the entry's date: a new scan that changed nothing composes nothing.
//
// With Settings' "Artist pictures from Deezer" ticked — and "Look for
// covers on the internet" on, both — the picture is the artist's Deezer
// photo when Deezer has one (artfetch.ArtistPhotos: disk-cached, one request
// per artist), and the mosaic when it does not.

// mosaicCells is which cover fills each quadrant — top left, top right,
// bottom left, bottom right — for two and three covers: two run corner to
// corner (A B / B A), three put the first again in the last corner (A B /
// C A). Four or more use the first four in order.
var mosaicCells = map[int][4]int{2: {0, 1, 1, 0}, 3: {0, 1, 2, 0}, 4: {0, 1, 2, 3}}

// Mosaic composes covers into one px×px picture: nil for none, the one
// cover filling the square for one, else the 2×2 of mosaicCells. Every
// cover is cropped to its centre square first, the way a tile crops one.
func Mosaic(covers []image.Image, px int) image.Image {
	if len(covers) == 0 || px <= 0 {
		return nil
	}
	dst := image.NewNRGBA(image.Rect(0, 0, px, px))
	if len(covers) == 1 {
		fill(dst, dst.Bounds(), covers[0])
		return dst
	}
	n := len(covers)
	if n > 4 {
		n = 4
	}
	h := px / 2
	cells := [4]image.Rectangle{
		image.Rect(0, 0, h, h), image.Rect(h, 0, px, h),
		image.Rect(0, h, h, px), image.Rect(h, h, px, px),
	}
	for q, c := range mosaicCells[n] {
		fill(dst, cells[q], covers[c])
	}
	return dst
}

// fill scales src's centre square into r of dst.
func fill(dst draw.Image, r image.Rectangle, src image.Image) {
	b := src.Bounds()
	s := b.Dx()
	if b.Dy() < s {
		s = b.Dy()
	}
	x, y := b.Min.X+(b.Dx()-s)/2, b.Min.Y+(b.Dy()-s)/2
	xdraw.CatmullRom.Scale(dst, r, src, image.Rect(x, y, x+s, y+s), xdraw.Src, nil)
}

// mosaicMost is how many covers a mosaic uses.
const mosaicMost = 4

// artistPicKey is the part of an artist's cache key that does not change
// with the size: the name and the album folders, in mosaic order.
func artistPicKey(ar Artist, albums []Album) string {
	var b strings.Builder
	b.WriteString(ar.Name)
	for _, i := range ar.Albums {
		b.WriteByte('\x00')
		b.WriteString(albums[i].Dir)
	}
	return b.String()
}

// artistPicMod is the artist picture's date: its albums' newest.
func artistPicMod(albums []Album, idx []int) time.Time {
	var t time.Time
	for _, i := range idx {
		if i < len(albums) && albums[i].Mod.After(t) {
			t = albums[i].Mod
		}
	}
	return t
}

// The two picture kinds an artist key asks for.
const (
	picMosaic = "m"
	picDeezer = "d"
)

// artistKeyFor is the cache key for one artist at one decode size.
func artistKeyFor(kind string, px int, pic string) string {
	return kind + "\x01" + strconv.Itoa(px) + "\x01" + pic
}

// splitArtistKey undoes artistKeyFor and artistPicKey.
func splitArtistKey(key string) (kind string, px int, name string, dirs []string) {
	parts := strings.SplitN(key, "\x01", 3)
	if len(parts) != 3 {
		return "", 0, "", nil
	}
	px, _ = strconv.Atoi(parts[1])
	f := strings.Split(parts[2], "\x00")
	return parts[0], px, f[0], f[1:]
}

// artistPhotoer is a Backend that can fetch an artist's photo (RealBackend:
// Deezer, through its disk cache). A Backend without it has only mosaics.
type artistPhotoer interface {
	ArtistPhoto(ctx context.Context, name string, px int) (image.Image, error)
}

// deezerOn is whether an artist picture may come from the internet: both
// Settings switches, "Look for covers on the internet" and "Artist pictures
// from Deezer".
func (u *UI) deezerOn() bool { return u.cfg.OnlineArtOn() && u.cfg.ArtistPictures }

// loadArtistPicture is the artist cache's decode, on a worker goroutine.
func (u *UI) loadArtistPicture(key string) (image.Image, error) {
	kind, px, name, dirs := splitArtistKey(key)
	if kind == picDeezer {
		if ph, ok := u.be.(artistPhotoer); ok {
			if img, err := ph.ArtistPhoto(context.Background(), name, px); err == nil && img != nil {
				return img, nil
			}
		}
	}
	return mosaicFrom(u.coverLoad, dirs, px)
}

// mosaicFrom decodes up to mosaicMost of dirs' covers, at the quadrant's
// size, and composes them. A single cover is decoded again at the whole
// square's size: a quarter-size picture blown up is the blurry tile.
func mosaicFrom(load func(string, int) (image.Image, error), dirs []string, px int) (image.Image, error) {
	if load == nil {
		return nil, ErrNoThumb
	}
	var covers []image.Image
	var first string
	for _, d := range dirs {
		if len(covers) == mosaicMost {
			break
		}
		if img, err := load(d, coverPx(px/2)); err == nil && img != nil {
			if first == "" {
				first = d
			}
			covers = append(covers, img)
		}
	}
	switch len(covers) {
	case 0:
		return nil, ErrNoThumb
	case 1:
		if img, err := load(first, coverPx(px)); err == nil && img != nil {
			covers[0] = img
		}
	}
	return Mosaic(covers, px), nil
}

// artistPicture is one artist's picture for a box of box device pixels, from
// the artist cache: (op, true) once composed, and a load started otherwise.
func (u *UI) artistPicture(ar *Artist, box int) (paint.ImageOp, bool) {
	kind := picMosaic
	if u.deezerOn() {
		kind = picDeezer
	}
	return u.artistThumbs.get(artistKeyFor(kind, coverPx(box), ar.pic), artistPicMod(u.st.Albums, ar.Albums))
}

// artistArt draws an artist's square: the picture, or the plain tile with
// the artist's initial, then the rolled-up NEW / CHANGED badge.
func (u *UI) artistArt(gtx C, ar *Artist, sz image.Point, radius int, badge bool) D {
	defer clip.UniformRRect(image.Rectangle{Max: sz}, radius).Push(gtx.Ops).Pop()
	if img, ok := u.artistPicture(ar, sz.X); !ok || !u.coverFill(gtx, img, sz) {
		u.plainTileFor(gtx, ar.Name, ar.Name, sz)
	}
	if badge {
		u.badgeState(gtx, rollUp(u.st.Albums, ar.Albums), sz)
	}
	return D{Size: sz}
}
