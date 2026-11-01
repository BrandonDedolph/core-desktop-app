#!/usr/bin/env python3
"""
release.py — check a release of the app is ready, then say what is left.

    python3 tools/release.py v0.3.0            # prepare: notes + the steps
    python3 tools/release.py v0.3.0 --check    # after the tag: did it all land?

A release of this repository is the desktop app and the command line: ten
binaries, built by .github/workflows/release.yml from the tag, and no
core.ipod (the firmware is released from BrandonDedolph/core-firmware). This
script rewrites nothing in the tree. It

  1. checks CHANGELOG.md has a "## <version>" section and writes that section
     to a notes file for `gh release create`;
  2. prints the steps: tag, push, `gh release create` with NO core.ipod;
  3. asks the release what it carries. TEN assets are expected — the five
     `core` CLI binaries and the five `core-app` binaries — and no core.ipod.

`--check` runs only the asset step, which is what you want a few minutes after
tagging, when CI has had time to finish. Nothing here uploads binaries.
Python 3 standard library only; `gh` is needed for the asset step.
"""
import os
import re
import shutil
import subprocess
import sys
import tempfile

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CHANGELOG = os.path.join(REPO, "CHANGELOG.md")
# The GitHub repository the releases live on. Explicit, so the asset check
# asks the right repo whatever the checkout's remotes are called.
GH_REPO = os.environ.get("CORE_APP_REPO", "BrandonDedolph/core-desktop-app")


def die(msg):
    sys.exit(f"release.py: {msg}")


def main():
    args = sys.argv[1:]
    check_only = "--check" in args
    args = [a for a in args if a != "--check"]
    if len(args) != 1 or not re.fullmatch(r"v\d+\.\d+\.\d+", args[0]):
        die("usage: release.py vMAJOR.MINOR.PATCH [--check]\n"
            "       --check: only list the release's assets, change nothing")
    ver = args[0]

    # --check touches no file: it is the "did it all land?" run.
    if check_only:
        check_assets(ver, required=True)
        return

    text = open(CHANGELOG, encoding="utf-8").read()
    m = re.search(rf"^## {re.escape(ver)} .*?$(.*?)(?=^## |\Z)", text, re.M | re.S)
    if not m:
        die(f"CHANGELOG.md: no '## {ver} — <date>' section — write it before tagging")
    # Outside the tree: a notes file is an input to `gh`, not a source file.
    notes = os.path.join(tempfile.gettempdir(), f"core-app-release-notes-{ver}.md")
    open(notes, "w", encoding="utf-8").write(m.group(1).strip() + "\n")
    title = "Core app " + ver
    print(f"CHANGELOG.md: section found; notes written to {notes}")
    print(f"""
next:
  git tag -a {ver} -m "{title}"
  git push origin main {ver}
  gh release create {ver} --repo {GH_REPO} --title "{title}" --notes-file {notes}

  # no core.ipod: the firmware is released from core-firmware. The tag push
  # starts .github/workflows/release.yml, which builds the ten core-* /
  # core-app-* binaries on three runners and attaches them once the release
  # exists (it waits up to ten minutes for it). Then:
  python3 tools/release.py {ver} --check    # lists which of the ten landed
""")
    check_assets(ver)


# The ten binaries .github/workflows/release.yml attaches on the tag push:
# five `core` CLI binaries, all cross-compiled on one Linux runner because
# the CLI is pure Go, and five `core-app` desktop binaries, which are not —
# Gio's Windows backend is pure Go and cross-builds with the CLI, but its
# X11 and Cocoa backends are cgo and are built on their own runners.
CLI_BINARIES = [
    "core-linux-amd64",
    "core-linux-arm64",
    "core-darwin-amd64",
    "core-darwin-arm64",
    "core-windows-amd64.exe",
]

APP_BINARIES = [
    "core-app-linux-amd64",
    "core-app-linux-arm64",
    "core-app-darwin-amd64",
    "core-app-darwin-arm64",
    "core-app-windows-amd64.exe",
]

BINARIES = CLI_BINARIES + APP_BINARIES

# A firmware image does not belong on a release of this repository.
FORBIDDEN = {"core.ipod"}


def check_assets(ver, required=False):
    """Print which of the ten binaries are on the release.

    Advisory during a normal run — at that point the tag usually does not
    exist yet, and a missing release is the expected state, not an error.
    With --check (required=True) it is the whole point of the invocation, so
    a missing gh or a missing release exits non-zero.
    """
    def missing(msg):
        print(f"assets: {msg}")
        if required:
            sys.exit(1)
        return None

    if not shutil.which("gh"):
        return missing("gh not on PATH — cannot list the release's assets")
    r = subprocess.run(["gh", "release", "view", ver, "--repo", GH_REPO,
                        "--json", "assets", "-q", ".assets[].name"],
                       cwd=REPO, capture_output=True, text=True)
    if r.returncode != 0:
        return missing(f"no release {ver} yet"
                       + (f" ({r.stderr.strip().splitlines()[-1]})" if r.stderr.strip() else ""))

    have = set(r.stdout.split())
    gone = []
    print(f"\nassets on {ver}:")
    for name in BINARIES:
        if name in have:
            print(f"  present  {name}")
        else:
            gone.append(name)
            print(f"  MISSING  {name}")
    wrong = sorted(n for n in have if n in FORBIDDEN or n.endswith(".ipod"))
    for extra in sorted(have - set(BINARIES)):
        print(f"  {'WRONG' if extra in wrong else 'extra'}    {extra}")
    if wrong:
        print(f"{len(wrong)} asset(s) do not belong on an app release: {', '.join(wrong)}")

    if gone or wrong:
        if gone:
            print(f"{len(gone)} of {len(BINARIES)} missing: {', '.join(gone)}")
            print("  core-* binaries -> .github/workflows/release.yml attaches these; "
                  "check `gh run list --workflow=Release`")
        if "core-app-linux-arm64" in gone:
            print("  core-app-linux-arm64 -> its job runs on ubuntu-24.04-arm and is "
                  "continue-on-error; that runner label is not on every plan")
        if any(g.startswith("core-app-darwin") for g in gone):
            print("  core-app-darwin-*    -> built on macos-latest (Cocoa is cgo); "
                  "check that job in the same run")
        if required:
            sys.exit(1)
    else:
        print(f"all {len(BINARIES)} expected assets are attached")


if __name__ == "__main__":
    main()
