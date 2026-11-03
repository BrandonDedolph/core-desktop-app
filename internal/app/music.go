package app

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// The Music tab: the view switch under the tab row, and the four views it
// switches between (library plan, L1). Albums is the grid as it always was;
// Artists is the same grid of artists; Genres is a list that opens onto a
// grid of one genre's albums; Songs is a table.

// The Music tab's words, in one place so the words test reads what is drawn.
const (
	musicSearchHint = "Search songs"
	musicNoMatch    = "No songs match the search."
	musicNoGenres   = "No genres: the tags of these albums carry none, and the genre map knows none of their artists."
	musicColNum     = "#"
	musicColTitle   = "Title"
	musicColArtist  = "Artist"
	musicColAlbum   = "Album"
	musicColTime    = "Time"
	musicBack       = "‹ Genres"
	musicCrumb      = "›"
)

// musicStrings is every fixed string the views draw, for the words test.
func musicStrings() []string {
	out := []string{musicSearchHint, musicNoMatch, musicNoGenres, musicColNum, musicColTitle,
		musicColArtist, musicColAlbum, musicColTime, musicBack, musicCrumb, artistHint, artistClose}
	for _, v := range musicViews {
		out = append(out, v.String())
	}
	return out
}

// catalog is the groupings of State.Albums, rebuilt when the grid is
// replaced (a new plan, a canned state). The albums are only ever replaced
// whole, so the slice's identity is the version.
func (u *UI) catalog() *Catalog {
	if u.cat == nil || !sameAlbums(u.catAlbums, u.st.Albums) {
		u.cat = BuildCatalog(u.st.Albums)
		u.catAlbums = u.st.Albums
		u.songs.built = nil
	}
	return u.cat
}

func sameAlbums(a, b []Album) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// songTable is the Songs view's order: the sort column, its direction, the
// search, and the rows they come to, worked out when one of them changes.
type songTable struct {
	col   SongSort
	desc  bool
	query string
	order []int
	built *Catalog
}

// songOrder is the Songs view's rows, kept between frames.
func (u *UI) songOrder() []int {
	cat := u.catalog()
	if u.songs.built != cat {
		u.songs.order = SongOrder(u.st.Albums, cat.Songs, u.songs.col, u.songs.desc, u.songs.query)
		u.songs.built = cat
	}
	return u.songs.order
}

// sortSongs is a click on a column head: that column ascending, or the
// other way round when it is already the one.
func (u *UI) sortSongs(col SongSort) {
	if u.songs.col == col {
		u.songs.desc = !u.songs.desc
	} else {
		u.songs.col, u.songs.desc = col, false
	}
	u.songs.built = nil
}

// rememberView saves the view to config.json when it has changed. Like
// rememberTab it runs once a frame, after the events.
func (u *UI) rememberView() {
	if u.st.View == u.savedView {
		return
	}
	u.savedView = u.st.View
	v := ""
	if u.st.View != ViewAlbums {
		v = viewKey(u.st.View)
	}
	if v != u.cfg.MusicView {
		u.cfg.MusicView = v
		u.saveConfig()
	}
}

// musicEvents is the Music tab's half of the frame.
func (u *UI) musicEvents(gtx C) {
	for i := range u.viewClicks {
		if u.viewClicks[i].Clicked(gtx) {
			u.st.View, u.st.Genre = musicViews[i], ""
		}
	}
	if u.genreBack.Clicked(gtx) {
		u.st.Genre = ""
	}
	cat := u.catalog()
	for i, c := range u.artistClicks {
		if i < len(cat.Artists) && c.Clicked(gtx) {
			u.openArtist(cat.Artists[i].Name)
		}
	}
	for i, c := range u.genreClicks {
		if i < len(cat.Genres) && c.Clicked(gtx) {
			u.st.Genre = cat.Genres[i].Name
		}
	}
	for c := range u.songHeads {
		if u.songHeads[c].Clicked(gtx) {
			u.sortSongs(SongSort(c))
		}
	}
	for {
		ev, ok := u.songSearch.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.ChangeEvent); ok && u.songSearch.Text() != u.songs.query {
			u.songs.query = u.songSearch.Text()
			u.songs.built = nil
			u.songList.Position.First, u.songList.Position.Offset = 0, 0
		}
	}
	for i := range u.songClicks {
		if i < len(cat.Songs) && u.songClicks[i].Clicked(gtx) {
			u.playSong(cat.Songs[i])
		}
	}
}

