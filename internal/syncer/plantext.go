package syncer

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
)

// The plan has one printer. `core sync --dry-run` prints it (WritePlan) and
// the window's sync preview shows it behind "Show the plan" (WindowPlan), so
// the two can never describe the same plan differently.

// DryRunFooter is the CLI's last line under a printed plan. The window does
// not print it: its preview is not a command someone ran.
const DryRunFooter = "dry run — nothing was written"

// WritePlan prints the whole plan the way `core sync --dry-run` does: every
// rename and copy with its reason, every skip too with listSkips, the art,
// the playlists, what is on the device and not in the source, the totals,
// the device files, the index, and DryRunFooter.
func WritePlan(w io.Writer, p *Plan, listSkips bool) {
	writePlan(w, p, listSkips, false)
}

// WindowPlan is WritePlan's text for the window: the same lines, without
// DryRunFooter, and with the list of things not in the source pointing at
// the window's Sync and remove rather than at a command-line flag.
func WindowPlan(p *Plan) string {
	var b strings.Builder
	writePlan(&b, p, false, true)
	return strings.TrimRight(b.String(), "\n")
}

// WritePruneList prints what is under Music/ and not in the source, with the
// CLI's hint for removing it.
func WritePruneList(w io.Writer, p *Plan) {
	writePruneList(w, p, false)
}

func writePlan(w io.Writer, p *Plan, listSkips, window bool) {
	fmt.Fprintf(w, "source: %s\ndevice: %s\n\n", p.Src, p.Dst)
	fmt.Fprintf(w, "%d album(s), %d track(s)\n", p.Albums, p.Tracks)

	for _, op := range p.Rename {
		fmt.Fprintf(w, "  rename %s (%s)\n", op.Device, op.Reason)
	}
	for _, op := range p.Copy {
		fmt.Fprintf(w, "  copy   %s  %s (%s)\n", op.Device, HumanBytes(op.Size), op.Reason)
	}
	if listSkips {
		for _, op := range p.Skip {
			fmt.Fprintf(w, "  skip   %s (%s)\n", op.Device, op.Reason)
		}
	}
	var artWrite int
	for _, a := range p.Art {
		if a.Write {
			artWrite++
			fmt.Fprintf(w, "  art    %s (%s)\n", a.Album, a.Reason)
		}
	}
	for _, pl := range p.Playlists {
		state := "write"
		if pl.Unchanged {
			state = "unchanged"
		}
		fmt.Fprintf(w, "  m3u8   %s: %d entr%s, %d dropped (%s)\n",
			pl.Name, len(pl.Entries), ies(len(pl.Entries)), len(pl.Dropped), state)
	}
	if len(p.Prune) > 0 {
		writePruneList(w, p, window)
	}

	fmt.Fprintf(w, "\n  copy    %4d file(s)  %s\n", len(p.Copy), HumanBytes(p.CopyBytes))
	fmt.Fprintf(w, "  skip    %4d file(s)  %s\n", len(p.Skip), HumanBytes(p.SkipBytes))
	if len(p.Rename) > 0 {
		fmt.Fprintf(w, "  rename  %4d file(s)\n", len(p.Rename))
	}
	fmt.Fprintf(w, "  art     %4d to render, %d already valid\n", artWrite, len(p.Art)-artWrite)
	fmt.Fprintf(w, "  %s: %s\n", devicefs.ConfigName, created(p.Config))
	if p.Clock {
		fmt.Fprintf(w, "  %s: clock will be stamped\n", devicefs.ConfigName)
	}
	fmt.Fprintf(w, "  %s: %s\n", devicefs.LogName, created(p.Log))
	fmt.Fprintf(w, "  %s: %s\n", devicefs.OTGName, created(p.OTG))
	fmt.Fprintf(w, "  On-The-Go slots: %s\n", otgSlots(len(p.OTGSlots)))
	state := "write"
	if p.Index.Unchanged {
		state = "already identical"
	}
	fmt.Fprintf(w, "  %s/%s: %d bytes, %d record(s), %s\n",
		devicefs.MusicDir, devicefs.IndexName, p.Index.Size, p.Index.Records, state)
	if !window {
		fmt.Fprintln(w, "\n"+DryRunFooter)
	}
}

func writePruneList(w io.Writer, p *Plan, window bool) {
	fmt.Fprintf(w, "\n%d item(s) under %s/ are not in the source tree (%s):\n",
		len(p.Prune), devicefs.MusicDir, HumanBytes(p.PruneBytes))
	for _, victim := range p.Prune {
		rel, err := filepath.Rel(p.Dst, victim)
		if err != nil {
			rel = victim
		}
		fmt.Fprintf(w, "  %s\n", filepath.ToSlash(rel))
	}
	if window {
		fmt.Fprintln(w, "  (a sync keeps them; Sync and remove deletes them)")
		return
	}
	fmt.Fprintln(w, "  (pass --prune --yes to remove them; nothing was deleted)")
}

// otgSlots says how many of the five On-The-Go slot playlists are missing.
// An existing one is never rewritten, whatever it holds — it may be a list
// saved on the device, or a playlist of the user's own at that name.
func otgSlots(missing int) string {
	if missing == 0 {
		return "all five already there"
	}
	return fmt.Sprintf("%d of %d to create", missing, devicefs.OTGPlaylistSlots)
}

func created(b bool) string {
	if b {
		return "create"
	}
	return "present and valid, left alone"
}

func ies(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// HumanBytes prints a size the way a user reads one. Powers of 1000, because
// that is what the disk's label says.
func HumanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTP"[exp])
}
