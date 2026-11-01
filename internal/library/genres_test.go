package library

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/testutil"
)

// The embedded map must be the firmware repo's tools/artist_genres.json byte for byte,
// or `core index` in a checkout and `core.exe sync` on a user's machine would
// write different indexes for the same music.
func TestEmbeddedGenreMapEqualsTheRepoFile(t *testing.T) {
	// The firmware repository is a separate checkout now; only CORE_REPO
	// names it (see README.md, "Tests").
	repo := testutil.CoreRepo(t, filepath.Join("tools", "artist_genres.json"))
	want, err := os.ReadFile(filepath.Join(repo, "tools", "artist_genres.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, embeddedGenreMap) {
		t.Fatalf("internal/library/artist_genres.json differs from tools/artist_genres.json — copy it over")
	}
}

func TestLoadGenreMapEmbedded(t *testing.T) {
	m, err := LoadGenreMap(GenreMapEmbedded)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) == 0 {
		t.Fatal("embedded genre map is empty")
	}
	for k := range m {
		if k == "" || k[0] == '_' {
			t.Fatalf("comment key %q leaked into the map", k)
		}
	}
	empty, err := LoadGenreMap("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("LoadGenreMap(\"\") = %v, %v; want empty, nil", empty, err)
	}
}
