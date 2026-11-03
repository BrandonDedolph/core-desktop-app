# Building and testing

Go 1.22.10 (`.tool-versions`), from the repository root:

```bash
go build -o core ./cmd/core
./core --help
```

The CLI is pure Go, so one machine cross-compiles every target:

```bash
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -o core-darwin-amd64  ./cmd/core
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o core-darwin-arm64  ./cmd/core
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o core-linux-amd64   ./cmd/core
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o core-linux-arm64   ./cmd/core
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o core-windows.exe   ./cmd/core
```

## Building `core-app`

Gio's **Windows** backend is pure Go, so the exe everyone actually
downloads cross-builds from anywhere. `-H windowsgui` is what stops a
console flashing up on launch:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -ldflags "-H windowsgui" -o core-app.exe ./cmd/core-app
```

Gio's **X11/Wayland** and **Cocoa** backends are cgo, so those builds
happen on their own platform:

```bash
# Linux (needs libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-dev
#        libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev
#        libffi-dev libxcursor-dev, and libasound2-dev for the player)
go build -tags novulkan -o core-app ./cmd/core-app

# macOS
CGO_ENABLED=1 go build -o core-app ./cmd/core-app
```

`scripts/build-all.sh` does all of this: it builds the five
`core` binaries, `core-app-windows-amd64.exe`, and whichever `core-app`
builds the host can make natively — then **prints the ones it skipped**.
Nobody has three machines, and a release job that silently published
six of ten assets would be worse than one that names the four it
could not build. `.github/workflows/release.yml` runs it on
`ubuntu-latest`, `ubuntu-24.04-arm` and `macos-latest` for that reason.

### `-tags novulkan`, and why a plain `go build ./...` still works here

Gio compiles a Vulkan path on Linux, which needs `vulkan/vulkan.h`. That
header is not installed on every machine that can run the app perfectly
well through OpenGL ES (this project's WSL box among them), and a C
compiler error is not a useful thing for a Go build to fail with.

So the two files that import `gioui.org/app` and `gioui.org/gpu/headless`
— `internal/app/run.go` and `internal/app/snapshot.go` — carry
`//go:build !linux || novulkan`, with stubs returning `ErrNoGPU` behind
`//go:build linux && !novulkan`. The consequences:

- `go build ./...`, `go vet ./...` and `go test ./...` work on any Linux
  machine, with or without the Vulkan headers. The model, the job
  runner, the backend wiring and every layout function are compiled and
  tested either way — Gio records drawing into an `op.Ops` list, and
  nothing is rasterised until a frame is submitted.
- `go build -tags novulkan ./...` additionally compiles the window and
  the headless renderer, and `go test -tags novulkan ./...` runs
  `TestSnapshot`, which renders the window to PNGs. Without the tag that
  test **skips**; it never fails for want of a GPU.
- Windows and macOS builds never see any of this: their backends do not
  import the Vulkan headers, so the real files are always in.

CI runs both, so the tagged build is not a configuration only one laptop
ever compiles.

### `-tags noaudio`, and why the tests always pass it

The album sheet plays tracks through `internal/player` (oto v3 for the
device, mewkiz/flac and go-mp3 for the decoding — all pure Go on Windows;
on Linux oto is cgo against ALSA, hence `libasound2-dev`; on macOS it
loads AudioToolbox at run time). Built with `-tags noaudio`, the device
half is a stub: `player.Available` is false, `Play` returns
`ErrUnavailable`, and the sheet says "Playback is not available in this
build" with no transport. **Every test run passes `noaudio`**, so no test
ever opens an audio device on a developer's machine or a CI runner; the
player's own tests drive its decoding, resampling, dithering and
end-of-track logic against a fake device and synthetic FLAC files. Builds
for people to run never pass it.

## Rendering the window without a display

```bash
go run -tags novulkan ./cmd/core-app --screenshot out.png --state demo
go run -tags novulkan ./cmd/core-app --screenshot out.png --state library
go run -tags novulkan ./cmd/core-app --screenshot out.png --state empty --size 720x520
go run -tags novulkan ./cmd/core-app --screenshot out.png --state looking
go run -tags novulkan ./cmd/core-app --screenshot out.png --state notinstalled
go run -tags novulkan ./cmd/core-app --screenshot out.png --state demo --tab summary --theme onyx
go run -tags novulkan ./cmd/core-app --screenshot out.png --state advanced     # Summary tab, Advanced open
go run -tags novulkan ./cmd/core-app --screenshot out.png --state menu         # the ··· menu
go run -tags novulkan ./cmd/core-app --screenshot out.png --state looking --drawer
go run -tags novulkan ./cmd/core-app --screenshot out.png --state demo --dialog write   # also holding, remove, about
go run -tags novulkan ./cmd/core-app --screenshot out.png --state album      # the album sheet, track 3 playing
go run -tags novulkan ./cmd/core-app --screenshot out.png --state playing    # the same, sheet closed: the footer's Now Playing
go run -tags novulkan ./cmd/core-app --screenshot out.png --state preview    # the sync preview
go run -tags novulkan ./cmd/core-app --screenshot out.png --state artists    # the Library tab's Artists view (also genres, genre, songs)
go run -tags novulkan ./cmd/core-app --screenshot out.png --state lowres     # Fix: low-resolution covers, candidates up (lowres-after: applied)
go run -tags novulkan ./cmd/core-app --screenshot out.png --state duplicates # Fix: two folders with the same songs (also tidy)
go run -tags novulkan ./cmd/core-app --screenshot out.png --state demo --dialog settings   # a dialog: write, holding, remove, about,
                                                                                           # album, artist, preview, settings, chooser,
                                                                                           # ipodlog, albumedit
go run -tags novulkan ./cmd/core-app --screenshot out.png --state demo --source "$MUSIC"   # a real music folder's albums and covers
```

