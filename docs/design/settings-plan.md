# Settings — plan

*2026-11-02. Proposed from a code inventory of the app and the CLI; the owner
chose the whole proposal ("I like it, run it").*

The brief: "look into settings and configuration for the desktop app — what
should be configurable, what should be able to be enabled / disabled."

## Where the app stands

`<UserConfigDir>/core/config.json` (`internal/app/config.go`) holds seven keys:
`source`, `backup_dir`, `names`, `theme`, `min_art_px`, `volume`, `replaygain`.
Three have a UI — theme and Use ReplayGain in the ··· menu, volume on the
player. `backup_dir` has no UI at all; the guide tells people to edit
`min_art_px` by hand. Choices made every run and forgotten: the Fix tab's
ticks (names, organize, low-res; disc folders), the window size (fixed
980×700, `run.go`), the tab. Behaviours that exist only as `core` flags:
content verify on sync, the playlists folder, a custom genre map, release tag,
pre-releases (always filtered, `ghrelease.go:195`). The update check runs once,
on the first detect (`ui.go:703`), and never again by itself. Backups are never
pruned (`flasher.go:1345` reserves the feature and the rule: `apple-*` is kept
forever). The only outbound network calls are GitHub and the cover providers
(iTunes, MusicBrainz + Cover Art Archive); neither can be turned off.

## Rules

1. **A setting is a choice, not a fact.** The file never caches anything the
   app can read from the device or GitHub. Absent means default, so a key
   nobody touched is not written; booleans that default on use the `*bool`
   pattern `replaygain` already uses; ints use 0 = default.
2. **Not configurable, and the sheet says so in one line at the bottom:**
   the two-second hold to write, the backup before every flash, the read-back
   verify, the untested-hardware refusal (stays `--untested-hardware`, CLI
   only), the on-device file names and art sizes (the firmware's contract),
   the song/album/genre caps, the prune confirmation (deleting is a per-sync
   decision with its own dialog, never a standing preference).
3. **One file, flags beat it.** `core-app --source`/`--theme` already beat the
   file; every new CLI flag that has a key does the same. The CLI reads the
   file through the `map[string]json.RawMessage` round-trip `name.go` uses,
   never `internal/app` (that would link Gio into `core`).
4. **Verbs in the menu, nouns in Settings.** `menuLabels` are "the words,
   verb first"; Theme and Use ReplayGain are the two nouns in there today and
   they move. The menu gains *Settings…*.
5. **Nothing here changes what a sync writes to the iPod.** Verify, eject and
   the playlists folder change how the host works, not the layout the firmware
   reads.

## The keys

| Key | Type | Default | Read by | Slice |
|---|---|---|---|---|
| `source` | string | "" | app, CLI (`--src` default, after `$CORELIB_SRC`) | exists; CLI C2 |
| `backup_dir` | string | flasher.DefaultBackupDir | app, CLI | exists; UI C1 |
| `names` | map | {} | app, `core name` | exists |
| `theme` | "", linen, onyx | system | app | exists; moves C1 |
| `min_art_px` | int | 600; <0 off | app, CLI `fix --min-art` default | exists; UI C1 |
| `volume` | *int | 100 | app | exists |
| `replaygain` | *bool | true | app | exists; moves C1 |
| `disc_folders` | bool | false | app, CLI `fix`/`organize --disc-folders` default | C1 |
| `fix_names` | *bool | true | app | C1 |
| `fix_organize` | *bool | true | app | C1 |
| `fix_lowres` | *bool | true | app | C1 |
| `window` | {w,h} | 980×700 | app | C1 |
| `last_tab` | music, fix, summary | music | app | C1 |
| `playlists_dir` | string | `<source>/Playlists` | app, CLI `sync --playlists` default | C3 |
| `online_art` | *bool | true | app, CLI `fix --art` (off refuses with a message) | C3 |
| `check_duplicates` | *bool | true | app | C3 |
| `verify_sync` | bool | false | app | C3 |
| `eject_after_sync` | bool | false | app | C3 |
| `genre_map` | string | "" (built-in) | app, CLI `sync`/`index --genre-map` default | C3 |
| `update_check` | "", connect, never | connect | app | C3 |
| `prereleases` | bool | false | app, CLI `update`/`install --prereleases` default | C3 |
| `keep_backups` | int | 0 = all; N = last N `fwpart-*` | app, CLI `flash`/`update`/`install` | C3 |
| `log_file` | string | "" | app (`--log` beats it) | C3 |

`window` is size only: Gio reports size through `app.ConfigEvent`, and
position is not portable. A saved size smaller than `MinWidth × MinHeight`
is ignored.

## The sheet

*···* → **Settings…** opens a sheet built like the album sheet (`album.go`:
scrim, plate, one Close, a vertical `widget.List`). Sections, in order, each a
heading in `SelSub` and rows in the Summary tab's two-column shape (label left,
control right):

