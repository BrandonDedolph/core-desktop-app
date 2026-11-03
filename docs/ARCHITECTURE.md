# How it is put together

## Layout

```
cmd/core/         the CLI main (just calls cli.Root().Execute())
cmd/core-app/     the desktop app main: flags, --screenshot, app.Main
internal/
  cli/            cobra command tree (one file per subcommand)
  app/            core-app: State (the model), Runner (one job at a
                  time, events over a channel), Backend (an interface
                  with the real wiring and a fake), the Gio layout, the
                  Linen and Onyx palettes, and the headless snapshot
                  renderer.
                  State is mutated only by the UI goroutine; jobs say
                  what happened by sending Events
  doctor/         the read-only device + volume checks, shared by
                  `core doctor` and core-app's Summary tab, so the two
                  cannot answer the same question differently
  eject/          flush + dismount + eject, per OS, shared by
                  `core eject` and core-app's Eject button
  firmware/       .ipod transport format (checksum, codec) + the
                  firmware-partition directory entry — scaffolding, see
                  the file comment in imageheader.go
  fwpart/         partition model: preamble, directory, OSOS entry,
                  capacity, write set, read-back verify. Reads and plans
                  only — nothing here writes. doc.go carries the
                  write-path SAFETY CHECKLIST.
  flasher/        the write sequence: parse the image, gate the
                  hardware, plan, print, confirm, elevate, back up,
                  write, verify. Every OS call is a field of Deps, so
                  the whole thing runs against an in-memory device
  disk/           the ONLY package that opens a raw block device:
                  enumerate (Windows IOCTLs / Linux sysfs / macOS
                  diskutil), open with aligned bounce-buffer I/O,
                  lock/dismount, elevation, and iPod identification
  organizer/      the source tree's names from its tags: Scan (a plan of
                  moves plus the files a human has to decide about), Apply
                  (journal first, fsynced, then the renames) and Undo (the
                  journal backwards, verifying size and mtime). Writes only
                  under the root it was given
  librarian/      the "things to fix" list and the one job that fixes it:
                  Inspect (one organizer.Scan plus a pass for front covers ->
                  Report), Candidates (the network step, on its own so it can
                  be cancelled), Fix (covers into the files, then the moves,
                  then the sidecars — one journal) and Undo
  installer/      the install sequence with no command line in it:
                  classify -> refuse a device already running Core ->
                  Apple's firmware to apple-<serial>-<date>.bin -> the
                  flasher -> CORECFG.DAT, CORELOG.BIN, COREOTG.DAT,
                  Music\ and the five On-The-Go slots on the volume ->
                  read the device back. `core install` and
                  core-app's Install button are both callers
  ghrelease/      GitHub Releases: latest / by tag, the firmware asset,
                  and a checksum-verified download cache under
                  <user cache dir>/core/<tag>/. stdlib net/http only
  artfetch/       cover-art lookup: the iTunes Search API, MusicBrainz +
                  the Cover Art Archive (mandatory User-Agent, 1 req/s,
                  injectable clock), the match score --yes acts on, and
                  the image + negative cache under
                  <user cache dir>/core/art. stdlib net/http only
  player/         the album sheet's preview listen: one oto context per
                  process (44.1 kHz, stereo, int16), FLAC and MP3 decoded
                  on their own goroutine, resampled, mono doubled, 24-bit
                  dithered to 16. -tags noaudio swaps the device for a stub
  syncer/         `core sync` and the window's Sync: the plan (new, changed,
                  renamed, covers, playlists, index) and the copy, with
                  CORELIB.IDX written last so a failed copy leaves the
                  device consistent
  library/        the source tree as the firmware sees it: ScanTree and
                  the locator name hash, byte for byte the firmware's
                  core/library/names.c
  cidx/           CORELIB.IDX, the index the firmware loads in one read:
                  encode and decode, the CRC header (v1 still read, never
                  written)
  coreart/        folder.art / folder.thm, the RGB565 sidecars, each
                  rendered from the source picture, never one from the
                  other
  devicefs/       CORECFG.DAT, CORELOG.BIN, COREOTG.DAT and the On-The-Go
                  slots: byte-identical to the firmware's Python oracles,
                  plus the clock stamp a sync leaves for the next boot
  flac/           a small, total FLAC metadata reader, and the PICTURE
                  and tag writers `core fix`, `core add` and Edit… use
  id3/            the ID3v2 reader for MP3s, agreeing with the firmware's
                  id3_meta.c on what it does not read
  selfupdate/     `core self-update` and the window's Update the app:
                  download, checksum, stage both files, swap, restart
  testutil/       the parity-skip helper: under CORE_REQUIRE_PARITY=1 a
                  skip is a failure
  version/        build version info, stamped via -ldflags at release time
```


## The write path, and what it checks

**Flashing goes through `core flash` as of S7**, and `ipodpatcher`
stays documented as the fallback for two more releases (`ipodpatcher <n>
-wf core.ipod`, read back with `-rfb` and compared — which is also how
the first `core flash` on real hardware is independently confirmed).
`doflash.cmd` stays in the bring-up folder. Our firmware is
written **as the OSOS image itself** and boots directly from the
firmware partition: there is no chainloader to install and no bootloader
stage to place, so the write is one image with the Apple preamble, the
partition table and every other directory entry untouched.

