# Versions and releases

The app and the `core` CLI are versioned by this repository's `vX.Y.Z` tags; the first is
`v0.3.0` (the same code that was tagged `app-v0.3.0` in ipod-core before the split). The firmware
keeps its own `vX.Y.Z` tags in core-firmware — the two number lines are independent.
`scripts/build-all.sh` and the release workflow stamp the binaries from
`git describe --tags --always --dirty --match 'v[0-9]*'`; with no tag reachable the stamp is the
bare short hash. The window's About prints the stamp, the commit and the build date, and the CLI's
`--version` the same.

**Which firmware it writes for.** `app.MinFirmware` (`v0.2.0`) is the
oldest firmware whose index, config and log formats this app writes. About
says "writes files for Core v0.2.0 or newer"; when the iPod's firmware is
older the header and the Summary tab say "this app needs Core v0.2.0 or newer
on the iPod — update the firmware first", Update is the primary instead of
Sync, and a real sync refuses (the preview still runs). Raise it when a
firmware release changes one of those formats.

**Update checks.** The firmware check lists core-firmware's releases
(`ghrelease.DefaultRepo`) and takes the newest published `v…` release that
carries `core.ipod` (`ghrelease.LatestFirmware`) — never `releases/latest`.
Once per launch a second, quiet check (`ghrelease.LatestApp`) lists this
repository's releases (`ghrelease.AppRepo`) for a newer `v…` release than
the one running (numeric compare; a build stamped with a bare hash is never
told) and, if there is one, puts a line in About and "Update the app to
vX.Y.Z" in the ··· menu. It logs only on failure. That item (and `core
self-update`) is `internal/selfupdate`: this platform's `core-app-<os>-<arch>`
and `core-<os>-<arch>` off the release, each checked against the
`<name>.sha256` the release workflow publishes beside it (no checksum, no
update), then swapped in — on Windows the running exe is renamed to
`.old.exe` and the new one takes its name, elsewhere the new file is renamed
over the old with its mode bits — and the app restarts. Both programs delete
`core*.old.exe` at start.

**Cutting a release.**

```bash
# 1. write the "## vX.Y.Z — <date>" section in CHANGELOG.md, commit it
python3 tools/release.py vX.Y.Z           # checks it, writes the notes file, prints the rest:
git tag -a vX.Y.Z -m "Core app vX.Y.Z"
git push origin main vX.Y.Z
gh release create vX.Y.Z --title "Core app vX.Y.Z" --notes-file <printed path>
# .github/workflows/release.yml builds the ten binaries from the tag, writes a
# <name>.sha256 beside each, and attaches all twenty once the release exists
# (no core.ipod). Then:
python3 tools/release.py vX.Y.Z --check   # lists which of the twenty landed
```