`--theme system|linen|onyx` picks the palette (default: the saved choice,
System on a first run; a screenshot without it is Linen on Linux);
`--tab music|fix|summary` the tab (`music` is the Library tab, under its old
name; `albums`, `library` and `details` still work); `--view
albums|artists|genres|songs` the Library view; `--drawer` opens the log
drawer; `--dialog` puts a canned dialog on screen — drawn only, there is no
job behind it to confirm; and `--source` draws a real music folder's albums,
covers and one tracklist in place of the canned grid of letter tiles (read,
never written).

One frame, headless, then exit. `--state demo` is a canned State with
the bench device, the 928-track library and a sync half-finished, open on
the album grid; `--state library` is the Fix tab with something in
every problem row; `--state empty` is the first run; `--state looking` is
the window with no iPod and the poll running; `--state notinstalled`
is a stock iPod on the Install screen; `artists`, `genres`, `genre` (one
genre's albums) and `songs` are the Library tab's other views; `lowres`,
`lowres-after`, `tidy` and `duplicates` are the Fix tab's special rows.
`TestSnapshot`,
`TestSnapshotLaunchPhases` and `TestSnapshotTheTabs` use the same code
path and write to `$CORE_SNAPSHOT_DIR` (default:
`$TMPDIR/core-app-snapshots`).

Rendering is also a memory assertion. `TestFrameAllocationIsBounded`
renders every canned state at both sizes and fails if one frame allocates
more than 256 MiB; `TestEveryListScrollsVertically` checks the thing that
blew that budget once (the `core-app` row in [STATUS.md](STATUS.md) tells the story).

`core-app --no-detect` turns the poll off: the window then knows only
what a job told it, which is what a screenshot or a test wants and
nothing else does.

`core-app --log FILE` appends startup diagnostics to a file. On Windows
that is the only way to find out why a window did not appear: a
`-H windowsgui` process has no console, so its stderr goes nowhere.

## Test

```bash
go test -tags noaudio ./...       # noaudio: never open an audio device (see above)
go vet ./...
gofmt -l .        # must print nothing; CI enforces this

# and again with the GUI files in (see -tags novulkan above):
go build -tags novulkan ./...
go vet   -tags novulkan ./...
go test  -tags "novulkan noaudio" ./...     # this run includes TestSnapshot
```

On this project's WSL box the app's tests also run under a memory guard,
since a layout bug once allocated 40 GB in one frame:
`(ulimit -v 6000000; timeout 900 go test -count=1 -tags "novulkan noaudio" ./internal/app/)`.

### Parity env vars

Several tests check Go output against the firmware repository — its Python tools in `tools/`
(`build_index.py`, `make_config.py`, `make_log.py`, `make_otg.py`, `coreart.py`), its C headers
and sources — or against real data. They **skip** — never fail — when the thing they need is
absent, and they never touch the network. The firmware is a separate checkout now, so nothing
looks for it by walking up from this tree: point `CORE_REPO` at it. CI checks core-firmware out
into `_core-firmware/` (ignored by `go ./...` and by git) and sets `CORE_REQUIRE_PARITY=1`, under
which a firmware-parity test that would have skipped fails instead; set it locally too, to prove a
run checked parity rather than skipped it.

```bash
CORE_REPO=/path/to/core-firmware CORE_PARITY_SRC=/path/to/Music/MC \
  go test -count=1 -tags noaudio ./...
```

| Variable | What it points at | Used by |
|---|---|---|
| `CORE_REPO` | a checkout of [core-firmware](https://github.com/BrandonDedolph/core-firmware), for tests that run its Python tools or grep its C sources (caps in `core/kernel/main.c`, `settings_defaults()` in `core/ui/settings.c`, the art sizes in `core/ui/artcache.h`, the name-hash vectors, `tools/artist_genres.json`, the MP3 vectors in `core/tests/codec-vectors`). Unset: those tests skip. | S2, S3, S4a |
| `CORE_REQUIRE_PARITY` | `1` turns every firmware-parity skip (no `CORE_REPO`, no `python3`, no `ffprobe`) into a failure. CI sets it. Tests that want `CORE_PARITY_SRC` or a dump still skip. Helper: `internal/testutil`. | CI |
| `CORE_PARITY_SRC` | the source music tree (`…/Music/MC`), read and never written | S1, S2, S3, S4b |
| `CORE_PARITY_IDX` | an oracle `CORELIB.IDX` built by core-firmware's `tools/build_index.py` | S2 |
| `CORE_FWPART_DUMP` | a firmware-partition dump (`ipodpatcher -r`), or several separated by `:` | S5 |
| `CORE_SNAPSHOT_DIR` | where `TestSnapshot` writes the rendered window PNGs. Default: `$TMPDIR/core-app-snapshots` — a stable path, not `t.TempDir()`, because the point of the files is that somebody looks at them after the run. | S10 |

Partition dumps and music are never committed.


The README gallery (`docs/screens/`) is shot this way from a real library;
[`screens/README.md`](screens/README.md) has the exact commands.
