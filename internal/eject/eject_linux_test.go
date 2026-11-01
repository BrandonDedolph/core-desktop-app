// SPDX-License-Identifier: Apache-2.0

//go:build linux

package eject

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost replaces the programs and the mount table for one test: which
// programs exist, what each udisksctl verb answers, and every command run.
type fakeHost struct {
	missing map[string]bool       // programs lookPath does not find
	fail    map[string]fakeResult // keyed by the verb ("unmount", "power-off") or program
	ran     []string              // every command, joined with spaces
}

type fakeResult struct {
	out string
	err error
}

func install(t *testing.T, h *fakeHost, mountinfo string) {
	t.Helper()
	oldLook, oldRun, oldMI := lookPath, runCmd, mountInfoPath
	t.Cleanup(func() { lookPath, runCmd, mountInfoPath = oldLook, oldRun, oldMI })
	lookPath = func(name string) (string, error) {
		if h.missing[name] {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		return "/usr/bin/" + name, nil
	}
	runCmd = func(name string, args ...string) ([]byte, error) {
		h.ran = append(h.ran, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		key := filepath.Base(name)
		if len(args) > 0 {
			key = args[0]
		}
		if r, ok := h.fail[key]; ok {
			return []byte(r.out), r.err
		}
		return nil, nil
	}
	mountInfoPath = filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(mountInfoPath, []byte(mountinfo), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mountLine is one /proc/self/mountinfo row: point mounted from source.
func mountLine(point, source string) string {
	return fmt.Sprintf("36 25 8:17 / %s rw,relatime shared:1 - vfat %s rw,fmask=0022\n", point, source)
}

// A fake /dev: a disk node and its partition, as files in a temp dir, so
// parentDisk's existence check sees a real parent. resolveDevice only takes
// a /dev/ path on trust, so the tests reach these through a mount point.
func fakeDev(t *testing.T) (disk, part string) {
	t.Helper()
	dir := t.TempDir()
	disk, part = filepath.Join(dir, "sdb"), filepath.Join(dir, "sdb1")
	for _, p := range []string{disk, part} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return disk, part
}

func TestEjectByMountPointUnmountsThenPowersOff(t *testing.T) {
	disk, part := fakeDev(t)
	mnt := t.TempDir()
	h := &fakeHost{}
	install(t, h, mountLine("/", "/dev/sda1")+mountLine(mnt, part))

	var out bytes.Buffer
	if err := Eject(&out, mnt); err != nil {
		t.Fatalf("Eject: %v\n%s", err, out.String())
	}
	want := []string{
		"/usr/bin/sync",
		"/usr/bin/udisksctl unmount -b " + part,
		"/usr/bin/udisksctl power-off -b " + disk,
	}
	if strings.Join(h.ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(h.ran, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range []string{"clock not stamped", "unmounted.", "powered off. Safe to unplug."} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}
}

func TestEjectWithoutUdisksPrintsTheCommandsAndFails(t *testing.T) {
	disk, part := fakeDev(t)
	h := &fakeHost{missing: map[string]bool{"udisksctl": true, "sync": true}}
	mnt := t.TempDir()
	install(t, h, mountLine(mnt, part))

	var out bytes.Buffer
	err := ejectVolume(&out, mnt)
	if err == nil || !strings.Contains(err.Error(), "udisksctl not found") {
		t.Fatalf("err = %v, want udisksctl not found", err)
	}
	if len(h.ran) != 0 {
		t.Errorf("ran %v with neither program installed", h.ran)
	}
	for _, s := range []string{"udisksctl unmount -b " + part, "udisksctl power-off -b " + disk} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}
}

func TestEjectUnmountErrors(t *testing.T) {
	_, part := fakeDev(t)
	for _, c := range []struct {
		name    string
		out     string
		wantErr bool
	}{
		{"already unmounted carries on", "Error unmounting: Not Mounted", false},
		{"busy stops", "Error unmounting: target is busy", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := &fakeHost{fail: map[string]fakeResult{"unmount": {c.out, errors.New("exit status 1")}}}
			mnt := t.TempDir()
			install(t, h, mountLine(mnt, part))
			var out bytes.Buffer
			err := ejectVolume(&out, mnt)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.wantErr {
				if !strings.Contains(err.Error(), "target is busy") {
					t.Errorf("err does not carry udisksctl's message: %v", err)
				}
				if len(h.ran) != 2 { // sync, unmount — no power-off
					t.Errorf("ran %v; a failed unmount must not power off", h.ran)
				}
			}
		})
	}
}

func TestEjectPowerOffFailureIsStillSafe(t *testing.T) {
	disk, part := fakeDev(t)
	h := &fakeHost{fail: map[string]fakeResult{
		"power-off": {"not allowed", errors.New("exit status 1")},
		"sync":      {"", errors.New("exit status 2")},
	}}
	mnt := t.TempDir()
	install(t, h, mountLine(mnt, part))
	var out bytes.Buffer
	if err := ejectVolume(&out, mnt); err != nil {
		t.Fatalf("a failed power-off after a good unmount is not an error: %v", err)
	}
	for _, s := range []string{"warning: sync failed", disk + ": could not power off", "not allowed", "safe to unplug"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}
}

func TestResolveDevice(t *testing.T) {
	mnt := t.TempDir()
	unmounted := t.TempDir()
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	install(t, &fakeHost{}, "short line\n"+
		"1 2 3 4 /no/separator a b c d e\n"+
		mountLine(mnt, "/dev/sdc1"))

	if got, err := resolveDevice("/dev/sdz9"); err != nil || got != "/dev/sdz9" {
		t.Errorf("device node: %q, %v", got, err)
	}
	if got, err := resolveDevice(mnt); err != nil || got != "/dev/sdc1" {
		t.Errorf("mount point: %q, %v", got, err)
	}
	if got, err := resolveDevice(mnt + "/"); err != nil || got != "/dev/sdc1" {
		t.Errorf("mount point with a trailing slash: %q, %v", got, err)
	}
	for _, c := range []struct{ target, want string }{
		{filepath.Join(unmounted, "missing"), "give a device node"},
		{file, "neither a device node nor a mount point"},
		{unmounted, "is not a mount point"},
	} {
		if _, err := resolveDevice(c.target); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("resolveDevice(%s) = %v, want %q", c.target, err, c.want)
		}
	}
}

func TestDeviceForMountUnescapesAndSurvivesNoTable(t *testing.T) {
	install(t, &fakeHost{}, mountLine(`/media/u/IPOD\040NANO`, `/dev/sd\142\061`))
	if got := deviceForMount("/media/u/IPOD NANO"); got != "/dev/sdb1" {
		t.Errorf("deviceForMount = %q, want /dev/sdb1", got)
	}
	mountInfoPath = filepath.Join(t.TempDir(), "absent")
	if got := deviceForMount("/"); got != "" {
		t.Errorf("no mount table: %q, want \"\"", got)
	}
}

func TestUnescapeMountPath(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:         `plain`,
		`a\040b`:        `a b`,
		`tab\011end`:    "tab\tend",
		`back\134slash`: `back\slash`,
		`short\04`:      `short\04`, // not three octal digits: left alone
		`not\8octal`:    `not\8octal`,
		`trailing\`:     `trailing\`,
	} {
		if got := unescapeMountPath(in); got != want {
			t.Errorf("unescapeMountPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParentDisk(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"sdb", "nvme0n1", "mmcblk0"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for in, want := range map[string]string{
		"sdb2":      "sdb",
		"sdb":       "sdb",     // already whole: no trailing digits
		"nvme0n1p1": "nvme0n1", // the p belongs to the partition
		"mmcblk0p3": "mmcblk0",
		"nvme0n1":   "nvme0n1", // "nvme0n" does not exist: it was the disk
		"sdq1":      "sdq1",    // parent node absent: keep what we were given
		"123":       "123",     // all digits: nothing to strip to
	} {
		if got := parentDisk(filepath.Join(dir, in)); got != filepath.Join(dir, want) {
			t.Errorf("parentDisk(%s) = %s, want %s", in, filepath.Base(got), want)
		}
	}
}
