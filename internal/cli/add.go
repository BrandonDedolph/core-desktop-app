package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
	"github.com/spf13/cobra"
)

// newAddCmd builds "core add": a folder from outside the library copied into
// it under the organizer's names, verified, and cleared from where it was.
func newAddCmd() *cobra.Command {
	var (
		to          string
		keep        bool
		dryRun      bool
		discFolders bool
		journalDir  string
	)
	cmd := &cobra.Command{
		Use:   "add <folder>",
		Short: "Add a folder of music to the library: copy, verify, then clear the originals",
		Long: `Reads a folder from outside the library — a ripped CD, a download, a USB
stick — and prints what it found: the albums by their tags, the name each
will get in the library ("Album - Artist"), and the ones that are not
added, with the reason:

  no tags, no track numbers   the names come from the tags, and are never guessed
  a format the iPod cannot play   .m4a, .ogg, .wav and the like
  already in your library     the same folder, or the same songs in another
                              (listed and skipped here; the window lets you tick one anyway)

Then every file of each album is copied to its place, every copy is read
back and compared with its original by SHA-256, and only when every file
of the album verified are the originals deleted — and their folder, when
that leaves it empty. A copy that does not match removes that album's
copies and leaves its originals. --keep-originals leaves the folder as
it was. An album with no cover gets the cover file from its folder, or
one from the internet unless online_art is off.

Undo it with ` + "`core fix --undo <journal>`" + `: the originals go back
where they were, each checked against its original's hash.

--to defaults to the music folder in core config (source).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to = resolveSrc(cmd, to); to == "" {
				return errors.New("--to is required (or run `core config set source <music folder>` once)")
			}
			discFolders = resolveBoolFlag(cmd, "disc-folders", "disc_folders", discFolders)
			o := librarian.Options{JournalDir: journalDir, DiscFolders: discFolders}
			if check := true; configDefault("check_duplicates", &check) && !check {
				o.SkipDuplicates = true
			}
			if online := true; !configDefault("online_art", &online) || online {
				c, err := newLibrarianArtClient()
				if err != nil {
					return err
				}
				o.Art = c
			}
			return runAdd(cmd, args[0], to, !keep, dryRun, o)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "The music folder to add to (default: source in core config)")
	cmd.Flags().BoolVar(&keep, "keep-originals", false, "Leave the originals where they are")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be added and write nothing")
	cmd.Flags().BoolVar(&discFolders, "disc-folders", false,
		"Put a flat album whose tags carry several discs into Disc N subfolders")
	cmd.Flags().StringVar(&journalDir, "journal-dir", "",
		"Where undo journals live (default <user config dir>/core/journal)")
	return cmd
}

func runAdd(cmd *cobra.Command, src, lib string, deleteOriginals, dryRun bool, o librarian.Options) error {
	out := cmd.OutOrStdout()
	plan, err := librarian.PlanAdd(cmd.Context(), src, lib, o)
	if err != nil {
		return err
	}
	printAddPlan(out, plan)
	chosen := plan.Ticked()
	switch {
	case len(chosen) == 0:
		fmt.Fprintln(out, "\nNothing to add.")
		return nil
	case dryRun:
		fmt.Fprintf(out, "\n--dry-run: nothing was copied. Without it, %s would be copied and verified; %s.\n",
			albumsWord(len(chosen)), originalsLine(deleteOriginals))
		return nil
	}

	fmt.Fprintf(out, "\nAdding %s; %s.\n", albumsWord(len(chosen)), originalsLine(deleteOriginals))
	o.Progress = func(e librarian.Event) {
		if e.Kind == librarian.EventLog && e.Job == librarian.JobAdd {
			fmt.Fprintf(out, "  %s\n", e.Text)
		}
	}
	res, err := librarian.Add(cmd.Context(), plan, chosen, deleteOriginals, o)
	if res != nil {
		fmt.Fprintf(out, "Added %s to %s", albumsWord(res.Albums()), plan.Library)
		if res.Deleted > 0 {
			fmt.Fprintf(out, "; %d original file(s) deleted", res.Deleted)
		}
		fmt.Fprintln(out, ".")
		for _, f := range res.Failures {
			fmt.Fprintf(out, "  ! %s: %s\n", f.Album, f.Reason)
		}
		if res.Journal != "" {
			fmt.Fprintf(out, "Undo: core fix --undo %s\n", res.Journal)
		}
		if res.Albums() > 0 {
			fmt.Fprintln(out, "Nothing is on the iPod yet: `core sync` copies the new albums over.")
		}
	}
	if err != nil {
		return err
	}
	if res != nil && res.Albums() < len(chosen) {
		return fmt.Errorf("%d of %d album(s) were not added", len(chosen)-res.Albums(), len(chosen))
	}
	return nil
}

// printAddPlan is the sheet the window shows, as text: the source and the
// counts, then one line per album — [x] for the ones that will be added —
// with the name it gets, its songs and size, and why when it is left out.
func printAddPlan(w io.Writer, p *librarian.AddPlan) {
	fmt.Fprintf(w, "Adding to your library (%s)\n", p.Library)
	line := fmt.Sprintf("from %s: %s, %d song(s)", p.Source, albumsWord(len(p.Albums)), p.Songs)
	if f := p.FormatsText(); f != "" {
		line += " (" + f + ")"
	}
	fmt.Fprintf(w, "%s, %s\n", line, humanBytes(p.Bytes))
	if p.Other > 0 {
		fmt.Fprintf(w, "%d other file(s) (not music, not a cover) stay where they are\n", p.Other)
	}
	fmt.Fprintln(w)
	width := 0
	for _, a := range p.Albums {
		if n := len([]rune(a.Name)); n > width && n <= 48 {
			width = n
		}
	}
	for _, a := range p.Albums {
		mark := "[ ]"
		if a.Ticked() {
			mark = "[x]"
		}
		name := a.Name
		if pad := width - len([]rune(name)); pad > 0 {
			name += strings.Repeat(" ", pad)
		}
		row := fmt.Sprintf("  %s %s  %8s  %9s", mark, name, songsWord(a.Songs), humanBytes(a.Bytes))
		if why := a.Why(); why != "" {
			row += "  " + why
		}
		fmt.Fprintln(w, strings.TrimRight(row, " "))
	}
}

func originalsLine(deleteOriginals bool) string {
	if deleteOriginals {
		return "the originals are deleted once they are safely in the library (--keep-originals leaves them)"
	}
	return "the originals stay where they are"
}

func albumsWord(n int) string {
	if n == 1 {
		return "1 album"
	}
	return fmt.Sprintf("%d albums", n)
}

func songsWord(n int) string {
	if n == 1 {
		return "1 song"
	}
	return fmt.Sprintf("%d songs", n)
}
