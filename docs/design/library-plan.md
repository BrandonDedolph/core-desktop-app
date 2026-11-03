# The library — plan (proposal)

*2026-11-03. Written from the owner's statement of what the app is for,
and three asks the same day. A proposal: the choices are marked, and nothing
here is built yet.*

## The charter

The owner, 2026-11-03: the whole point of the app is to

1. manage your music library on a specified drive (the OS drive or a
   plugged-in external drive);
2. manage what is on the iPod;
3. fix metadata in the music library, then sync it over — never edit the
   iPod directly;
4. update to newer firmware versions;
5. fix issues found with the iPod's library or the drive's library;
6. see and read the log from the desktop app and from the iPod.

Where the app stands, honestly:

| Job | Today | Gap |
|---|---|---|
| 1 Library on a drive | the music folder, the Fix tab (names, folders, covers, discs, duplicates) | the Music tab is an album grid only; no way to add music except copying folders in by hand and running Fix; external drives work but the chooser (D1) is the first time one can be picked without typing |
| 2 What is on the iPod | Sync, Preview, Sync and remove, the grid's NEW/CHANGED/on-iPod states | no per-album "take this off the iPod" without removing it from the folder; no view of the iPod's own library as the iPod sees it |
| 3 Metadata in the library | Fix renames files *from* tags, organizes folders, replaces covers | the tags themselves cannot be edited (title, artist, album, genre, year, track number) |
| 4 Firmware | Install, Update, flash an image, backups, retention | — |
| 5 Fix issues | the Fix tab for the folder; `core doctor` for the iPod (CLI only; the window runs the same checks on Refresh and shows one boot line) | the iPod's problems are not listed in the window, and nothing offers to repair them |
| 6 The logs | the window's log (drawer, copy, file) | the iPod's log (`CORELOG.BIN`) is readable by `tools/make_log.py` in the firmware repo and decoded by `devicefs.ReadLogRing` for one boot fact, but nobody can read it in the window |

## L1 — the Music tab has views

*The ask: "it shouldn't just be a list of albums — an artist list (get
artist art), genres, songs, albums."*

A view switch under the tab row — **Artists · Albums · Genres · Songs** —
the iPod's own top menu, because the iPod is what this library is for and
the same grouping on both ends is what makes the window a true preview of
the device. Everything comes from the scan the app already has
(`library.Scan`: every track carries Artist, Genre, Disc, Track,
DurationS; every album its folder and cover), so no new reading.

