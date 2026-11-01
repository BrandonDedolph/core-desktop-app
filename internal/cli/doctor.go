package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/disk"
	"github.com/BrandonDedolph/core-desktop-app/internal/doctor"
	"github.com/spf13/cobra"
)

// `core doctor` is the command for "it boots but something is wrong".
// It reads and never writes — not one byte, not even the two files the
// firmware is allowed to overwrite in place — and it answers, in order,
// the questions whose answers the device itself cannot tell anyone:
//
//   - is this an iPod this firmware has run on?
//   - does the firmware partition still parse, and does the installed
//     image still sum to what its directory row says?
//   - which build is installed?
//   - are the four things the firmware looks for on the FAT volume
//     there, valid, and within the caps past which it silently drops?
//
// The checks themselves live in internal/doctor — core-app's Device
// card asks the same questions and must get the same answers. This file
// is the device selector, the column widths and the exit code.

func newDoctorCmd() *cobra.Command {
	var (
		volume string
		boot   bool
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Read-only health check of a connected iPod and its library volume",
		Long: `Walks everything the firmware depends on and prints one OK / WARN /
FAIL line per check: the device and whether it is hardware this
firmware has actually run on, the firmware partition's image directory,
the installed image's checksum and version, and then the FAT volume —
Music/, CORELIB.IDX (header, CRC, record count and the three caps past
which the device silently drops), CORECFG.DAT, CORELOG.BIN and the
playlist folder — and last, whether the iPod has booted the image on it:
the newest boot in CORELOG.BIN — the build it names, on firmware newer
than v0.2.2 — held against the image on the iPod and the note the last
flash left in COREFLSH.TXT.

--boot prints only that last section and exits 1 when the iPod has an
image it has not started yet. The bench check after a flash is: unplug
the iPod, let it start, plug it back in, then run "core doctor --boot".

Nothing is written, opened for writing, or repaired. The exit status is
1 if any check FAILs and 0 otherwise, so this is usable as a gate.

--volume points at the mounted FAT volume (the drive letter or mount
point). Without it the volume is taken from the device the OS reports;
with it, the device half is skipped when no iPod is attached, which is
how the library half can be checked from a copy on disk.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, strings.TrimSpace(volume), boot)
		},
	}
	cmd.Flags().BoolVar(&boot, "boot", false,
		"Print only the boot evidence: whether the iPod has started the image on it")
	cmd.Flags().StringVar(&volume, "volume", "",
		"The mounted FAT volume to check (default: the one the connected iPod reports)")
	return cmd
}

func runDoctor(cmd *cobra.Command, volume string, bootOnly bool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "core doctor — read-only\n")
	if bootOnly {
		return runBootCheck(cmd, volume)
	}

	// A named --volume turns the device half's misses into SKIPs:
	// checking a library on a volume with the iPod unplugged is a thing
	// worth being able to do.
	miss := doctor.Fail
	if volume != "" {
		miss = doctor.Skip
	}
	dev := doctor.CheckDevice(doctor.DeviceDeps{
		Select:  func() (disk.IPod, error) { return selectDevice(cmd) },
		Open:    func(path string) (disk.Handle, error) { return openRO(path) },
		Missing: miss,
	})
	printSection(out, "device")
	printChecks(out, dev.Checks)

	if volume == "" {
		volume = doctor.VolumeOf(dev.Pod, dev.Found)
	}
	vol := doctor.CheckVolume(volume)
	title := "volume"
	if volume != "" {
		title = "volume  " + volume
	}
	printSection(out, title)
	printChecks(out, vol.Checks)

	boot := doctor.CheckBoot(volume, dev, vol, time.Now())
	printSection(out, "boot")
	printChecks(out, boot.Checks)

	all := append(append(append([]doctor.Check{}, dev.Checks...), vol.Checks...), boot.Checks...)
	return summarize(out, "core doctor", all)
}

// runBootCheck is `core doctor --boot`: the device and volume halves are
// still read — the boot section compares against the image on the device
// and the index on the volume — but only the boot lines are printed, and
// only they decide the exit status. A device that cannot be read is not a
// failure here; the boot section says what it could not compare.
func runBootCheck(cmd *cobra.Command, volume string) error {
	out := cmd.OutOrStdout()
	dev := doctor.CheckDevice(doctor.DeviceDeps{
		Select:  func() (disk.IPod, error) { return selectDevice(cmd) },
		Open:    func(path string) (disk.Handle, error) { return openRO(path) },
		Missing: doctor.Skip,
	})
	if volume == "" {
		volume = doctor.VolumeOf(dev.Pod, dev.Found)
	}
	vol := doctor.CheckVolume(volume)
	boot := doctor.CheckBoot(volume, dev, vol, time.Now())
	title := "boot"
	if volume != "" {
		title = "boot  " + volume
	}
	printSection(out, title)
	printChecks(out, boot.Checks)
	if !dev.Image.Known && dev.Err != "" {
		fmt.Fprintf(out, "  (the firmware partition was not read: %s)\n", dev.Err)
	}
	return summarize(out, "core doctor --boot", boot.Checks)
}

func summarize(out io.Writer, name string, all []doctor.Check) error {
	counts := doctor.Counts(all)
	fmt.Fprintf(out, "\nsummary: %d OK, %d WARN, %d FAIL",
		counts[doctor.OK], counts[doctor.Warn], counts[doctor.Fail])
	if n := counts[doctor.Skip]; n > 0 {
		fmt.Fprintf(out, ", %d skipped", n)
	}
	fmt.Fprintln(out)
	if n := counts[doctor.Fail]; n > 0 {
		return fmt.Errorf("%s: %d check(s) failed", name, n)
	}
	return nil
}

// openRO opens a device read-only with the CLI's decoration, so a
// permission failure still carries the elevated command line to run.
func openRO(path string) (disk.Handle, error) {
	h, err := disk.Open(path, false)
	if err != nil {
		return nil, decorateDeviceError(fmt.Errorf("open %s for reading: %w", path, err))
	}
	return h, nil
}

func printSection(out io.Writer, title string) { fmt.Fprintf(out, "\n%s\n", title) }

func printChecks(out io.Writer, checks []doctor.Check) {
	for _, c := range checks {
		fmt.Fprintf(out, "  %-4s  %-12s %s\n", c.State, c.Name, c.Text)
	}
}
