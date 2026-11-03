package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
	"github.com/BrandonDedolph/core-desktop-app/internal/flasher"
	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// DialogKinds are the dialogs `core-app --screenshot --dialog KIND` can
// put on screen, for review. They are drawn and nothing else: a canned
// dialog has no job behind it, so nothing can be confirmed through one.
var DialogKinds = []string{"write", "holding", "remove", "about", "album", "preview", "settings"}

// cannedDialog builds one of DialogKinds against the state's device.
func cannedDialog(u *UI, kind string) error {
	g := u.gate("Update this iPod to Core " + u.st.Release.Tag + "?")
	switch kind {
	case "write", "holding":
		req, err := writeDialog(g, g.dev.Path, 443_392, strings.TrimSpace(flashPlanSample))
		if err != nil {
			return err
		}
		u.dlg = req
		if kind == "holding" {
			u.holdFrozen = 0.45
		}
	case "remove":
		// A mixed list, the way a real one comes out of the syncer: two
		// album folders that are gone from the source, a stray file in
		// an album that stays, and a playlist nobody names any more.
		plan := &syncer.Plan{PruneBytes: 1_288_490_189, Prune: []string{
			`D:\Music\Old Band - First Album`,
			`D:\Music\Someone Else - Live at the Hall`,
			`D:\Music\Talking Heads - Remain in Light\09 - The Overload (demo).flac`,
			`D:\Music\Playlists\Road trip 2019.m3u8`,
		}}
		info := map[string]int64{
			plan.Prune[0]: 412_000_000, plan.Prune[1]: 830_000_000,
			plan.Prune[2]: 46_489_000, plan.Prune[3]: 1_189,
		}
		u.dlg = removalDialog(plan, func(p string) (int64, bool) {
			return info[p], !strings.Contains(p, ".")
		})
	case "about":
		u.showAbout()
		return nil
	case "album":
		// The sheet on Remain in Light (or a real library's hero album),
		// its third track playing.
		if u.st.NowPlaying == nil {
			setDemoPlaying(&u.st)
		}
		np := u.st.NowPlaying
		if np == nil {
			return fmt.Errorf("--dialog album: no album in the state carries a tracklist "+
				"(with --source: no album with %d+ tracks and a square embedded cover)", heroTracks)
		}
		u.openSheet(u.st.Albums[np.AlbumIndex])
		return nil
	case "settings":
		u.openSettings()
		return nil
	case "preview":
		u.dlg = cannedPreview(u)
	default:
		return fmt.Errorf("--dialog %q: want one of %s", kind, strings.Join(DialogKinds, ", "))
	}
	u.dlg.reply = make(chan string, 1)
	return nil
}

// flashPlanSample is what the flasher prints before it asks, for the canned
// write dialogs' "Show the plan".
var flashPlanSample = `
device      \\.\PhysicalDrive2  iPod Video 5.5G 80 GB  80.0 GB
partition   0: 129,024 .. 131,604,480 (131,475,456 bytes), sector 2048
image       C:\Users\you\AppData\Roaming\core\cache\v0.1.3\core.ipod
            443,392 bytes, checksum 0x0163b2a0 (.ipod header verified)

OSOS entry  devOffset 0x4800, body at 0x5000, capacity 7,618,560
  old       len 237,640  chksum 0x01589d64
  new       len 443,392  chksum 0x0163b2a0

backup      C:\Users\you\AppData\Roaming\core\backups\fwpart-131475456-2026-09-15T01-12-04Z.bin
` + "\n" + flasher.ConfirmPrompt(`\\.\PhysicalDrive2`)

// cannedPreview is `--dialog preview`: the sync preview for a plan made up
// from the state's grid (cannedPlan), so the card, the albums' whys and
// "Show the plan" all have something to say with no iPod attached.
func cannedPreview(u *UI) *dialogRequest {
	plan := cannedPlan(u.st.Albums)
	return previewDialog(plan, AlbumsFromPlan(plan), u.st.Source, u.st.FirmwareTooOld(),
		func(string) (int64, bool) { return 0, true })
}

// cannedPlan is the plan the grid's tiles describe: a NEW album's tracks
// are copies of files the iPod does not have, a CHANGED album's are copies
// of files modified since the last sync, the rest are skips; every NEW
// album gets its covers rendered, one playlist is written, the index is
// rewritten, and two albums are on the iPod and not in the folder.
func cannedPlan(albums []Album) *syncer.Plan {
	p := &syncer.Plan{Src: `C:\Users\you\Music\MC`, Dst: `D:\`, Clock: true}
	for _, a := range albums {
		p.AlbumOrder = append(p.AlbumOrder, a.Device)
		p.Albums++
		var size int64
		if a.Copy > 0 {
			size = a.Bytes / int64(a.Copy)
		}
		for i := 1; i <= a.Tracks; i++ {
			p.Tracks++
			name := fmt.Sprintf("%02d. Track %d.flac", i, i)
			op := syncer.FileOp{
				Src:    a.Dir + `\` + name,
				Dst:    `D:\Music\` + a.Device + `\` + name,
				Device: "Music/" + a.Device + "/" + name,
				Album:  a.Device,
				Size:   size,
			}
			switch {
			case i > a.Copy:
				op.Reason, op.Why = "same size and time", syncer.WhySame
				p.Skip = append(p.Skip, op)
				continue
			case a.State == TileNew:
				op.Reason, op.Why = "not on the device", syncer.WhyNew
			default:
				op.Reason, op.Why = "modified 3h12m40s after the copy on the device", syncer.WhyTime
			}
			p.Copy = append(p.Copy, op)
			p.CopyBytes += size
		}
		art := syncer.ArtOp{Album: a.Device, Reason: "folder.art + folder.thm are already valid"}
		if a.State == TileNew {
			art.Write, art.Reason = true, "missing or invalid sidecars"
		}
		p.Art = append(p.Art, art)
	}
	p.Playlists = []syncer.PlaylistOp{
		{Name: "Favourites.m3u8", Entries: make([]string, 24)},
		{Name: "Road trip.m3u8", Entries: make([]string, 40), Unchanged: true},
	}
	p.Index = syncer.IndexOp{Size: 64 + 220*p.Tracks, Records: p.Tracks}
	p.Prune = []string{
		filepath.Join(p.Dst, devicefs.MusicDir, "Old Band - First Album"),
		filepath.Join(p.Dst, devicefs.MusicDir, "Someone Else - Live at the Hall"),
	}
	p.PruneBytes = 1_242_000_000
	return p
}