- **Library** — Music folder (the same `sourceEd` and *Browse…* the Fix tab
  has; this is a second door, not a second field). Playlists folder (C3).
  *Fix all* ticks: Rename files to match their tags · Organize folders · Replace
  low-resolution covers · Split multi-disc albums into disc folders (the Fix
  tab's ticks are the same four state fields; both views save). Low-resolution
  line: a four-way picker Off · 300 px · 600 px · 1000 px. Look for covers on
  the internet (C3). Check for duplicate albums (C3).
- **Sync** (C3) — Verify copies by content (slower). Eject when a sync
  finishes. Genre map file (*Browse…*, *Built-in* to clear).
- **Firmware** — Backup folder (*Browse…*, *Show folder*). Keep backups: All ·
  Last 3 · Last 10 (C3; the row's sub-line: "the Apple firmware backup is
  always kept"). Check for updates: When an iPod connects · Never (C3).
  Include pre-releases (C3; sub-line: "for trying a release candidate on your
  own iPod").
- **Playback** — Use ReplayGain (moved from the menu; same sub-line as the
  guide's sentence). Volume stays on the player.
- **Appearance** — Theme: System · Linen · Onyx (the menu's radio rows, moved).
  Remember the window size (on; off = 980×700 every time). Reopen the last tab.
- **Diagnostics** (C3) — Write the log to a file: a path field, *Browse…*,
  *Show folder*.
- **Footer** — "Settings are in `<path>`" with *Show folder*; *Restore
  defaults* (clears every key except `names`; asks once); the one-line rule-2
  sentence: "The hold to write, the backup before every flash and the read-back
  check cannot be turned off."

Every change saves on the spot through `saveConfig`, as theme and ReplayGain
do today; there is no Apply. Esc and the scrim close it. The sheet is a canned
state (`--screenshot --dialog settings`) and its strings join `frontStrings`
so `TestNoOldWordsOnTheFront` reads them.

## `core config` (C2)

```
core config                 # every known key, value and whether it is the default
core config get <key>
core config set <key> <value>
core config unset <key>
core config path
```

Known keys only (the table above, minus `names`, which `core name` owns);
`set` validates the type and the enum values and refuses the rest with the
list. Round-trips through `readConfigObject` so unknown keys survive. The
guide's "edit `min_art_px` in config.json" sentence becomes `core config set
min_art_px 1000`.

## Slices

Each slice is its own branch and PR off `main`, squash-merged, with the plan's
table as the acceptance list. `go build ./... && go vet ./... && go test
-tags noaudio ./...` and the `-tags novulkan` build must pass; `gofmt -l` is
empty.

**C1 — The sheet.** Config keys `disc_folders`, `fix_*`, `window`, `last_tab`.
The sheet with Library (folder, ticks, low-res picker), Firmware (backup
folder), Playback, Appearance, footer. Theme rows and Use ReplayGain leave the
menu; *Settings…* joins the last group above About. `MinArtPx` and
`BackupDir` become live: changing them re-points the backend (`be.MinArtPx`,
the flasher's backup dir) without a restart. Window size saved on
`ConfigEvent` (debounced like volume), restored at `runWindow`; last tab
restored at `NewUI`. Tests: config round-trip with absent keys; the ticks
persist across `NewUI`; the sheet's strings pass the words test; a
`settings` canned state renders in both palettes.

**C2 — `core config`.** The subcommand, its tests (set/get/unset/path, type
and enum refusal, unknown keys preserved), and the CLI defaults from the file:
`source` for `--src`, `min_art_px` for `fix --min-art`, `disc_folders` for
`--disc-folders`. GUIDE.md: a short "Settings" paragraph after the ··· bullet;
the `min_art_px` sentence rewritten.

**C3 — Behaviour.** The remaining keys and rows. Wiring:
- `online_art` off → the backend's `artClient()` returns nil and the librarian
  gets no `ArtClient`; low-res rows then offer only a larger `cover.jpg` from
  the folder; the Fix tab's category sub-line says covers come from the folder
  only. `core fix --art` with it off says why and exits 1.
- `check_duplicates` off → no duplicate scan, no category.
- `verify_sync` → `syncer` Verify; `eject_after_sync` → after a sync (or a
  Sync and remove) ends with no error, the Eject job is queued, same path as
  the button.
- `update_check` never → `afterJob` does not queue `startLaunchCheck`; the
  menu's *Check for updates* still works and still asks the app question.
- `prereleases` → `ghrelease.Client.IncludePrereleases`; drafts stay out; the
  header's "vX available" and the app check honour it; `core update
  --prereleases`.
- `keep_backups` → after a flash's backup is written and verified, the
  flasher lists `fwpart-*` in the backup dir by their timestamp, keeps the
  newest N (the one just written is always among them), deletes the rest, logs
  each deletion, and never touches `apple-*` or any other name. `--dry-run`
  prints what it would delete.
- `log_file` → `core-app` opens it at start when `--log` is empty.
Tests for each wire: fake backend sees Verify; eject queued after a sync and
not after a failed one; no launch check under `never`; a pre-release is chosen
only with the flag; retention keeps `apple-*`, keeps the newest, deletes by
timestamp not by name order.

**C4 — the iPod's own settings (deferred, own design).** The app already
rewrites one record of `CORECFG.DAT` byte-for-byte for the clock stamp, and the
firmware is not running while the device is in disk mode, so the Summary tab
could offer volume limit, charge rate, backlight, clicker, the iPod theme,
12/24-hour clock and resume on startup, taking effect at the next boot. The
record also carries the resume locator and the host stamp, which must ride
through untouched, and the layout is the firmware's (`core/ui/settings.h`,
`core/kernel/config.c`), so this needs a parity test against the firmware's
encoder before it ships. Not in this plan's slices.
