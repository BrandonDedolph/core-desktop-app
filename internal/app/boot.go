package app

import (
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/doctor"
)

// bootWords is the boot evidence as the Summary tab's "Last boot" line says
// it: a sentence, the build and when, or what has not happened yet. stale is
// true when the iPod holds an image it has not started. The time is the
// boot's own clock reading, shown in loc; nothing here is relative to now,
// so the line does not go stale while the window stays open.
func bootWords(rep doctor.BootReport, loc *time.Location) (line string, stale bool) {
	when := ""
	if at := rep.Boot.At; !at.IsZero() {
		when = "started " + at.In(loc).Format("Jan 2 at 15:04")
	}
	switch rep.State {
	case doctor.BootCurrent:
		line = "Core " + rep.Version
		if when != "" {
			line += ", " + when
		}
		return line, false
	case doctor.BootNotYet:
		return "Has not started the new firmware yet", true
	case doctor.BootUnknownBuild:
		if when == "" {
			return "Started, but which build is not recorded", false
		}
		return capitalize(when) + " — which build is not recorded", false
	case doctor.BootNone:
		return "No start recorded in the iPod's log yet", false
	}
	return "", false
}

func capitalize(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
