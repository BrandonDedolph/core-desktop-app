// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestPartitionLabels(t *testing.T) {
	for _, c := range []struct {
		typ  byte
		want string
	}{
		{TypeEmpty, "Empty (0x00)"},
		{TypeFAT32CHS, "W95 FAT32 (0x0b)"},
		{TypeFAT32LBA, "W95 FAT32 LBA (0x0c)"},
		{0x06, "FAT16 (0x06)"},
		{0xAF, "HFS/HFS+ (0xaf)"},
		{0xEE, "GPT protective (0xee)"},
		{0x83, "type 0x83"},
	} {
		if got := (Partition{Type: c.typ}).TypeName(); got != c.want {
			t.Errorf("TypeName(%#x) = %q, want %q", c.typ, got, c.want)
		}
	}
	if got := (Partition{StartLBA: 63}).EndLBA(); got != 63 {
		t.Errorf("EndLBA of an empty extent = %d, want its start", got)
	}
	if got := (Partition{StartLBA: 63, NumSectors: 10}).EndLBA(); got != 72 {
		t.Errorf("EndLBA = %d, want 72 (inclusive)", got)
	}
}

func TestBuildMBRIgnoresAFifthSlot(t *testing.T) {
	five := make([]Partition, 5)
	five[0] = Partition{Bootable: true, Type: TypeFAT32LBA, StartLBA: 1, NumSectors: 2}
	five[4] = Partition{Type: 0x83, StartLBA: 99, NumSectors: 1}
	parts, err := ParseMBR(BuildMBR(five))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 4 || !parts[0].Bootable || parts[0].Type != TypeFAT32LBA {
		t.Errorf("round trip = %+v", parts)
	}
	for _, p := range parts[1:] {
		if p.Used() {
			t.Errorf("slot %d used: %+v (a fifth entry must be dropped, not wrapped)", p.Index, p)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{
		0:           "0 B",
		-1:          "0 B",
		1 << 20:     "1048576 B (1.0 MiB)",
		80026361856: "80.0 GB / 74.53 GiB",
	} {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestNeedsSudoError(t *testing.T) {
	err := error(&NeedsSudoError{Command: "sudo /usr/bin/core flash"})
	if !errors.Is(fmt.Errorf("wrapped: %w", err), ErrNeedsSudo) {
		t.Error("errors.Is(NeedsSudoError, ErrNeedsSudo) is false")
	}
	if errors.Is(err, ErrNoDevice) {
		t.Error("NeedsSudoError matches an unrelated sentinel")
	}
	if !strings.HasSuffix(err.Error(), "\n  sudo /usr/bin/core flash") {
		t.Errorf("Error() = %q, want the line to paste on its own", err.Error())
	}
}

func TestSudoCommandNamesThisProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows line is a PowerShell relaunch, covered in elevate_test.go")
	}
	got := SudoCommand([]string{"flash", "--device", "/dev/sdb"})
	if got == "" || !strings.HasSuffix(got, " flash --device /dev/sdb") {
		t.Errorf("SudoCommand = %q", got)
	}
	if !strings.HasPrefix(ElevatedLogFlag, "--") || !strings.Contains(elevatedLogPath(), "core-elevated-") {
		t.Errorf("elevated log path %q", elevatedLogPath())
	}
}

func TestMemStatsFaults(t *testing.T) {
	h, st, err := NewMemDevice(make([]byte, 4096), 512, true)
	if err != nil {
		t.Fatal(err)
	}
	if h.SectorSize() != 512 {
		t.Errorf("SectorSize %d", h.SectorSize())
	}
	st.FailWriteAt(1024)
	if _, err := h.WriteAt([]byte{1}, 0); err != nil {
		t.Errorf("write below the fault offset failed: %v", err)
	}
	if _, err := h.WriteAt([]byte{1}, 1500); err == nil {
		t.Error("write past the fault offset succeeded")
	}
	boom := errors.New("unplugged")
	st.SetReadErr(boom)
	if _, err := h.ReadAt(make([]byte, 10), 0); !errors.Is(err, boom) {
		t.Errorf("ReadAt after SetReadErr = %v", err)
	}
}
