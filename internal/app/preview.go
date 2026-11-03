package app

import (
	"fmt"

	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// The sync preview: what a sync will do, shown before it does it. The
// header's "Sync N albums" and the iPod tab's "Preview sync" both open it
// (startSyncPreview); its button runs the sync. Nothing on the iPod changes
// until that button is pressed.
//
// It says why, top to bottom: a card with one line per kind of work the plan
// holds (new albums, changed ones, renames, covers, playlists, the index,
// what is on the iPod and not in the folder), the albums to copy with the
// why in their middle column, and the plan itself — the same text `core
// sync --dry-run` prints — behind "Show the plan".

// previewMax is how many albums the preview lists before "… and N more".
const previewMax = 40

// previewCardW is the card's label column: wide enough for "On the iPod,
// not in your folder" on one line.
const previewCardW = 184

// previewDialog is the preview for one plan. albums are the plan's tiles
// (AlbumsFromPlan); plan supplies the rest of the work and the text behind
// "Show the plan" and may be nil (albums only). src names the music folder
// in the total line; tooOld turns the button off for an iPod whose firmware
// cannot read what a sync writes. info reads a path on the iPod that is not
// in the folder (pathInfo; nil means pathInfo, a test passes its own).
func previewDialog(plan *syncer.Plan, albums []Album, src string, tooOld bool, info func(string) (int64, bool)) *dialogRequest {
	var rows []cardRow
	n, tracks := 0, 0
	var bytes int64
	for _, a := range albums {
		if a.Copy == 0 {
			continue
		}
		n++
		tracks += a.Copy
		bytes += a.Bytes
		if n > previewMax {
			continue
		}
		mid := fmt.Sprintf("%d of %d tracks", a.Copy, a.Tracks)
		if a.Copy == a.Tracks {
			mid = fmt.Sprintf("all %d track%s", a.Tracks, plural(a.Tracks))
		}
		mid += " · " + albumWhy(a)
		rows = append(rows, cardRow{label: a.Title, mid: mid, value: binarySize(a.Bytes)})
	}
	if n > previewMax {
		rows = append(rows, cardRow{label: fmt.Sprintf("… and %d more", n-previewMax)})
	}
	card, work := previewCard(plan, albums, info)
	text := ""
	if plan != nil {
		text = syncer.WindowPlan(plan)
	}

	if n == 0 {
		if !work {
			req := &dialogRequest{
				title:   "Everything is on the iPod",
				note:    "Your music folder (" + sourceName(src) + ") and the iPod match: a sync would copy nothing.",
				plan:    text,
				okLabel: "Close",
				only:    true,
			}
			// Nothing to do, but what is on the iPod and not in the
			// folder is still worth saying.
			for _, r := range card {
				if r.label == previewStaysLabel {
					req.card, req.cardW = []cardRow{r}, previewCardW
				}
			}
			return req
		}
		req := &dialogRequest{
			title:   "Nothing to copy",
			card:    card,
			cardW:   previewCardW,
			plan:    text,
			note:    "Nothing has been written. Your music is on the iPod already; Sync does the rest of the work above.",
			okLabel: "Sync",
		}
		if tooOld {
			req.note = "Nothing has been written. " + upperFirst(TooOldText) + "."
			req.okLabel, req.only = "Close", true
		}
		return req
	}
	total := fmt.Sprintf("%d track%s", tracks, plural(tracks))
	if bytes > 0 {
		total += " · " + binarySize(bytes)
	}
	total += " · from " + sourceName(src)
	req := &dialogRequest{
		title:   fmt.Sprintf("Preview: %d album%s to copy", n, plural(n)),
		card:    card,
		cardW:   previewCardW,
		items:   rows,
		total:   total,
		plan:    text,
		note:    "Nothing has been written. Sync copies these to Music/ on the iPod and rebuilds the index.",
		okLabel: fmt.Sprintf("Sync %d album%s", n, plural(n)),
	}
	if tooOld {
		req.note = "Nothing has been written. " + upperFirst(TooOldText) + "."
		req.okLabel, req.only = "Close", true
	}
	return req
}

// The card's labels.
const (
	previewNewLabel      = "New on the iPod"
	previewChangedLabel  = "Changed since the last sync"
	previewRenamedLabel  = "Renamed on the iPod"
	previewCoversLabel   = "Covers to render"
	previewPlaylistLabel = "Playlists"
	previewIndexLabel    = "Index"
	previewStaysLabel    = "On the iPod, not in your folder"
	previewFilesLabel    = "Device files"
)

// deviceFilesToCreate counts the device files the plan creates: the three
// in the volume root and the On-The-Go slot playlists that are missing.
func deviceFilesToCreate(p *syncer.Plan) int {
	n := len(p.OTGSlots)
	for _, b := range []bool{p.Config, p.Log, p.OTG} {
		if b {
			n++
		}
	}
	return n
}

// albumWhy is the album row's why: "changed" when any of its copies is of a
// file the iPod has another version of, else "new".
func albumWhy(a Album) string {
	if a.Changed > 0 {
		return "changed"
	}
	return "new"
}

// previewCard is the card's lines — only the ones that apply — and whether
// the plan has work for a sync to do beyond copies: covers, playlists, a
// changed index or renames. (What is on the iPod and not in the folder is
// a line, not work: a sync leaves it.)
func previewCard(plan *syncer.Plan, albums []Album, info func(string) (int64, bool)) ([]cardRow, bool) {
	var rows []cardRow
	var newA, newT, chA, chT, renamed int
	var newB, chB int64
	for _, a := range albums {
		renamed += a.Renamed
		if a.Copy == 0 {
			continue
		}
		if a.Changed > 0 {
			chA, chT, chB = chA+1, chT+a.Copy, chB+a.Bytes
		} else {
			newA, newT, newB = newA+1, newT+a.Copy, newB+a.Bytes
		}
	}
	if newA > 0 {
		rows = append(rows, cardRow{label: previewNewLabel, value: countLine(newA, newT, newB)})
	}
	if chA > 0 {
		rows = append(rows, cardRow{label: previewChangedLabel, value: countLine(chA, chT, chB)})
	}
	if renamed > 0 {
		rows = append(rows, cardRow{label: previewRenamedLabel, value: fmt.Sprintf("%d track%s", renamed, plural(renamed))})
	}
	work := renamed > 0
	if plan == nil {
		return rows, work
	}
	covers := 0
	for _, a := range plan.Art {
		if a.Write {
			covers++
		}
	}
	if covers > 0 {
		work = true
		rows = append(rows, cardRow{label: previewCoversLabel, value: fmt.Sprintf("%d album%s", covers, plural(covers))})
	}
	pls := 0
	for _, pl := range plan.Playlists {
		if !pl.Unchanged {
			pls++
		}
	}
	if pls > 0 {
		work = true
		rows = append(rows, cardRow{label: previewPlaylistLabel, value: fmt.Sprintf("%d to write", pls)})
	}
	// An index of no bytes is a plan that planned none (a test's, or a
	// canned one's): there is nothing to say about it.
	if plan.Index.Size > 0 {
		v := "unchanged"
		if !plan.Index.Unchanged {
			work = true
			v = fmt.Sprintf("rewritten, %d song%s", plan.Index.Records, plural(plan.Index.Records))
		}
		rows = append(rows, cardRow{label: previewIndexLabel, value: v})
	}
	// The device files a first sync creates — CORECFG.DAT, CORELOG.BIN,
	// COREOTG.DAT and the On-The-Go slots — are writes too: a plan with
	// nothing to copy and one of these to create is not "everything is on
	// the iPod".
	if files := deviceFilesToCreate(plan); files > 0 {
		work = true
		rows = append(rows, cardRow{label: previewFilesLabel, value: fmt.Sprintf("%d to create", files)})
	}
	if k := len(plan.Prune); k > 0 {
		if info == nil {
			info = pathInfo
		}
		albumsOnly := true
		for _, p := range plan.Prune {
			_, dir := info(p)
			if _, isAlbum := pruneLabel(p, dir); !isAlbum {
				albumsOnly = false
				break
			}
		}
		noun := "item"
		if albumsOnly {
			noun = "album"
		}
		v := fmt.Sprintf("%d %ss stay (Sync and remove deletes them)", k, noun)
		if k == 1 {
			v = fmt.Sprintf("1 %s stays (Sync and remove deletes it)", noun)
		}
		rows = append(rows, cardRow{label: previewStaysLabel, value: v})
	}
	return rows, work
}

// countLine is "3 albums · 31 tracks · 1.2 GB".
func countLine(albums, tracks int, bytes int64) string {
	s := fmt.Sprintf("%d album%s · %d track%s", albums, plural(albums), tracks, plural(tracks))
	if bytes > 0 {
		s += " · " + binarySize(bytes)
	}
	return s
}

// binarySize is humanBinary as one string, "" for nothing.
func binarySize(n int64) string {
	if n <= 0 {
		return ""
	}
	v, unit := humanBinary(n)
	return v + " " + unit
}

// previewLog is the log's line for one album the preview would copy:
// "would copy X — 3 of 10 tracks, 90.0 MB, changed (2 changed, 1 new)".
func previewLog(a Album, size string) string {
	why := albumWhy(a)
	if a.Changed > 0 && a.New > 0 {
		why += fmt.Sprintf(" (%d changed, %d new)", a.Changed, a.New)
	}
	return fmt.Sprintf("would copy %s — %d of %d tracks, %s, %s", a.Title, a.Copy, a.Tracks, size, why)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	if c := s[0]; c >= 'a' && c <= 'z' {
		return string(c-'a'+'A') + s[1:]
	}
	return s
}