// playSong plays one song on this computer, as a click on a track in the
// album sheet does: the album's songs are the tracklist, so the next one
// follows it.
func (u *UI) playSong(r SongRow) {
	if r.Album >= len(u.st.Albums) {
		return
	}
	a := u.st.Albums[r.Album]
	if r.Song >= len(a.Songs) {
		return
	}
	u.playFrom(a, songTracks(a.Songs), r.Song, false)
}

// songPlaying reports whether the player holds this song.
func (u *UI) songPlaying(a *Album, s *Song) bool {
	np := u.st.NowPlaying
	if np == nil || np.Ended || np.Album.Device != a.Device ||
		np.TrackIndex < 0 || np.TrackIndex >= len(np.Album.TrackList) {
		return false
	}
	t := &np.Album.TrackList[np.TrackIndex]
	if t.Path != "" || s.Path != "" {
		return t.Path == s.Path
	}
	return t.Title == s.Title // a canned state, with no files
}

// --- drawing ----------------------------------------------------------------

// musicPane is the Music tab: the switch, then the view.
func (u *UI) musicPane(gtx C) D {
	if len(u.st.Albums) == 0 {
		return u.gridEmpty(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.viewSwitch),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Flexed(1, func(gtx C) D {
			switch u.st.View {
			case ViewArtists:
				return u.artistsPane(gtx)
			case ViewGenres:
				if u.st.Genre != "" {
					return u.genrePane(gtx)
				}
				return u.genresPane(gtx)
			case ViewSongs:
				return u.songsPane(gtx)
			}
			return u.albumsPane(gtx)
		}),
	)
}

// viewSwitch is "Albums · Artists · Genres · Songs", the chosen one in ink;
// in a genre, "‹ Genres › Hip-Hop". On Songs, the search field at the right.
func (u *UI) viewSwitch(gtx C) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	h := gtx.Dp(28)
	var kids []layout.FlexChild
	word := func(click *widget.Clickable, label string, on bool) layout.FlexChild {
		return layout.Rigid(func(gtx C) D {
			return click.Layout(gtx, func(gtx C) D {
				l := material.Label(u.th, unit.Sp(12.5), label)
				l.Color = u.pal.Muted
				if on {
					l.Color, l.Font.Weight = u.pal.Ink, font.SemiBold
				} else if click.Hovered() {
					l.Color = u.pal.Ink
				}
				d := rowText(gtx, l, h)
				defer clip.Rect{Max: d.Size}.Push(gtx.Ops).Pop()
				pointer.CursorPointer.Add(gtx.Ops)
				return d
			})
		})
	}
	dot := layout.Rigid(func(gtx C) D {
		return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
			l := material.Label(u.th, unit.Sp(12.5), "·")
			l.Color = u.pal.Muted2
			return rowText(gtx, l, h)
		})
	})
	if u.st.View == ViewGenres && u.st.Genre != "" {
		kids = append(kids, word(&u.genreBack, musicBack, false),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: unit.Dp(7), Right: unit.Dp(7)}.Layout(gtx, func(gtx C) D {
					l := material.Label(u.th, unit.Sp(12.5), musicCrumb)
					l.Color = u.pal.Muted2
					return rowText(gtx, l, h)
				})
			}),
			layout.Rigid(func(gtx C) D {
				l := material.Label(u.th, unit.Sp(12.5), u.st.Genre)
				l.Color, l.Font.Weight = u.pal.Ink, font.SemiBold
				return rowText(gtx, l, h)
			}))
	} else {
		for i, v := range musicViews {
			if i > 0 {
				kids = append(kids, dot)
			}
			kids = append(kids, word(&u.viewClicks[i], v.String(), u.st.View == v))
		}
	}
	kids = append(kids, layout.Flexed(1, func(gtx C) D {
		if u.st.View != ViewSongs {
			return D{Size: image.Pt(gtx.Constraints.Min.X, h)}
		}
		return layout.E.Layout(gtx, func(gtx C) D {
			w := gtx.Dp(240)
			if w > gtx.Constraints.Max.X {
				w = gtx.Constraints.Max.X
			}
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
			return u.field(gtx, &u.songSearch, musicSearchHint)
		})
	}))
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
}

