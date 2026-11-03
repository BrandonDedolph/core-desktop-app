package app

import (
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// The artist sheet: what a click on an artist tile opens. The album sheet's
// chrome — the picture, the name, the facts — and the artist's albums as
// small tiles, each opening the album sheet over this one. Esc, Close or a
// click outside closes it.

const (
	artistHint  = "Click an album to open it."
	artistClose = "Close"
)

// artistSheet is the open sheet. It holds the artist's key, not the Artist:
// a new plan rebuilds the catalog under it, and the sheet follows the artist
// into the new one (or closes, when the artist is gone).
type artistSheet struct {
	key                    string
	list                   widget.List
	closeBtn, scrim, block widget.Clickable
	albums                 []widget.Clickable
}

// openArtist opens the sheet on one artist.
func (u *UI) openArtist(name string) {
	s := &artistSheet{key: artistSortKey(name)}
	s.list.Axis = layout.Vertical
	u.artist = s
}

// closeArtist closes it.
func (u *UI) closeArtist() { u.artist = nil }

// sheetArtist is the open sheet's artist in the current catalog, or nil.
func (u *UI) sheetArtist() *Artist {
	if u.artist == nil {
		return nil
	}
	cat := u.catalog()
	for i := range cat.Artists {
		if artistSortKey(cat.Artists[i].Name) == u.artist.key {
			return &cat.Artists[i]
		}
	}
	return nil
}

// artistEvents is the sheet's half of the frame.
func (u *UI) artistEvents(gtx C) {
	s := u.artist
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
			u.closeArtist()
			return
		}
	}
	ar := u.sheetArtist()
	if ar == nil || s.closeBtn.Clicked(gtx) || s.scrim.Clicked(gtx) {
		u.closeArtist()
		return
	}
	s.block.Clicked(gtx)
	for i := range s.albums {
		if i < len(ar.Albums) && s.albums[i].Clicked(gtx) {
			u.openSheet(u.st.Albums[ar.Albums[i]])
		}
	}
}

// artistStatus is the sheet's sentence about the iPod.
func artistStatus(albums []Album, idx []int) string {
	var n, c int
	for _, i := range idx {
		switch albums[i].State {
		case TileNew:
			n++
		case TileChanged:
			c++
		}
	}
	var parts []string
	if n > 0 {
		parts = append(parts, nOf(n, "new album"))
	}
	if c > 0 {
		parts = append(parts, nOf(c, "changed album"))
	}
	if len(parts) == 0 {
		return "On the iPod"
	}
	return strings.Join(parts, " and ") + " to copy on the next sync"
}

// Artist sheet geometry: the small tiles.
const (
	artistTile = 92
	artistGap  = 12
)

// artistLayer is the scrim and the sheet.
func (u *UI) artistLayer(gtx C) D {
	s := u.artist
	return u.panelLayer(gtx, &s.scrim, &s.block, u.artistBody)
}

// artistBody is the head, the albums (scrolling) and the foot, as the album
// sheet lays its own out.
func (u *UI) artistBody(gtx C) D {
	s := u.artist
	ar := u.sheetArtist()
	if ar == nil {
		return D{}
	}
	for len(s.albums) < len(ar.Albums) {
		s.albums = append(s.albums, widget.Clickable{})
	}
	in := gtx.Dp(18)
	gap := gtx.Dp(14)
	innerW := gtx.Constraints.Max.X - 2*in
	g := gtx
	g.Constraints = layout.Constraints{Min: image.Pt(innerW, 0), Max: image.Pt(innerW, gtx.Constraints.Max.Y)}

	hm := op.Record(gtx.Ops)
	head := u.artistHead(g, ar)
	headC := hm.Stop()
	fm := op.Record(gtx.Ops)
	foot := u.artistFoot(g)
	footC := fm.Stop()

	tile, tgap := gtx.Dp(artistTile), gtx.Dp(artistGap)
	cols := (innerW + tgap) / (tile + tgap)
	if cols < 1 {
		cols = 1
	}
	rows := (len(ar.Albums) + cols - 1) / cols
	rowH := tile + gtx.Dp(32)
	want := rows * rowH
	room := gtx.Constraints.Max.Y - 2*in - head.Size.Y - foot.Size.Y - 2*gap
	if room < rowH {
		room = rowH
	}
	listH := want
	if listH > room {
		listH = room
	}

	y := in
	place := func(c op.CallOp, h int) {
		st := op.Offset(image.Pt(in, y)).Push(gtx.Ops)
		c.Add(gtx.Ops)
		st.Pop()
		y += h
	}
	place(headC, head.Size.Y)
	y += gap
	lg := gtx
	lg.Constraints = layout.Exact(image.Pt(innerW, listH))
	lm := op.Record(gtx.Ops)
	l := material.List(u.th, &s.list)
	l.Indicator.Color = u.pal.SelSub
	l.Layout(lg, rows, func(gtx C, row int) D {
		kids := make([]layout.FlexChild, 0, cols*2)
		for c := 0; c < cols; c++ {
			i := row*cols + c
			if c > 0 {
				kids = append(kids, layout.Rigid(layout.Spacer{Width: unit.Dp(artistGap)}.Layout))
			}
			if i >= len(ar.Albums) {
				break
			}
			kids = append(kids, layout.Rigid(func(gtx C) D {
				return u.artistAlbum(gtx, ar.Albums[i], &s.albums[i], tile)
			}))
		}
		return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx, kids...)
		})
	})
	place(lm.Stop(), listH)
	y += gap
	place(footC, foot.Size.Y)
	return D{Size: image.Pt(gtx.Constraints.Max.X, y+in)}
}

// artistHead is the picture and the column beside it.
func (u *UI) artistHead(gtx C, ar *Artist) D {
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			sz := image.Pt(gtx.Dp(sheetCover), gtx.Dp(sheetCover))
			gtx.Constraints = layout.Exact(sz)
			return u.artistArt(gtx, ar, sz, gtx.Dp(6), false)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					l := material.Label(u.th, unit.Sp(18), ar.Name)
					l.Font.Weight = font.Bold
					l.Color = u.pal.Ink
					l.MaxLines = 2
					return l.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(u.text(11.5, u.pal.Muted2, ar.Facts)),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
				layout.Rigid(u.wrapN(12.5, u.pal.Ink, artistStatus(u.st.Albums, ar.Albums), 2)),
			)
		}),
	)
}

// artistAlbum is one small tile: the cover with its badge, and the title.
func (u *UI) artistAlbum(gtx C, i int, click *widget.Clickable, size int) D {
	a := u.st.Albums[i]
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = size, size
	return click.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				gtx.Constraints = layout.Exact(image.Pt(size, size))
				return u.tileArt(gtx, a)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx C) D {
				l := material.Label(u.th, unit.Sp(11), tileTitle(a))
				l.Color = u.pal.Muted
				l.MaxLines = 1
				l.Truncator = " "
				return l.Layout(gtx)
			}),
		)
	})
}

// artistFoot is the hint and Close.
func (u *UI) artistFoot(gtx C) D {
	s := u.artist
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			d := u.text(12, u.pal.Muted2, artistHint)(gtx)
			d.Size.X = gtx.Constraints.Max.X
			return d
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx C) D { return u.button(gtx, btn{&s.closeBtn, artistClose, false, true}) }),
	)
}
