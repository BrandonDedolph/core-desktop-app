//go:build windows

package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

// On Windows the hidden and system attributes hide an entry too, the way
// Explorer does: desktop.ini, a profile's "My Music" junction.
func TestChooserSkipsHiddenAttribute(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "Shown/", "Hidden/", "System/", "shown.ipod", "hidden.ipod")
	for name, attr := range map[string]uint32{
		"Hidden":      windows.FILE_ATTRIBUTE_HIDDEN,
		"System":      windows.FILE_ATTRIBUTE_SYSTEM,
		"hidden.ipod": windows.FILE_ATTRIBUTE_HIDDEN,
	} {
		p, err := windows.UTF16PtrFromString(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetFileAttributes(p, attr); err != nil {
			t.Fatal(err)
		}
	}
	es, err := readChooserDir(dir, chooseFirmwareKind)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNames(es); !reflect.DeepEqual(got, []string{"Shown", "shown.ipod"}) {
		t.Errorf("listed %q", got)
	}
}

// The drives answer, each with a name Explorer would give it.
func TestListDrivesFindsTheSystemDrive(t *testing.T) {
	ds := listDrives()
	if len(ds) == 0 {
		t.Fatal("no drives")
	}
	for _, e := range chooserRoots("windows", "", ds) {
		if e.path == "" || e.name == "" {
			t.Errorf("drive %+v", e)
		}
	}
}