// tileGrid lays n tiles out in the album grid's columns, in list, drawing
// tile i with draw.
func (u *UI) tileGrid(gtx C, list *widget.List, n int, draw func(gtx C, i, size int) D) D {
	cols, tile := gridMetrics(gtx.Constraints.Max.X, gtx.Metric.PxPerDp)
	rows := (n + cols - 1) / cols
	l := material.List(u.th, list)
	l.Indicator.Color = u.pal.SelSub
	l.Indicator.HoverColor = u.pal.Muted2
	return l.Layout(gtx, rows, func(gtx C, row int) D {
		return layout.Inset{Bottom: unit.Dp(tileGap)}.Layout(gtx, func(gtx C) D {
			children := make([]layout.FlexChild, 0, cols*2)
			for c := 0; c < cols; c++ {
				i := row*cols + c
				if c > 0 {
					children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(tileGap)}.Layout))
				}
				if i >= n {
					// An empty cell keeps the last row's tiles the same
					// size as every other row's.
					children = append(children, layout.Flexed(1, func(gtx C) D {
						return D{Size: image.Pt(gtx.Constraints.Min.X, 0)}
					}))
					continue
				}
				children = append(children, layout.Flexed(1, func(gtx C) D { return draw(gtx, i, tile) }))
			}
			return layout.Flex{Alignment: layout.Start}.Layout(gtx, children...)
		})
	})
}

// growClicks keeps one Clickable per item, kept across frames.
func growClicks(cs []*widget.Clickable, n int) []*widget.Clickable {
	for len(cs) < n {
		cs = append(cs, &widget.Clickable{})
	}
	return cs
}

// artistsPane is the Artists view: the grid of artist tiles.
func (u *UI) artistsPane(gtx C) D {
	cat := u.catalog()
	u.artistClicks = growClicks(u.artistClicks, len(cat.Artists))
	return u.tileGrid(gtx, &u.artistGrid, len(cat.Artists), func(gtx C, i, size int) D {
		return u.artistTile(gtx, &cat.Artists[i], u.artistClicks[i], size)
	})
}

// artistTile is one artist: the picture with the rolled-up badge, the name,
// and "N albums · M songs".
func (u *UI) artistTile(gtx C, ar *Artist, click *widget.Clickable, size int) D {
	return click.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := image.Pt(gtx.Constraints.Max.X, size)
				gtx.Constraints = layout.Exact(sz)
				return u.artistArt(gtx, ar, sz, gtx.Dp(4), true)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx C) D {
				l := material.Label(u.th, unit.Sp(11), ar.Name)
				l.Color = u.pal.Ink
				l.MaxLines = 1
				l.Truncator = " "
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx C) D {
				l := material.Label(u.th, unit.Sp(10), ar.Facts)
				l.Color = u.pal.Muted2
				l.MaxLines = 1
				l.Truncator = " "
				return l.Layout(gtx)
			}),
		)
	})
}

// Genre row geometry.
const (
	genreRowH   = 54
	genreCover  = 38
	genreCovers = 6
)

// genresPane is the Genres view: one row per genre.
func (u *UI) genresPane(gtx C) D {
	cat := u.catalog()
	if len(cat.Genres) == 0 {
		return u.wrapN(12.5, u.pal.Muted, musicNoGenres, 2)(gtx)
	}
	u.genreClicks = growClicks(u.genreClicks, len(cat.Genres))
	l := material.List(u.th, &u.genreList)
	l.Indicator.Color = u.pal.SelSub
	return l.Layout(gtx, len(cat.Genres), func(gtx C, i int) D {
		return u.genreRow(gtx, &cat.Genres[i], u.genreClicks[i], i+1 < len(cat.Genres))
	})
}

// genreRow is the genre's name and facts, and a strip of its covers.
func (u *UI) genreRow(gtx C, g *Genre, click *widget.Clickable, rule bool) D {
	sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(genreRowH))
	gtx.Constraints = layout.Exact(sz)
	return click.Layout(gtx, func(gtx C) D {
		if click.Hovered() {
			paint.FillShape(gtx.Ops, withAlpha(u.pal.Ink, 0x0E), clip.Rect{Max: sz}.Op())
		}
		if rule {
			paint.FillShape(gtx.Ops, u.pal.Border, clip.Rect{Min: image.Pt(0, sz.Y-gtx.Dp(1)), Max: sz}.Op())
		}
		st := clip.Rect{Max: sz}.Push(gtx.Ops)
		pointer.CursorPointer.Add(gtx.Ops)
		st.Pop()
		return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							l := material.Label(u.th, unit.Sp(13.5), g.Name)
							l.Color, l.Font.Weight = u.pal.Ink, font.SemiBold
							l.MaxLines, l.Truncator = 1, " "
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx C) D {
							l := material.Label(u.th, unit.Sp(11), g.Facts)
							l.Color = u.pal.Muted
							l.MaxLines = 1
							return l.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx C) D { return u.coverStrip(gtx, g.Albums) }),
			)
		})
	})
}

