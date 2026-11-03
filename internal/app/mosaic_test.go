package app

import (
	"errors"
	"image"
	"image/color"
	"testing"
)

func solid(c color.NRGBA, w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fillRect(img, c)
	return img
}

var (
	mRed   = color.NRGBA{R: 0xE0, A: 0xFF}
	mGreen = color.NRGBA{G: 0xE0, A: 0xFF}
	mBlue  = color.NRGBA{B: 0xE0, A: 0xFF}
	mGold  = color.NRGBA{R: 0xE0, G: 0xC0, A: 0xFF}
	mGrey  = color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF}
)

// nearColour is a pixel within a few levels of c: the scaler blends at the edges,
// never in a quadrant's middle.
func nearColour(got color.Color, want color.NRGBA) bool {
	r, g, b, _ := got.RGBA()
	d := func(x uint32, y uint8) bool { v := int(x>>8) - int(y); return v > -6 && v < 6 }
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

// The mosaic's quadrants, at their centres on a 40 px render: one cover
// fills the square; two run corner to corner; three put the first again in
// the last corner; four (or more) are the first four in order.
func TestTheMosaicQuadrants(t *testing.T) {
	covers := []image.Image{solid(mRed, 50, 50), solid(mGreen, 30, 30), solid(mBlue, 64, 64),
		solid(mGold, 20, 20), solid(mGrey, 20, 20)}
	centres := [4]image.Point{{10, 10}, {30, 10}, {10, 30}, {30, 30}}
	for _, c := range []struct {
		n    int
		want [4]color.NRGBA
	}{
		{1, [4]color.NRGBA{mRed, mRed, mRed, mRed}},
		{2, [4]color.NRGBA{mRed, mGreen, mGreen, mRed}},
		{3, [4]color.NRGBA{mRed, mGreen, mBlue, mRed}},
		{4, [4]color.NRGBA{mRed, mGreen, mBlue, mGold}},
		{5, [4]color.NRGBA{mRed, mGreen, mBlue, mGold}},
	} {
		img := Mosaic(covers[:c.n], 40)
		if img == nil || img.Bounds().Dx() != 40 || img.Bounds().Dy() != 40 {
			t.Fatalf("%d covers: %v", c.n, img)
		}
		for q, p := range centres {
			if got := img.At(p.X, p.Y); !nearColour(got, c.want[q]) {
				t.Errorf("%d covers, quadrant %d at %v = %v, want %v", c.n, q, p, got, c.want[q])
			}
		}
	}
	if Mosaic(nil, 40) != nil {
		t.Error("no covers made a picture: the plain tile is drawn for none")
	}
}

// A cover that is not square is cropped to its centre, as a tile crops it:
// a 3:1 strip of red|green|blue fills its quadrant with green.
func TestTheMosaicCropsToTheCentre(t *testing.T) {
	strip := image.NewNRGBA(image.Rect(0, 0, 60, 20))
	for x := 0; x < 60; x++ {
		c := []color.NRGBA{mRed, mGreen, mBlue}[x/20]
		for y := 0; y < 20; y++ {
			strip.SetNRGBA(x, y, c)
		}
	}
	img := Mosaic([]image.Image{strip}, 40)
	for _, p := range []image.Point{{2, 20}, {20, 20}, {37, 20}} {
		if !nearColour(img.At(p.X, p.Y), mGreen) {
			t.Errorf("at %v: %v, want the strip's middle (green)", p, img.At(p.X, p.Y))
		}
	}
}

// The decode side: the covers are taken in the artist's album order, a
// folder with no cover is skipped, at most four are read, and a lone cover
// is read again at the whole square's size.
func TestMosaicFromTheAlbumFolders(t *testing.T) {
	colours := map[string]color.NRGBA{"/a": mRed, "/c": mBlue, "/d": mGreen, "/e": mGold, "/f": mGrey}
	var asked []string
	var sizes []int
	load := func(dir string, px int) (image.Image, error) {
		asked = append(asked, dir)
		sizes = append(sizes, px)
		c, ok := colours[dir]
		if !ok {
			return nil, ErrNoThumb
		}
		return solid(c, px, px), nil
	}
	img, err := mosaicFrom(load, []string{"/a", "/b", "/c", "/d", "/e", "/f"}, 64)
	if err != nil {
		t.Fatal(err)
	}
	// /b has no cover: the four are a, c, d, e — f is never opened.
	for q, want := range []color.NRGBA{mRed, mBlue, mGreen, mGold} {
		p := []image.Point{{16, 16}, {48, 16}, {16, 48}, {48, 48}}[q]
		if !nearColour(img.At(p.X, p.Y), want) {
			t.Errorf("quadrant %d = %v, want %v", q, img.At(p.X, p.Y), want)
		}
	}
	if len(asked) != 5 || asked[4] != "/e" {
		t.Errorf("folders read: %v", asked)
	}

	asked, sizes = nil, nil
	if _, err := mosaicFrom(load, []string{"/b", "/c"}, 64); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 3 || sizes[2] != coverPx(64) || sizes[1] >= sizes[2] {
		t.Errorf("a lone cover was not read again at full size: %v at %v", asked, sizes)
	}

	if _, err := mosaicFrom(load, []string{"/b", "/x"}, 64); !errors.Is(err, ErrNoThumb) {
		t.Errorf("no covers at all: %v, want ErrNoThumb (the plain tile)", err)
	}
}
