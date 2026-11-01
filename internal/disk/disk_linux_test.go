// SPDX-License-Identifier: Apache-2.0

//go:build linux

package disk

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSys builds a /sys/block and a mountinfo in a temp dir and points the
// package at them for the rest of the test.
//
//	sdb   USB, removable, 2048-byte sectors, partitions sdb1 (mounted) and
//	      sdb2; the entry is a symlink into a devices/…/usb1/… path, which
//	      is how the kernel spells "this hangs off USB"
//	sda   internal, no logical_block_size (falls back to 512), no partitions
//	loop0 no device/ link: filtered out
func fakeSys(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	block := filepath.Join(root, "block")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	usb := filepath.Join("devices", "pci0000:00", "usb1", "1-2", "block", "sdb")
	write(filepath.Join(usb, "size"), "156301488\n")
	write(filepath.Join(usb, "removable"), "1\n")
	write(filepath.Join(usb, "queue", "logical_block_size"), "2048\n")
	write(filepath.Join(usb, "device", "vendor"), "Apple   \n")
	write(filepath.Join(usb, "device", "model"), "iPod            \n")
	write(filepath.Join(usb, "device", "serial"), "000A27001\n")
	write(filepath.Join(usb, "sdb1", "partition"), "1\n")
	write(filepath.Join(usb, "sdb1", "dev"), "8:17\n")
	write(filepath.Join(usb, "sdb2", "partition"), "2\n")
	write(filepath.Join(usb, "sdb2", "dev"), "8:18\n")
	write(filepath.Join(usb, "holders", ".keep"), "") // not a partition
	if err := os.MkdirAll(block, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, usb), filepath.Join(block, "sdb")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("block", "sda", "size"), "1000\n")
	write(filepath.Join("block", "sda", "removable"), "0\n")
	write(filepath.Join("block", "sda", "device", "model"), "")
	write(filepath.Join("block", "loop0", "size"), "8\n")
	write("mountinfo",
		"bad\n"+
			"22 1 8:1 / / rw - ext4 /dev/sda1 rw\n"+
			`40 22 8:17 / /media/u/IPOD\040DISK rw - vfat /dev/sdb1 rw`+"\n")

	oldBlock, oldMI := sysBlock, mountInfoPath
	t.Cleanup(func() { sysBlock, mountInfoPath = oldBlock, oldMI })
	sysBlock, mountInfoPath = block, filepath.Join(root, "mountinfo")
	return root
}

func TestListReadsSysfs(t *testing.T) {
	fakeSys(t)
	disks, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 2 {
		t.Fatalf("got %d disks, want sda and sdb (loop0 filtered): %+v", len(disks), disks)
	}
	byPath := map[string]Disk{}
	for _, d := range disks {
		byPath[d.Path] = d
	}
	b := byPath["/dev/sdb"]
	if b.SizeBytes != 156301488*512 {
		t.Errorf("sdb size %d: /sys size is in 512-byte units whatever the sector size", b.SizeBytes)
	}
	if b.SectorSize != 2048 || !b.Removable || !b.USB || b.Vendor != "Apple" || b.Model != "iPod" || b.Serial != "000A27001" {
		t.Errorf("sdb = %+v", b)
	}
	if strings.Join(b.Volumes, ",") != "/dev/sdb1,/dev/sdb2" {
		t.Errorf("sdb volumes %v", b.Volumes)
	}
	if strings.Join(b.MountPoints, ",") != "/media/u/IPOD DISK" {
		t.Errorf("sdb mount points %v (mountinfo is unescaped)", b.MountPoints)
	}
	if want := "/dev/sdb  Apple iPod  80.0 GB / 74.53 GiB  [/dev/sdb1 /dev/sdb2]"; b.Describe() != want {
		t.Errorf("Describe = %q, want %q", b.Describe(), want)
	}
	a := byPath["/dev/sda"]
	if a.SectorSize != 512 || a.Removable || a.USB || len(a.Volumes) != 0 {
		t.Errorf("sda = %+v", a)
	}
	if want := "/dev/sda  (no model string)  512000 B (0.5 MiB)"; a.Describe() != want {
		t.Errorf("Describe = %q, want %q", a.Describe(), want)
	}
}

func TestListWithoutSysfs(t *testing.T) {
	fakeSys(t)
	sysBlock = filepath.Join(t.TempDir(), "absent")
	mountInfoPath = sysBlock
	if _, err := List(); err == nil {
		t.Error("List with no /sys/block did not fail")
	}
	if m := readMountInfo(); m != nil {
		t.Errorf("readMountInfo with no file = %v, want nil", m)
	}
	if p := partitionsOf("sdb"); p != nil {
		t.Errorf("partitionsOf a missing disk = %v", p)
	}
}