// coverStrip is up to six of the albums' covers in a row, right-aligned.
func (u *UI) coverStrip(gtx C, idx []int) D {
	n := len(idx)
	if n > genreCovers {
		n = genreCovers
	}
	c := gtx.Dp(genreCover)
	gap := gtx.Dp(5)
	w := n*c + (n-1)*gap
	if n == 0 {
		w = 0
	}
	sz := image.Pt(c, c)
	for k := 0; k < n; k++ {
		a := u.st.Albums[idx[k]]
		off := op.Offset(image.Pt(k*(c+gap), 0)).Push(gtx.Ops)
		cl := clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(3)).Push(gtx.Ops)
		if img, ok := u.cover(a, c); !ok || !u.coverFill(gtx, img, sz) {
			g := gtx
			g.Constraints = layout.Exact(sz)
			u.plainTile(g, a, sz)
		}
		cl.Pop()
		off.Pop()
	}
	return D{Size: image.Pt(w, c)}
}

// genrePane is one genre's albums, the album grid filtered.
func (u *UI) genrePane(gtx C) D {
	cat := u.catalog()
	var g *Genre
	for i := range cat.Genres {
		if cat.Genres[i].Name == u.st.Genre {
			g = &cat.Genres[i]
			break
		}
	}
	if g == nil {
		// A new scan has no albums of this genre any more.
		u.st.Genre = ""
		return u.genresPane(gtx)
	}
	return u.albumGrid(gtx, &u.genreGrid, g.Albums)
}

// --- the Songs table --------------------------------------------------------

// Song table geometry.
const (
	songRowH  = 26
	songNumW  = 34
	songTimeW = 52
	songDotW  = 24
)

// songsPane is the Songs view: the column heads and the rows.
func (u *UI) songsPane(gtx C) D {
	cat := u.catalog()
	order := u.songOrder()
	for len(u.songClicks) < len(cat.Songs) {
		u.songClicks = append(u.songClicks, widget.Clickable{})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.songHead),
		layout.Flexed(1, func(gtx C) D {
			if len(order) == 0 {
				return layout.Inset{Top: unit.Dp(10), Left: unit.Dp(8)}.Layout(gtx, u.text(12.5, u.pal.Muted, musicNoMatch))
			}
			l := material.List(u.th, &u.songList)
			l.Indicator.Color = u.pal.SelSub
			l.Indicator.HoverColor = u.pal.Muted2
			return l.Layout(gtx, len(order), func(gtx C, i int) D {
				return u.songRow(gtx, order[i])
			})
		}),
	)
}

// songColumns lays a row out in the table's columns: the number, the three
// text columns sharing what is left 4:3:3, the time, the dot.
func songColumns(gtx C, num, title, artist, album, tm, dot layout.Widget) D {
	gap := layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout)
	fixed := func(w unit.Dp, f layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx C) D {
			px := gtx.Dp(w)
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = px, px
			return f(gtx)
		})
	}
	return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(6)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			fixed(songNumW, num), gap,
			layout.Flexed(4, title), gap,
			layout.Flexed(3, artist), gap,
			layout.Flexed(3, album), gap,
			fixed(songTimeW, tm),
			fixed(songDotW, dot),
		)
	})
}

// songHead is the column heads; the four sortable ones click, and the sorted
// one is in ink with its direction drawn beside it.
func (u *UI) songHead(gtx C) D {
	h := gtx.Dp(24)
	head := func(c SongSort, label string, end bool) layout.Widget {
		return func(gtx C) D {
			click := &u.songHeads[c]
			return click.Layout(gtx, func(gtx C) D {
				on := u.songs.col == c
				col := u.pal.Muted2
				if on || click.Hovered() {
					col = u.pal.Ink
				}
				l := material.Label(u.th, unit.Sp(11), label)
				l.Color, l.Font.Weight = col, font.SemiBold
				if end {
					l.Alignment = text.End
				}
				g := gtx
				g.Constraints.Min.X = 0 // the word's own width: the mark goes after it
				if end {
					g.Constraints.Min.X = gtx.Constraints.Max.X - gtx.Dp(12)
				}
				d := rowText(g, l, h)
				if on {
					x := d.Size.X + gtx.Dp(4)
					if end {
						x = gtx.Constraints.Max.X - gtx.Dp(8)
					}
					u.sortMark(gtx, image.Pt(x, h/2), u.songs.desc)
				}
				d.Size.X = gtx.Constraints.Max.X
				st := clip.Rect{Max: d.Size}.Push(gtx.Ops)
				pointer.CursorPointer.Add(gtx.Ops)
				st.Pop()
				return d
			})
		}
	}
	num := func(gtx C) D {
		l := material.Label(u.th, unit.Sp(11), musicColNum)
		l.Color, l.Font.Weight, l.Alignment = u.pal.Muted2, font.SemiBold, text.End
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return rowText(gtx, l, h)
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	d := songColumns(gtx, num, head(SortTitle, musicColTitle, false), head(SortArtist, musicColArtist, false),
		head(SortAlbum, musicColAlbum, false), head(SortTime, musicColTime, true),
		func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, h)} })
	paint.FillShape(gtx.Ops, u.pal.Border, clip.Rect{Min: image.Pt(0, d.Size.Y-gtx.Dp(1)), Max: d.Size}.Op())
	return d
}