- **Albums** is today's grid, unchanged.
- **Artists** is a grid of artist tiles (the album tile's size and chrome),
  sorted with "The " ignored as the iPod sorts. A tile carries the artist's
  picture, the name, and "N albums · M songs"; a click opens the artist: a
  sheet like the album sheet with the picture, the facts, and the artist's
  albums as a row of small tiles, each opening the album sheet. The
  sync states roll up: an artist tile shows NEW when any album is new.
- **Genres** is a list (the genre count is small): the genre name, the
  count of artists and albums, a strip of up to six covers; a click shows
  the genre's albums in the grid, filtered, with the view switch reading
  "Genres › Hip-Hop" and a ‹ back. The genre is the tag's genre through the
  genre map, exactly what the iPod shows.
- **Songs** is a table: #, title, artist, album, time, and the sync state
  as a dot; sortable by the column headers, filtered by the search field
  that appears on this view only (typing narrows title/artist/album). A
  click plays the song on this computer, as a track in the album sheet
  does. This is the one view that is not a tile grid and the one that
  needs a list widget with fixed columns, which the app does not have yet
  (`listLine` is close).

**Artist pictures — the decision to make.** Nothing the app talks to today
has artist photos: iTunes Search returns none for `musicArtist`, the Cover
Art Archive is releases only, MusicBrainz has URL relations that sometimes
reach Wikimedia Commons and sometimes do not. Deezer's search API returns a
1000×1000 photo per artist with no key and no sign-up for the request, but
its terms limit use to "strictly non-commercial" and "private use within a
family scope" and reserve the photos to Deezer; TheAudioDB's guide page is
gone. So:

- **Recommended: the mosaic.** An artist's picture is a 2×2 of their album
  covers (one cover alone when there is one; the firmware's placeholder
  chip when there is none), rendered once per scan and cached beside the
  app's other caches. Offline, deterministic, no terms, and it looks like
  the library. This is what the iPod would show if it ever had an artist
  view.
- **Optional, off by default: Deezer.** A Settings tick under Covers,
  "Artist pictures from Deezer", honouring the existing "Look for covers on
  the internet" switch, with the sub-line saying whose photos they are. The
  pictures stay in the app's cache and are never written into the library
  or onto the iPod. The owner decides whether the terms are acceptable for
  a private, non-commercial use; the app ships with it off.

Tests: the grouping (artist sort, "The " rule, genre map, multi-artist
albums land under each artist), the mosaic for 1/2/3/4+ covers, the song
table's sort and filter, canned states for all four views in both palettes.

**Deviations (as built, 2026-11-03).**

- The switch reads **Albums · Artists · Genres · Songs**, Albums first and
  the default (the owner's brief), not Artists first; `music_view` in
  config.json (and `core config`) remembers it.
- The scan's `Artist` is the *folder's* artist — what the index carries —
  so `library.Track` gained `TagArtist` (never written to the device) and
  `syncer.Plan` carries the `Scan` it was made from (not serialised); the
  views read that, so there is still no second read of the library.
- "Multi-artist albums land under each artist": each artist that *leads* a
  track. A real library's tags are full of `blackbear; G-Eazy` and `feat.`
  spellings — 211 tiles on the owner's MC folder, 36 with the rule — so a
  tag is cut at `;`, ` / `, `feat.`/`ft.`/`featuring` and `(with`; `&` and
  `,` are left alone (*Simon & Garfunkel*, *Tyler, The Creator*). The
  Songs table still shows the whole tag.
- The mosaic for two covers runs corner to corner (A B / B A) and for three
  puts the first again in the last corner (A B / C A). It is composed in
  memory, in the window's artist picture cache, dated by the albums' newest
  file, so a rescan that changed nothing composes nothing; it is not
  written to disk.
- Deezer: the photo is that of the first result whose *name* matches (case,
  punctuation and "The " folded), not `data[0]` blindly — an obscure name's
  first hit is often somebody else. A miss is remembered for a week, a
  failure (no network) is not retried until the next start, and the photos
  are cached in `<UserCacheDir>/core/artists`. The Settings tick sits in
  the Library section's Covers row under "Look for covers on the internet"
  and says when that switch is off that it cannot work; the window and the
  backend each check both switches.
- A genre row holds every album one of whose tracks has that genre; an
  album with no genre is in no row, as on the iPod.
- The Songs table's sync dot: filled for NEW, a ring for CHANGED, nothing
  for a song already on the iPod (the tiles' badges, as dots).
- Canned states: `--state artists|genres|genre|songs`, `--view` for any
  state (with `--source` too), and `--dialog artist`.

## L2 — adding music

*The ask: "what the process should look like when someone wants to add
more music to the library — how does that get added and properly sorted."*

Today the only way in is to copy a folder into the music folder by hand,
switch to the Fix tab, and run Fix all. That works, and it is also why the
process needs a front door: nothing tells a person that this is the
process.

**Add music…** — in the ··· menu, on the Music tab's empty state, and as a
button at the top of the Music tab beside the view switch. It opens the
chooser (D1) in folder mode: "Choose a folder of music to add". The folder
can be anything — a ripped CD, a download, a whole second library, a USB
stick. Then one sheet, **Adding to your library**, in three stages, all
read-only until the last button:

1. **What was found.** The folder is scanned the way the library is
   (`librarian.Inspect` on the chosen folder with the library's own
   options): N albums, M songs, their formats, and for each album the name
   it will get in the library (`Album - Artist`, from the tags through the
   organizer's `Canonical`). Problems that stop an album are listed under
   it: no tags, no track numbers, a format the iPod does not play (the
   scan's own warnings). Albums the library **already has** (same
   canonical folder, or the duplicate check's same-songs rule) are marked
   "already in your library" and unticked.
2. **The choice.** Copy or move, one radio; the move is the default for a
   folder on the same drive and the copy for another drive (a download
   folder should empty, a USB stick should not). A tick per album, all on
   except the ones already there.
3. **Add.** The button says "Add 3 albums". It copies or moves each file
   to its canonical place in the library (the organizer's rules; disc
   folders per the setting), fetches a cover for an album without one if
   the internet switch is on, journals every move the way Fix does so
   *Undo* exists, and rescans. The albums appear in the grid as NEW and the
   header's button becomes "Sync 3 albums". Nothing touches the iPod.

The CLI gets `core add <folder> --to <library> [--move] [--dry-run]` with the
same plan printed, because an SSH session is sometimes where the music is.

Not in this slice: a watched folder (a download folder that adds itself),
and drag-and-drop onto the window — Gio's Windows backend does not take
file drops (it has clipboard text only), so a dropped folder cannot be
received without a change to the toolkit.

**Deviations (as built, 2026-11-03).**

- **Always a copy, then the originals go.** The owner decided the
  copy-or-move question differently from stage 2: there is no radio. Every
  file is copied into the library and verified by SHA-256 against its
  original (hashed as it is read for the copy, the copy hashed again read
  back from the disk); only when every file of an album verified are that
  album's originals deleted, and then their folder if that left it empty
  (a `.cue` or a `.log` keeps it). One tick on the sheet, on by default,
  "Delete the originals once they are safely in the library"; off, the
  source is left alone (`--keep-originals` on the CLI, whose flag is the
  opposite of the spec's `--move`). A mismatch, a full disk or a cancel
  removes that album's copies and leaves its originals; the journal is
  written before each step, so a run that dies between the copy and the
  delete leaves both and Undo removes the copies. Undo moves the copies
  back (a rename on one drive, copy-check-remove across two), each checked
  against the original's hash; a copy changed since is left and listed.
- **A cover in a copy does not stop an exact undo.** A cover embedded in a
  copy changes its bytes, so the copy's metadata before the cover went in
  (the region in front of the audio, which the FLAC writer never moves) is
  kept beside the journal and the original is rebuilt from it plus the
  copy's audio, then hashed like the rest. The cover is the album folder's
  own picture first, then the art client's top match at 0.9 or better
  (none when the internet switch is off); an MP3 album gets none (there is
  no ID3 writer).
- **Albums are found by tags, not folders.** Songs are grouped by source
  folder and album tag, named with the organizer's artist rule (majority
  album artist, else majority artist; a tie is a problem, not a guess),
  and groups that name the same library folder merge (CD 1 and CD 2 in two
  folders). The names are built with `organizer.CanonicalFolder` and a new
  `CanonicalNumbered` (the extension the file has, so an MP3 stays an MP3;
  "D-NN" for a flat multi-disc album, `Disc N` folders when the source had
  them or the disc-folders setting is on), not `organizer.Canonical`,
  which can only see one file's own artist. MP3s are added as well as
  FLACs; the Fix tab is still FLAC only.
- **"Already in your library" can be ticked.** Same folder (as the iPod's
  name comparison folds it) or same songs (the duplicate check's rule,
  against every FLAC and MP3 album in the library; off with the duplicate
  check) start unticked with the reason; ticking one adds it, and a file
  that would land on one already there is never written over — the album
  fails, its copies go.
- **The journal** is librarian's fix journal at format 3 with `adds` and
  `add_dirs` (format 2 is the tag editor's, L5), so an older build refuses
  it rather than reporting an undo that put nothing back; `core fix --undo`
  reads it. The sheet's ticks are new dialog machinery (`picks`, `option`,
  `okFor`) rather than a second sheet type. *Add music…* sits at the far
  right of L1's view switch row, on every view (after the Songs search
  field).

## L3 — the iPod's log in the window

The log drawer gets two tabs, **This window** and **The iPod**. The second
reads `CORELOG.BIN` through `devicefs.ReadLogRing`, orders the blocks the
way `tools/make_log.py` does (`ordered_entries`), and shows the text, newest
boot first, with the boot's build and time as a divider; *Copy* copies it.
Read on Refresh and on opening the tab, never written. A parity test pins
the ordering against a ring the firmware's tool emits (`make_log.py --emit`),
the way the config record already has one.

## L4 — the iPod's problems, and the repair

The Summary tab gains a row, **Library on the iPod**, from the checks
`core doctor` runs and the window already runs on Refresh (`CheckVolume`,
`CheckIndex`, `CheckConfig`, `CheckLog`, `CheckOTG`, `CheckPlaylists`):
"907 songs, 101 albums, index valid" when all is well, or the failures in
words — a missing sidecar, an index that names a file that is not there, a
playlist entry that points nowhere — with one button, **Repair**, which is
a sync with `--art-refresh` where the sidecars are the problem and a plain
sync otherwise, through the preview. The window never patches the iPod by
hand: the repair is the same write a sync is (charter 3).

## L5 — editing tags

Charter 3 says metadata is fixed in the library. Fix fixes what the tags
*imply* (names, folders, covers); the tags themselves are edited nowhere.
The album sheet gains **Edit…**: album title, album artist, genre, year,
and per track the title, artist and number, in fields; *Save* writes the
tags (FLAC: Vorbis comments, through `internal/flac`, which already
rewrites PICTURE blocks and preserves the audio; MP3: ID3v2 text frames,
which `internal/id3` reads but does not yet write — the slice that adds
writing comes with the usual byte-for-byte test on a real file's audio),
then runs the organizer on the album so the file and folder names follow
the new tags, journals it for *Undo*, and marks the album CHANGED. Every
other field stays as it was: an editor that rewrites the whole tag block
loses the ReplayGain and the cover.

**Deviations (as built, 2026-11-03).**

- *The renaming is FLAC-only.* The organizer has only ever read FLACs, so an
  MP3 album's tags are written and journaled (and Undo puts them back) but its
  file and folder names stay. Teaching the organizer MP3 is its own change: it
  touches Fix, the duplicate check and the re-copy count.
- *The organizer gained `Options.Albums`* so the rename after a save reads
  that one album's files, not the whole library; names held by other albums
  are still collisions.
- *The journal is format 2 when it carries tags* (`tags`: every old value of
  every touched key, aliases included). Format-1 journals — all of Fix's —
  read and replay unchanged; an older build refuses a format-2 one rather
  than putting the names back over tags it would not restore.
- *What the MP3 writer refuses*, untouched: ID3v2.2 tags, whole-tag
  unsynchronisation, a tag that runs past the file or whose frames do not add
  up. A v2.3 extended header and a v2.4 footer are dropped when the tag is
  rewritten. A new tag on an untagged MP3 is v2.3.
- *A track number keeps its total*: changing 3 of "3/12" to 4 writes "4/12".
- *A playing track is stopped* before its file is rewritten (Fix skips a
  renamed one instead): on Windows an open file cannot be renamed, and the
  sheet says it stopped it.

## L6 — the app updates itself

*The ask: "the desktop app should also be able to update itself."*

Today the launch check notices a newer app release and the ··· menu and
About offer *Get the newer app…*, a link to the release page. The link
becomes a job. **Update the app** downloads the release's `core-app.exe`
and `core.exe` for this platform through the same `ghrelease` client the
firmware update uses (its checksum file verified the same way), into the
app's cache folder, then swaps them in: on Windows a running executable
cannot be overwritten but it can be renamed, so the job renames the running
`core-app.exe` to `core-app.old.exe`, moves the new one into place, does the
same for `core.exe` beside it, and relaunches; the new app deletes the
`.old` files on its next start. On Linux and macOS it is a plain replace and
relaunch. A failed download or a checksum that does not match leaves
everything as it was and says so in the log. The CLI gets `core self-update`
with the same steps and `--check` to only say. Not in scope: an installer,
a service, or updating an app that was not started from a writable folder
(Program Files without elevation): that case says where the release is and
stops, which is today's behaviour.

**Deviations (as built, 2026-11-03).** The releases carried no checksum
for the app's binaries — `ghrelease`'s only check is the `.ipod` one — so
the release workflow now writes a `<name>.sha256` beside every binary
(sha256sum's format, made by the job that built it) and
`tools/release.py --check` expects twenty assets. The updater refuses a
binary without its `.sha256`, so releases up to v0.3.0 cannot be installed
this way; the first release that can update itself is the one after the
release made from this change. The Program Files refusal is a log line naming the
folder and the release page rather than the old "open the page" link: the
item is now the job, and the link is gone (About still shows the page). On
Linux and macOS no `.old` file is made — the new file is renamed over the
old one — so only the Windows swap has a rollback, which matters there
because it is two renames per program. `core self-update` replaces both
programs and does not restart anything: there is nothing to restart in a
console. The menu item is greyed while another job runs.

## Decisions taken 2026-11-03

- The bar says the iPod's name alone; the model is the Summary tab's; the
  title bar always says Core (option A of the header proposals; PR #8).
- Artist pictures: the mosaic is the default; Deezer stays an opt-in tick
  until the owner decides on its terms.
- The iPod gained a Daylight Saving setting and keeps standard time; the
  app's stamp carries the host's daylight state (core-firmware PR #53, app
  PR #9).

## Order

L2 first (it is the front door and it reuses D1's chooser and the Fix
machinery), then L1 (the views; the mosaic; Deezer behind the owner's
decision), then L3 and L4 together (both read the iPod), then L5 (the tag
writer is the only new file-format work). Each is its own branch and PR,
with the gates every slice has.
