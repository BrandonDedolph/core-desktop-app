# The `core` app

This is the guide to the host side: the `core` command line and the `core-app` window. The guide
to the iPod itself — the controls, browsing, Now Playing, Settings, themes, battery and recovery —
is the firmware's
[user guide](https://github.com/BrandonDedolph/core-firmware/blob/main/docs/USER_GUIDE.md).

`core` is one program for your computer that does the host side: it puts the music on the iPod in
the layout the firmware reads, bakes the album art, writes the index, and flashes firmware. One
file, no installer.

Download the one for your machine from this repository's
[releases page](https://github.com/BrandonDedolph/core-desktop-app/releases) — every release carries
the app and the command line. The firmware itself (`core.ipod`) is released from
[core-firmware](https://github.com/BrandonDedolph/core-firmware/releases), and `core update` fetches
it from there:

| File | For |
|---|---|
| `core-windows-amd64.exe` | Windows |
| `core-darwin-arm64` | macOS, Apple silicon |
| `core-darwin-amd64` | macOS, Intel |
| `core-linux-amd64` | Linux, x86-64 |
| `core-linux-arm64` | Linux, ARM |

To build it yourself instead, from a checkout of this repository: `go build -o core ./cmd/core`
(see the [README](../README.md)). On macOS and Linux, `chmod +x` the downloaded file.
The examples below call it `core`.

**The window.** `core-app` is the same thing as a desktop application. It ships beside the command
line as `core-app-windows-amd64.exe`, `core-app-darwin-arm64`, `core-app-darwin-amd64`,
`core-app-linux-amd64` and `core-app-linux-arm64`. On Windows it asks for Administrator when it
opens (one UAC prompt), because reading the iPod's raw disk needs it; that is also what lets it
flash from the window with no second prompt. Keep `core` in the same folder anyway: a window that
is somehow not elevated runs `core.exe` under Administrator for the flash.

It finds the iPod by itself — there is nothing to press. Plug it in, in disk mode, and the window
shows it within two seconds.

- **The header** says which iPod it is (the name you gave it, and its model), the firmware on it,
  whether a newer Core is out, and how much space is free. Its one button says what it will do:
  *Sync 7 albums*, *Update to v0.2.2*, *Install Core*, or *Eject*. The window asks GitHub for the
  latest release once when it first finds the iPod, so an update is offered without you asking.
- **Music** is your music folder as covers, with the ones that are new or changed marked.
- **Fix** is where the music folder is set (the field and *Browse…*), and what is wrong with
  it — filenames that do not match their tags, folders to organize, missing covers — with *Fix
  all* and *Undo*. Clicking the folder at the bottom of the window brings you here. Two more
  checks live here:
  - **Low-resolution cover art** lists albums whose cover is smaller than 600 px on its short side,
    with the cover they have beside a larger one: the cover.jpg already in the album folder when it
    is bigger, otherwise one found on iTunes (1200 px) or the Cover Art Archive. *Use this cover* or
    *Skip* each one; untick the row to leave them all. Only a larger cover is ever used, every track
    of the album gets it, the next sync copies those tracks again, and *Undo* puts the old cover
    back. (A cover file in the folder is also used, without asking the internet, for an album that
    has no cover at all.) The 600 is the *Small covers* picker in Settings… (Off, 300, 600 or
    1000 px), or `core config set min_art_px 1000` from the command line.
  - **Duplicate albums** are two album folders with the same songs in them, so the iPod has them
    twice — say an album that is also Disc 2 of its deluxe edition. Each pair is shown side by side
    with the reason; *Keep this one* moves the other folder to a `.core-trash` folder next to your
    music folder (not the Recycle Bin), and *Undo* moves it back. If the other album has songs the
    one you keep does not, the button warns and asks a second time. Empty `.core-trash` yourself
    when you are sure. *Fix all* never touches duplicates.
- **Summary** is this iPod in five rows — Name (*Rename*), Firmware (*Update* / *Check*), Storage,
  Hardware, Backups (*Show folder*, *Back up now*) — and an *Advanced* section with the disk path,
  the partition entry, the device files and *Flash this image* for an image file of your own.
- **The log** is behind **···** → *Show the log* (or Ctrl+L): a drawer with everything the window
  did, with *Copy* and *Clear*. When a job fails, its status line in the header says so and opens
  the log when you click it.
- **···** holds the rest: back up the firmware, flash an image file, read the iPod again, check for
  updates, show or copy the log, open the music folder, *Settings…* and About.
  *Settings…* is everything the app remembers, saved the moment you change it: the music folder,
  the four *Fix all* ticks (the Fix tab's own), the small-cover line, the backup folder, *Use
  ReplayGain*, the theme (System, Linen, Onyx — System follows Windows' dark-mode setting), and
  whether the window keeps its size and reopens on the last tab. *Restore defaults* puts them
  back, keeping the music folder and your iPods' names. The hold to write, the backup before
  every flash and the read-back check are not settings.

Writing firmware — Install, Update, or an image of your own — is confirmed in one dialog that says
which iPod will be written: its name, model, serial, which disk on this PC and how much is
written. On the iPod Video 5.5G 80 GB, the hardware Core has been tested on, you confirm by
**holding the button for two seconds**. Any other iPod is refused by the window; the command line
can write it with `--untested-hardware`, if you mean to take that risk. *Sync and remove* lists the albums it would delete and says how many on its button.
After any write the window reads the iPod again, so the header shows the new version at once.

Click an album to open it: its cover, what the iPod has of it, and its tracklist. Click a track to
listen to it on the computer, and the album plays on from there; the playing track shows the
iPod's bouncing bars, and *Show folder on this PC* opens the folder. The player is the bottom left
of the window, whether the album is open or not: the album's cover, the bars, the track and its
artist, the time over a line you can click to jump, pause, next and ✕ (stop). A click on the cover
or the title opens the album at that track, and the album's tile in the grid carries the bars too.
Space plays and pauses, and Ctrl+→ / Ctrl+← go to the next and previous track (not while you are typing in a
field). The app plays at the iPod's loudness: a FLAC's ReplayGain tag is
applied the way the firmware applies it (never louder than its peak allows), so albums mastered loud
sit level with quiet ones — *Use ReplayGain* in Settings turns it off. The speaker and slider beside the buttons set the app's own volume — not Windows' — and
remember it; the mouse wheel over the player or Ctrl+↑ / Ctrl+↓ nudge it, and a click on the
speaker mutes. When the album ends the bar says so for a moment and goes away. A sync or a library fix
does not stop the music; a track a fix has renamed is skipped. *Sync N albums* (and *Preview sync* on the Summary tab) first shows what the sync would copy —
each album, how many of its tracks and their size, and the total — and copies nothing until you
press the sync button on it; when there is nothing to copy it says *Everything is on the iPod*.

<p align="center"><img src="screens/core_app.png" alt="the core desktop app, Linen" width="640"></p>
<p align="center"><img src="screens/core_app_onyx.png" alt="the core desktop app, Onyx" width="640"></p>
<p align="center"><img src="screens/core_app_album.png" alt="an album open: cover, facts and tracklist, with the player below" width="640"></p>
<p align="center"><img src="screens/core_app_fix.png" alt="the Fix tab: what is wrong with the music folder, and the preview of the fix" width="640"></p>
<p align="center"><img src="screens/core_app_ipod.png" alt="the Summary tab" width="640"></p>

**Connect.** Put the iPod in disk mode — Settings, Disk Mode on the device, or hold Select + Play
at power-on — plug it in, and ask what is there:

```
core info
```

That prints the disk, the logical sector size, the partition table and the firmware images, and
never writes. If it says no iPod, `core info --all-disks` lists every disk the OS can see and which
opens were refused. Reading a raw disk needs Administrator on Windows and root on Linux and macOS;
when an open is refused the error prints the exact elevated command to run.

**Music.**

```
core sync --src "C:\Users\you\Music" --dst D:\
core eject D:
```

`--dst` is the iPod's volume root, not its `Music` folder. The source is one folder per album named
`Album - Artist`, one FLAC or MP3 per track, and playlists, if you keep any, as `.m3u8` files in a
`Playlists/` folder beside the albums.

`sync` copies each track to `Music/Artist - Album/NN. Title.flac` (or `.mp3` — nothing is
converted, each file keeps its own format), writes `folder.art` and
`folder.thm` beside it from the file's embedded cover, rewrites the playlists to device paths under
`Music/Playlists/`, creates `CORECFG.DAT`, `CORELOG.BIN` and `COREOTG.DAT` in the volume root and
the five `Music/Playlists/On-The-Go N.m3u8` slots if they are missing (an existing one is never
reset, so your settings, your log and your saved lists survive), **sets the iPod's clock**, and
writes `Music/CORELIB.IDX` last — last on purpose, so the index never names files a failed copy did
not leave behind. A track already on
the device is skipped when its size matches and its timestamp is within two seconds; `--verify`
compares content instead.

```
core sync --src … --dst D:\ --dry-run       # print the whole plan, write nothing
core sync --src … --dst D:\ --prune --yes   # also remove what is on the device and not in the source
```

Nothing is deleted without `--prune --yes`.

**Settings.** The command line reads the same settings file as the window:

```
core config                          # every setting, and which are still the default
core config set min_art_px 1000      # covers under 1000 px count as low-resolution
core config set source "C:\Users\you\Music"
core config unset source             # back to the default
```

A flag always wins, then `$CORELIB_SRC`, then the file — so once `source` is set, `core sync --dst D:\`
needs no `--src`, and the command says where the folder came from before it starts.

**The clock.** `sync`, `install` and `eject` all write your computer's current time (and its time
zone) into `CORECFG.DAT`, and the iPod picks it up at its **next boot**. It cannot be told the time
while it is plugged in: on the cable it is Apple's disk mode answering, not this firmware. `eject`
stamps last, immediately before the volume goes away, which is why it is the one that matters —
unplug, the iPod reboots, and the clock is seconds old. Nothing else in the file is touched: the
stamp rewrites one of its two records and copies everything else across byte for byte. `core doctor`
prints the stamp and whether the device has taken it yet.

**Firmware.**

```
core update                      # the newest release: download, verify, flash
core flash core.ipod             # a particular image, e.g. one downloaded from a release
core backup                      # the whole firmware partition to a file
core doctor                      # read-only check of the device and its library
core doctor --boot               # did the iPod start the image the last flash wrote?
```

`update` reads the latest GitHub release, downloads the image, checks it against its own checksum,
and flashes it. Writing the firmware partition needs Administrator on Windows and root on macOS and
Linux. From an ordinary console the download and the check still happen, and then the exact
elevated command is printed: a `core flash` of the downloaded file, first with `--dry-run` so you
can read the plan, then with `--yes`. On Windows, paste it into a Command Prompt opened with "Run as
administrator"; on macOS and Linux, prefix it with `sudo`. `core update` and `core doctor` ship with
the v0.1.3 binaries.

Every flash backs the whole firmware partition up first, writes only this firmware's image and the
one directory row describing it — the Apple preamble, the partition table and Apple's other images
are never touched — and re-reads what it wrote to compare. `--dry-run` prints the plan and opens
nothing for writing.

**Did it boot?** The read-back proves the bytes, not the boot. To prove the boot: unplug the iPod
and let it start, plug it back in, and run `core doctor --boot` (or look at **Last boot** on the
app's Summary tab). Every flash leaves a small note, `COREFLSH.TXT`, on the music volume: which
image it wrote and where the iPod's own log (`CORELOG.BIN`) stood at that moment. The firmware adds
to that log every time it starts, so a start newer than the note is a start of the new image.
`--boot` prints the last start and its time, the build it ran, whether the library index loaded,
and whether that session ended cleanly (sleep, standby, disk mode) or just stopped (a reset, a
crash, a flat battery). It exits 1 while the iPod has an image it has not started yet — "flashed
v0.2.2, last boot ran v0.2.1 — the iPod has not restarted since". Firmware newer than v0.2.2 also
names its own build in the log at every start, and the check believes that line over the note: it
then compares the build the iPod said it ran with the image it holds. An image written by an older
`core` or another tool has no note, so with older firmware the build is reported as unknown (a WARN,
not a FAIL).

**What is verified.** The iPod Video 5.5G 80 GB is the only model this firmware has ever booted,
and the only one the app knows. Its flash path has been run on that device: the whole-partition
backup, the write, and the read-back all matched, and `ipodpatcher` read the same image back
afterwards. `core flash` refuses any other iPod unless you pass `--untested-hardware`. Reading,
backing up and syncing are not gated.

**Recovery.** Select + Play at power-on is Apple's disk mode in the boot ROM. Nothing the firmware
or this app writes can remove it, so a bad image is always recoverable: hold it, then flash again.
A backup from `core backup` puts the partition back exactly as it was, Apple's images included:

```
core flash --from-backup fwpart-131475456-….bin
```

`ipodpatcher` remains the documented fallback for flashing and restoring — `ipodpatcher <disk> -wf
core.ipod`, read back with `-rfb` and compared against the image.
