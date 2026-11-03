package app

import (
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// whyPlan is a plan of one album: copies with the given whys, skips to make
// up tracks, and whatever extra the caller sets.
func whyPlan(album string, tracks int, whys ...syncer.CopyWhy) *syncer.Plan {
	p := &syncer.Plan{Src: "/m/MC", Dst: "/ipod", AlbumOrder: []string{album}, Albums: 1}
	for i := 0; i < tracks; i++ {
		op := syncer.FileOp{Album: album, Src: "/m/MC/" + album + "/" + itoa(i) + ".flac",
			Device: "Music/" + album + "/" + itoa(i) + ".flac", Size: 10 << 20}
		p.Tracks++
		if i < len(whys) {
			op.Why, op.Reason = whys[i], string(whys[i])
			p.Copy = append(p.Copy, op)
			p.CopyBytes += op.Size
			continue
		}
		op.Why, op.Reason = syncer.WhySame, "same size and time"
		p.Skip = append(p.Skip, op)
	}
	return p
}

func card(d *dialogRequest) map[string]string {
	m := map[string]string{}
	for _, r := range d.card {
		m[r.label] = r.value
	}
	return m
}

func TestAlbumsCountNewChangedAndRenamed(t *testing.T) {
	p := whyPlan("A - B", 5, syncer.WhyNew, syncer.WhyTime, syncer.WhySize)
	p.Rename = []syncer.FileOp{{Album: "A - B", Src: "/ipod/Music/A - B/X.flac", Why: syncer.WhySame}}
	a := AlbumsFromPlan(p)
	if len(a) != 1 || a[0].New != 1 || a[0].Changed != 2 || a[0].Renamed != 1 || a[0].Copy != 3 || a[0].Tracks != 6 {
		t.Fatalf("album = %+v", a)
	}
	if albumWhy(a[0]) != "changed" {
		t.Errorf("an album with a changed copy reads %q", albumWhy(a[0]))
	}
	if got := previewLog(a[0], "31 MB"); got != "would copy A - B — 3 of 6 tracks, 31 MB, changed (2 changed, 1 new)" {
		t.Errorf("log line = %q", got)
	}
}

func TestPreviewOfANewOnlyPlan(t *testing.T) {
	p := whyPlan("Talking Heads - Remain in Light", 8,
		syncer.WhyNew, syncer.WhyNew, syncer.WhyNew, syncer.WhyNew, syncer.WhyNew, syncer.WhyNew, syncer.WhyNew, syncer.WhyNew)
	p.Art = []syncer.ArtOp{{Album: "Talking Heads - Remain in Light", Write: true}}
	p.Index = syncer.IndexOp{Size: 1000, Records: 8}
	d := previewDialog(p, AlbumsFromPlan(p), "/m/MC", false, nil)
	if d.title != "Preview: 1 album to copy" || d.okLabel != "Sync 1 album" {
		t.Fatalf("%q / %q", d.title, d.okLabel)
	}
	want := map[string]string{
		"New on the iPod":  "1 album · 8 tracks · 80 MB",
		"Covers to render": "1 album",
		"Index":            "rewritten, 8 songs",
	}
	if got := card(d); !sameMap(got, want) {
		t.Errorf("card = %v, want %v", got, want)
	}
	if len(d.items) != 1 || d.items[0].mid != "all 8 tracks · new" {
		t.Errorf("rows = %+v", d.items)
	}
	if !strings.Contains(d.plan, "copy   Music/Talking Heads - Remain in Light/0.flac") ||
		strings.Contains(d.plan, syncer.DryRunFooter) {
		t.Errorf("plan text:\n%s", d.plan)
	}
}

func TestPreviewOfAChangedOnlyPlan(t *testing.T) {
	// The daylight-saving bench: every track on the iPod reads an hour
	// out, so every track is a copy and none of them is new.
	p := whyPlan("Kate Bush - Hounds of Love", 4, syncer.WhyTime, syncer.WhyTime, syncer.WhyTime, syncer.WhyTime)
	p.Index = syncer.IndexOp{Size: 1000, Records: 4, Unchanged: true}
	p.Prune = []string{"/ipod/Music/Gone - Away"}
	d := previewDialog(p, AlbumsFromPlan(p), "/m/MC", false, func(string) (int64, bool) { return 0, true })
	want := map[string]string{
		"Changed since the last sync":     "1 album · 4 tracks · 40 MB",
		"Index":                           "unchanged",
		"On the iPod, not in your folder": "1 album stays (Sync and remove deletes it)",
	}
	if got := card(d); !sameMap(got, want) {
		t.Errorf("card = %v, want %v", got, want)
	}
	if len(d.items) != 1 || d.items[0].mid != "all 4 tracks · changed" {
		t.Errorf("rows = %+v", d.items)
	}
	if !strings.Contains(d.plan, "(time)") {
		t.Errorf("the plan does not carry the reasons:\n%s", d.plan)
	}
}

func TestPreviewWithNoCopiesButCovers(t *testing.T) {
	p := whyPlan("A - B", 3)
	p.Art = []syncer.ArtOp{{Album: "A - B", Write: true}}
	p.Playlists = []syncer.PlaylistOp{{Name: "x.m3u8"}, {Name: "y.m3u8", Unchanged: true}}
	d := previewDialog(p, AlbumsFromPlan(p), "/m/MC", false, nil)
	if d.title != "Nothing to copy" || d.okLabel != "Sync" || d.solo() || len(d.items) != 0 {
		t.Fatalf("%+v", d)
	}
	want := map[string]string{"Covers to render": "1 album", "Playlists": "1 to write"}
	if got := card(d); !sameMap(got, want) {
		t.Errorf("card = %v, want %v", got, want)
	}
	if d.plan == "" {
		t.Error("no plan behind the link")
	}
	if d := previewDialog(p, AlbumsFromPlan(p), "/m/MC", true, nil); !d.solo() {
		t.Error("too-old firmware still offers the sync")
	}
}

// A first sync onto an iPod that has every track already (a library
// copied by hand) still creates the device files: that is work, and the
// preview offers the sync for it.
func TestPreviewWithOnlyDeviceFilesToCreate(t *testing.T) {
	p := whyPlan("A - B", 3)
	p.Index = syncer.IndexOp{Size: 1000, Records: 3, Unchanged: true}
	p.Config, p.Log = true, true
	p.OTGSlots = []string{"On-The-Go 1.m3u8", "On-The-Go 2.m3u8"}
	d := previewDialog(p, AlbumsFromPlan(p), "/m/MC", false, nil)
	if d.title != "Nothing to copy" || d.okLabel != "Sync" || d.solo() {
		t.Fatalf("%+v", d)
	}
	if got := card(d)[previewFilesLabel]; got != "4 to create" {
		t.Errorf("device files = %q", got)
	}
}

// The grid's chip agrees with the preview's word: an album whose every
// track the iPod has an older copy of is CHANGED, not NEW.
func TestAnAllChangedAlbumIsChangedNotNew(t *testing.T) {
	p := whyPlan("A - B", 2, syncer.WhyTime, syncer.WhyTime)
	if a := AlbumsFromPlan(p); a[0].State != TileChanged {
		t.Errorf("state = %v, want CHANGED", a[0].State)
	}
	p = whyPlan("A - B", 2, syncer.WhyNew, syncer.WhyNew)
	if a := AlbumsFromPlan(p); a[0].State != TileNew {
		t.Errorf("state = %v, want NEW", a[0].State)
	}
	p = whyPlan("A - B", 3, syncer.WhyNew, syncer.WhyNew)
	if a := AlbumsFromPlan(p); a[0].State != TileChanged {
		t.Errorf("two of three new: state = %v, want CHANGED", a[0].State)
	}
}

func TestPreviewOfAnEmptyPlan(t *testing.T) {
	p := whyPlan("A - B", 3)
	p.Index = syncer.IndexOp{Size: 1000, Records: 3, Unchanged: true}
	p.Playlists = []syncer.PlaylistOp{{Name: "y.m3u8", Unchanged: true}}
	d := previewDialog(p, AlbumsFromPlan(p), "/m/MC", false, nil)
	if d.title != "Everything is on the iPod" || !d.solo() || d.okLabel != "Close" || len(d.card) != 0 {
		t.Errorf("%+v", d)
	}
}

// The canned preview has every line, and nothing on its front — nor in
// its plan — uses a word the front gave up.
func TestCannedPreview(t *testing.T) {
	u := NewUI(Options{})
	u.SetState(DemoState())
	d := cannedPreview(u)
	got := card(d)
	for _, l := range []string{previewNewLabel, previewChangedLabel, previewCoversLabel,
		previewPlaylistLabel, previewIndexLabel, previewStaysLabel} {
		if got[l] == "" {
			t.Errorf("the canned card has no %q line: %v", l, got)
		}
	}
	if got[previewStaysLabel] != "2 albums stay (Sync and remove deletes them)" {
		t.Errorf("stays = %q", got[previewStaysLabel])
	}
	for _, bad := range []string{"dry run", "Dry run", "prune", "Prune"} {
		if strings.Contains(d.plan, bad) {
			t.Errorf("the plan text contains %q", bad)
		}
	}
	if !strings.Contains(d.plan, "Music/Old Band - First Album") {
		t.Errorf("plan:\n%s", d.plan)
	}
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
