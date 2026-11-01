# docs/screens — the app's screenshots

The five PNGs here are the desktop app, rendered by `core-app --screenshot`
itself through Gio's headless GPU path — the same code that draws the window,
from the canned demo state, at the window's default size (980×700). They are
used by [`../GUIDE.md`](../GUIDE.md) and the top-level README.

## Re-shooting them

From the repository root, on a clean tree, with `MUSIC` set to a real music
folder (the owner's is `/mnt/c/Users/you/Music`; it is only read):

```bash
go build -tags novulkan -o /tmp/core-app ./cmd/core-app
/tmp/core-app --screenshot docs/screens/core_app.png       --state playing --theme linen --size 980x700 --no-detect --source "$MUSIC"
/tmp/core-app --screenshot docs/screens/core_app_onyx.png  --state playing --theme onyx  --size 980x700 --no-detect --source "$MUSIC"
/tmp/core-app --screenshot docs/screens/core_app_album.png --state album   --theme linen --size 980x700 --no-detect --source "$MUSIC"
/tmp/core-app --screenshot docs/screens/core_app_fix.png   --state library --theme linen --size 980x700 --no-detect --source "$MUSIC"
/tmp/core-app --screenshot docs/screens/core_app_ipod.png  --state demo --tab summary --theme linen --size 980x700 --no-detect --source "$MUSIC"
```

Run them under `(ulimit -v 6000000; timeout 240 …)` — an earlier layout bug
allocated 40 GB per frame.

The albums, covers and the playing album's tracklist come from a real music
library (`--source`, read and never written), so reproducing them needs one;
without `--source` the same commands draw the canned grid of letter tiles.
Re-shoot them whenever the window changes, in the same PR as the change.

(The iPod's own screens — drawn by a PIL reproduction of the firmware UI —
live in the firmware repository,
[core-firmware/docs/screens](https://github.com/BrandonDedolph/core-firmware/tree/main/docs/screens).)