**Device detection is `internal/disk`, not USB descriptors.** The old
`internal/ipod` package (a Generation/Mode/Device type system with a
`detect()` that returned an error on every platform) is gone. An iPod is
now identified from the disk itself, three independent checks, all
required: MBR partition 0 is type 0x00 with a real extent and partition
1 is FAT32 (0x0B/0x0C); `fwpart.CheckPreamble` passes on partition 0's
first 512 bytes (the Apple banner plus the `]ih[` marker at 0x100); and
the vendor/product strings the OS already has say Apple or iPod. Reading
the real USB VID/PID portably would mean libusb, which means cgo, and
the inquiry strings name the block device we are about to open, which a
descriptor does not.

The **logical sector size is read from the OS and then cross-checked**:
partition 0's start LBA times that size must land on the preamble, and
if it does not, 512 and 2048 are tried and `core info` says which one
matched. Every byte offset in the flash path is an LBA times this
number, so the USB bridge reporting 2048 where the driver says 512 is
the one way to be 96 KB wrong while every other number looks right.

Only the **5.5G 80 GB** is `Tested` (disk size within 74–80 GiB; the
real device is 80,026,361,856 B = 74.53 GiB, which is why the lower
bound is 74 and not 76). An untested iPod can still be read, inspected
and backed up; `core flash` refuses it without `--untested-hardware`,
and says so in a sentence that names the one model that has booted this
firmware.

**Raw disk access needs privilege.** Enumeration does not: on Windows a
zero-access handle to `\\.\PhysicalDriveN` answers the metadata
IOCTLs, so `core info --all-disks` prints the full list from an ordinary
console. Reading sector 0 does. When the open is refused, the error
prints the exact elevated command line — a `sudo …` line on Linux/macOS,
a `Start-Process … -Verb RunAs` line on Windows, where UAC cannot
elevate a running console.

**There is no signing or verification code.** There is no release key
and no embedded public key. `core update` verifies the `.ipod`
transport checksum, which is an integrity check and not a signature; any
flag that implies signature verification stays out of the help text
until the code exists. `GITHUB_TOKEN` is honoured if it is set, only to
get out of the unauthenticated rate limit — the releases being read are
public.

**The write path has a safety checklist, and the code follows it.**
The checklist lives in `internal/fwpart/doc.go`: prove the target is an
iPod three ways, back up the firmware partition first, verify by
read-back, and support `--dry-run` plus a typed confirmation.
`internal/fwpart` (S5) is the read and planning half — `PlanWrite`
returns the two byte ranges (the zero-padded body at `devOffset +
0x800`, then the one sector holding the re-encoded directory entry) and
`VerifyWritten` proves they landed. `internal/flasher` (S7) performs
them, in a fixed order with no flag that skips the backup or the
read-back, and every OS call it makes is a field of `Deps` so the whole
sequence — including a write that fails between the body and the
directory — is exercised against `disk.NewMemDevice`. The global
`--device` flag disambiguates when several iPods are connected.

**Elevation and the relaunch loop guard.** A write asks for elevation
whether or not the read worked (`disk.DecideElevation`): on Windows a
non-elevated handle can sometimes read a physical drive while every
write to it fails, and finding that out halfway through a flash is the
failure this exists to prevent. On Windows `core flash` starts an
elevated copy of itself with `--yes --no-relaunch --elevated-log <file>`
and prints the child's log, exiting with the child's code; the child
always carries `--no-relaunch`, so a child that somehow believes it is
not elevated refuses with the command line instead of spawning another.
The typed confirmation happens in the parent, where there is a console
to type into. On Linux/macOS there is no relaunch at all — `sudo`
re-runs in place, and the exact line is printed.


## Design notes

- **Cobra** for the command tree. We considered urfave/cli too — cobra
  won on better help-text rendering and richer flag types.
- **Minimal deps, no cgo.** cobra + pflag + mousetrap (cobra's
  transitive Windows-mode dep) is the whole dependency list today. The
  only addition is `golang.org/x/image` (Lanczos scaling for the art
  sidecars). `golang.org/x/sys` was budgeted for the raw-disk ioctls in
  S6 and turned out not to be needed there: the stdlib `syscall` package
  has `DeviceIoControl`, `FlushFileBuffers`, `GetTokenInformation` and
  `EscapeArg` on Windows and `SYS_IOCTL` on Linux, and `ShellExecuteExW`
  comes from a `syscall.NewLazyDLL`. It is a direct dependency since the
  app pass for two Windows calls only: the dark-mode registry value
  (`windows/registry`) and the music volume's free space
  (`GetDiskFreeSpaceEx`). It was already in the build as Gio's. Anything
  more goes through a code review. No cgo means every target still cross-builds from one machine.
- **The Python tools stay** (in core-firmware). `tools/build_index.py`, `coreart.py`,
  `make_config.py` and `make_log.py` remain as reference implementations
  and parity oracles for the Go code that replaces them in the daily
  flow.
- **Output files are written atomically.** `writeFileAtomic` in
  `internal/cli/firmware.go` writes to a temp file, fsyncs, verifies by
  read-back, and renames. `--force` must never destroy the previous
  artifact before the replacement is complete on disk — `make ipod`
  (in core-firmware) passes `--force` on every build, and a truncated `core.ipod` still
  parses as a valid header.