func TestReadSysUint(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"ok": " 42\n", "empty": "", "junk": "4k"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n, ok := readSysUint(filepath.Join(dir, "ok")); n != 42 || !ok {
		t.Errorf("ok: %d %v", n, ok)
	}
	for _, name := range []string{"empty", "junk", "missing"} {
		if _, ok := readSysUint(filepath.Join(dir, name)); ok {
			t.Errorf("%s parsed as a number", name)
		}
	}
}

func TestUnescapeMountPathLinux(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:       `plain`,
		`a\040b`:      `a b`,
		`nl\012`:      "nl\n",
		`back\134x`:   `back\x`,
		`bad\09x`:     `bad\09x`, // 9 is not octal
		`short\04`:    `short\04`,
		`over\777end`: `over\777end`, // 0777 does not fit in a byte
	} {
		if got := unescapeMountPath(in); got != want {
			t.Errorf("unescapeMountPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A regular file stands in for /dev/sdb: the ioctls fail on it (ENOTTY),
// which exercises exactly the sysfs and seek fallbacks a device whose
// driver does not answer them would take.
func TestOpenRegularFileUsesTheFallbacks(t *testing.T) {
	root := fakeSys(t)
	dev := filepath.Join(root, "sdb") // basename sdb: sysfs says 2048
	data := bytes.Repeat([]byte{0xA5}, 4*2048+100)
	if err := os.WriteFile(dev, data, 0o644); err != nil {
		t.Fatal(err)
	}

	h, err := Open(dev, true)
	if err != nil {
		t.Fatal(err)
	}
	if h.SectorSize() != 2048 {
		t.Errorf("sector size %d, want sysfs's 2048", h.SectorSize())
	}
	if h.Size() != 4*2048 {
		t.Errorf("size %d, want the seek size rounded down to whole sectors", h.Size())
	}
	if _, err := h.WriteAt([]byte("core"), 3000); err != nil {
		t.Fatal(err)
	}
	if err := h.Flush(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 6)
	if _, err := h.ReadAt(got, 2999); err != nil {
		t.Fatal(err)
	}
	if string(got[1:5]) != "core" || got[0] != 0xA5 || got[5] != 0xA5 {
		t.Errorf("read back % x", got)
	}

	// sdb1 (8:17) is mounted in the fake mountinfo, so Lock must refuse.
	if err := h.Lock(); !errors.Is(err, ErrMounted) || !strings.Contains(err.Error(), "/dev/sdb1 on /media/u/IPOD DISK") {
		t.Errorf("Lock = %v, want ErrMounted naming the mount", err)
	}
	if err := h.Unlock(); err != nil {
		t.Errorf("Unlock = %v", err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestOpenRegularFileReadOnlyDefaults(t *testing.T) {
	fakeSys(t)
	dir := t.TempDir()
	dev := filepath.Join(dir, "sdz") // not in sysfs: 512
	if err := os.WriteFile(dev, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Open(dev, false)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.SectorSize() != 512 || h.Size() != 1024 {
		t.Errorf("ss %d size %d", h.SectorSize(), h.Size())
	}
	if err := h.Lock(); err != nil {
		t.Errorf("Lock on a disk with no partitions = %v", err)
	}
	if err := h.Flush(); err != nil {
		t.Errorf("Flush on a read-only handle = %v", err)
	}
	if _, err := h.WriteAt([]byte{1}, 0); !errors.Is(err, ErrReadOnly) {
		t.Errorf("WriteAt read-only = %v", err)
	}
}

func TestOpenFailures(t *testing.T) {
	fakeSys(t)
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "missing"), false); err == nil {
		t.Error("Open of a missing node succeeded")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(empty, false); err == nil || !strings.Contains(err.Error(), "cannot determine the size") {
		t.Errorf("Open of a zero-length node = %v", err)
	}
	// sysfs claiming a sector size that is not a power of two is refused
	// by the aligned layer, and the raw handle is closed behind it.
	odd := filepath.Join(sysBlock, "sdo", "queue", "logical_block_size")
	if err := os.MkdirAll(filepath.Dir(odd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(odd, []byte("1000"), 0o644); err != nil {
		t.Fatal(err)
	}
	sdo := filepath.Join(dir, "sdo")
	if err := os.WriteFile(sdo, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(sdo, false); err == nil || !strings.Contains(err.Error(), "power of two") {
		t.Errorf("Open with a 1000-byte sector = %v", err)
	}
}
