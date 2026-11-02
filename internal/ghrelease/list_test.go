package ghrelease

import (
	"context"
	"errors"
	"testing"
	"time"
)

func day(d int) time.Time { return time.Date(2026, 11, d, 12, 0, 0, 0, time.UTC) }

var fwAsset = []Asset{{Name: "core.ipod", Size: 1, URL: "x"}}

// The firmware repo's list: a prerelease firmware is newer than the real
// one, a draft is newer still, an odd tag is newest of all, and a release
// with no image sits among them. The firmware check picks v0.2.1.
func TestLatestFirmwareFromAMixedList(t *testing.T) {
	s := newAPIServer(t, map[string]Release{}, nil)
	s.list = []Release{
		{Tag: "v0.3.0", Published: day(6), Draft: true, Assets: fwAsset},
		{Tag: "v0.2.2-rc1", Published: day(4), Prerelease: true, Assets: fwAsset},
		{Tag: "v0.2.3", Published: day(3)},
		{Tag: "v0.2.1", Published: day(1), Assets: fwAsset},
		{Tag: "v0.2.0", Published: day(0), Assets: fwAsset},
		{Tag: "vscode-thing", Published: day(7), Assets: fwAsset},
	}
	fw, err := s.client().LatestFirmware(context.Background(), "owner/name")
	if err != nil || fw.Tag != "v0.2.1" {
		t.Errorf("LatestFirmware = %q, %v; want v0.2.1", fw.Tag, err)
	}
}

// The app repo's list: plain `v` tags, no image on any of them. The app
// check picks the newest published one and skips drafts, prereleases and
// tags that are not versions.
func TestLatestAppFromItsOwnList(t *testing.T) {
	s := newAPIServer(t, map[string]Release{}, nil)
	s.list = []Release{
		{Tag: "v0.4.0", Published: day(9), Draft: true},
		{Tag: "v0.3.2-rc1", Published: day(8), Prerelease: true},
		{Tag: "nightly", Published: day(7)},
		{Tag: "v0.3.1", Published: day(5), URL: "https://example/v0.3.1"},
		{Tag: "v0.3.0", Published: day(1)},
	}
	app, err := s.client().LatestApp(context.Background(), "owner/name")
	if err != nil || app.Tag != "v0.3.1" || app.URL != "https://example/v0.3.1" {
		t.Errorf("LatestApp = %+v, %v", app, err)
	}
}

func TestReposAreTheTwoRepositories(t *testing.T) {
	if DefaultRepo != "BrandonDedolph/core-firmware" || AppRepo != "BrandonDedolph/core-desktop-app" {
		t.Errorf("DefaultRepo = %q, AppRepo = %q", DefaultRepo, AppRepo)
	}
}

// A firmware tag without the image is not a firmware release (the old flow
// attached binaries before the image).
func TestLatestFirmwareNeedsTheImage(t *testing.T) {
	s := newAPIServer(t, map[string]Release{}, nil)
	s.list = []Release{
		{Tag: "v0.2.2", Published: day(5)},
		{Tag: "v0.2.1", Published: day(1), Assets: fwAsset},
	}
	fw, err := s.client().LatestFirmware(context.Background(), "owner/name")
	if err != nil || fw.Tag != "v0.2.1" {
		t.Errorf("LatestFirmware = %q, %v", fw.Tag, err)
	}
}

// A list with no image on any release (say, --repo pointed at the app's
// own repository): the firmware check says not found, it does not return
// an app release.
func TestImagelessListDoesNotBreakTheFirmwareCheck(t *testing.T) {
	s := newAPIServer(t, map[string]Release{}, nil)
	s.list = []Release{{Tag: "v0.3.0", Published: day(5)}}
	if fw, err := s.client().LatestFirmware(context.Background(), "owner/name"); !errors.Is(err, ErrNotFound) {
		t.Errorf("LatestFirmware on an imageless list = %+v, %v", fw, err)
	}
}

func TestAppNewer(t *testing.T) {
	cases := []struct {
		cand, run string
		want      bool
	}{
		{"v0.3.1", "v0.3.0", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.3.0", "v0.3.1", false},
		{"v0.10.0", "v0.9.9", true}, // numeric, not lexical
		{"v1.0.0", "v0.99.99", true},
		{"v0.3.1", "v0.3.0-4-g1a2b3c4-dirty", true},
		{"v0.3.0", "v0.3.0-4-g1a2b3c4", false},
		{"v0.3.1", "(devel)", false},
		{"v0.3.1", "1a2b3c4", false},
		{"v0.3.1", "1a2b3c4-dirty", false},
		{"nightly", "v0.3.0", false},
	}
	for _, c := range cases {
		if got := AppNewer(c.cand, c.run); got != c.want {
			t.Errorf("AppNewer(%q, %q) = %v, want %v", c.cand, c.run, got, c.want)
		}
	}
}

// Include pre-releases: a pre-release is chosen only when the client asks
// for them, and a draft is never chosen either way.
func TestPrereleasesOnlyWhenAsked(t *testing.T) {
	list := []Release{
		{Tag: "v0.3.0", Published: day(6), Draft: true, Assets: fwAsset},
		{Tag: "v0.2.2-rc1", Published: day(4), Prerelease: true, Assets: fwAsset},
		{Tag: "v0.2.1", Published: day(1), Assets: fwAsset},
	}
	for _, tc := range []struct {
		include bool
		want    string
	}{{false, "v0.2.1"}, {true, "v0.2.2-rc1"}} {
		s := newAPIServer(t, map[string]Release{}, nil)
		s.list = list
		c := s.client()
		c.IncludePrereleases = tc.include
		fw, err := c.LatestFirmware(context.Background(), "owner/name")
		if err != nil || fw.Tag != tc.want {
			t.Errorf("IncludePrereleases %v: firmware %q, %v; want %s", tc.include, fw.Tag, err, tc.want)
		}
		app, err := c.LatestApp(context.Background(), "owner/name")
		if err != nil || app.Tag != tc.want {
			t.Errorf("IncludePrereleases %v: app %q, %v; want %s", tc.include, app.Tag, err, tc.want)
		}
	}
}
