// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeTB records what the helper asked for. Like the real thing, Skip and
// Fatalf stop the caller; here by a panic that run recovers.
type fakeTB struct {
	testing.TB
	skipped, failed bool
}

type stop struct{}

func (f *fakeTB) Helper()               {}
func (f *fakeTB) Skip(...any)           { f.skipped = true; panic(stop{}) }
func (f *fakeTB) Fatalf(string, ...any) { f.failed = true; panic(stop{}) }

func (f *fakeTB) run(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(stop); !ok {
				panic(r)
			}
		}
	}()
	fn()
}

func TestCoreRepo(t *testing.T) {
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "tools", "x.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, repo, require string
		marker              string
		want                string
		skip, fail          bool
	}{
		{name: "unset skips", skip: true},
		{name: "unset, require=0 still skips", require: "0", skip: true},
		{name: "unset under require fails", require: "1", fail: true},
		{name: "set and valid", repo: tree, require: "1", marker: "tools/x.py", want: tree},
		{name: "set but wrong tree fails", repo: tree, marker: "core/main.c", fail: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(RepoEnv, c.repo)
			t.Setenv(RequireEnv, c.require)
			f := &fakeTB{}
			var markers []string
			if c.marker != "" {
				markers = append(markers, c.marker)
			}
			var got string
			f.run(func() { got = CoreRepo(f, markers...) })
			if got != c.want || f.skipped != c.skip || f.failed != c.fail {
				t.Errorf("got %q skipped=%v failed=%v, want %q skipped=%v failed=%v",
					got, f.skipped, f.failed, c.want, c.skip, c.fail)
			}
		})
	}
}