// sortMark is a small triangle centred on c: pointing up for ascending,
// down for descending. Drawn, like the transport's glyphs: the faces have
// no arrows of the same weight.
func (u *UI) sortMark(gtx C, c image.Point, desc bool) {
	w, h := float32(gtx.Dp(7)), float32(gtx.Dp(4))
	x, y := float32(c.X), float32(c.Y)
	var p clip.Path
	p.Begin(gtx.Ops)
	if desc {
		p.MoveTo(f32.Pt(x-w/2, y-h/2))
		p.LineTo(f32.Pt(x+w/2, y-h/2))
		p.LineTo(f32.Pt(x, y+h/2))
	} else {
		p.MoveTo(f32.Pt(x-w/2, y+h/2))
		p.LineTo(f32.Pt(x+w/2, y+h/2))
		p.LineTo(f32.Pt(x, y-h/2))
	}
	p.Close()
	paint.FillShape(gtx.Ops, u.pal.Ink, clip.Outline{Path: p.End()}.Op())
}

// songRow is one song: number, title, artist, album, time and its sync dot,
// with the iPod's selected row when it is the one playing.
func (u *UI) songRow(gtx C, i int) D {
	cat := u.cat
	r := &cat.Songs[i]
	a := &u.st.Albums[r.Album]
	s := &a.Songs[r.Song]
	click := &u.songClicks[i]
	sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(songRowH))
	gtx.Constraints = layout.Exact(sz)
	playing := u.songPlaying(a, s)
	fg, sub := u.pal.Ink, u.pal.Muted
	if playing {
		fg, sub = u.pal.Surface, withAlpha(u.pal.Surface, 0xB0)
	}
	h := sz.Y
	cell := func(txt string, col color.NRGBA, end bool) layout.Widget {
		return func(gtx C) D {
			l := material.Label(u.th, unit.Sp(12), txt)
			l.Color = col
			l.Truncator = " "
			if end {
				l.Alignment = text.End
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
			}
			return rowText(gtx, l, h)
		}
	}
	return click.Layout(gtx, func(gtx C) D {
		switch {
		case playing:
			paint.FillShape(gtx.Ops, u.pal.Ink, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(4)).Op(gtx.Ops))
		case click.Hovered():
			paint.FillShape(gtx.Ops, withAlpha(u.pal.Ink, 0x0E), clip.Rect{Max: sz}.Op())
		}
		if s.Path != "" {
			st := clip.Rect{Max: sz}.Push(gtx.Ops)
			pointer.CursorPointer.Add(gtx.Ops)
			st.Pop()
		}
		num := cell(r.Num, sub, true)
		if playing {
			num = func(gtx C) D {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
				return layout.E.Layout(gtx, func(gtx C) D { return u.rowBars(gtx, sub, !u.st.NowPlaying.Playing) })
			}
		}
		return songColumns(gtx, num, cell(s.Title, fg, false), cell(s.Artist, sub, false),
			cell(r.AlbumTitle, sub, false), cell(r.Time, sub, true),
			func(gtx C) D { return u.syncDot(gtx, s.State, fg, h) })
	})
}

// syncDot is a song's side of the plan: a filled dot for NEW, a ring for
// CHANGED, nothing for a song already on the iPod — the tiles' badges, as
// dots.
func (u *UI) syncDot(gtx C, st TileState, col color.NRGBA, h int) D {
	sz := image.Pt(gtx.Constraints.Max.X, h)
	d := gtx.Dp(7)
	box := image.Rectangle{Min: image.Pt((sz.X-d)/2+gtx.Dp(3), (h-d)/2)}
	box.Max = box.Min.Add(image.Pt(d, d))
	switch st {
	case TileNew:
		paint.FillShape(gtx.Ops, col, clip.Ellipse(box).Op(gtx.Ops))
	case TileChanged:
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.Ellipse(box).Path(gtx.Ops), Width: float32(gtx.Dp(1.5))}.Op())
	}
	return D{Size: sz}
}
