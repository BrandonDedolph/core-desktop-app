package app

import (
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/doctor"
)

func TestBootWords(t *testing.T) {
	at := time.Date(2026, 11, 1, 14, 3, 0, 0, time.UTC)
	cases := []struct {
		rep   doctor.BootReport
		line  string
		stale bool
	}{
		{doctor.BootReport{State: doctor.BootCurrent, Version: "v0.2.2", Boot: doctor.BootFacts{At: at}},
			"Core v0.2.2, started Nov 1 at 14:03", false},
		{doctor.BootReport{State: doctor.BootCurrent, Version: "v0.2.2"}, "Core v0.2.2", false},
		{doctor.BootReport{State: doctor.BootNotYet, Version: "v0.2.1", Boot: doctor.BootFacts{At: at}},
			"Has not started the new firmware yet", true},
		{doctor.BootReport{State: doctor.BootUnknownBuild, Boot: doctor.BootFacts{At: at}},
			"Started Nov 1 at 14:03 — which build is not recorded", false},
		{doctor.BootReport{State: doctor.BootUnknownBuild}, "Started, but which build is not recorded", false},
		{doctor.BootReport{State: doctor.BootNone}, "No start recorded in the iPod's log yet", false},
		{doctor.BootReport{State: doctor.BootNoLog}, "", false},
	}
	for _, tc := range cases {
		line, stale := bootWords(tc.rep, time.UTC)
		if line != tc.line || stale != tc.stale {
			t.Errorf("state %d: got %q %v, want %q %v", tc.rep.State, line, stale, tc.line, tc.stale)
		}
	}
}

func TestLastBootRow(t *testing.T) {
	u := NewUI(Options{})
	st := DemoState()
	u.SetState(st)
	if r := rowByLabel(t, u, "Last boot"); r.value != "Core v0.2.1, started Sep 24 at 20:41" ||
		r.sub != "" || len(r.actions) != 0 {
		t.Errorf("demo Last boot row = %+v", r)
	}

	st.Device.Boot, st.Device.BootStale = "Has not started the new firmware yet", true
	u.SetState(st)
	if r := rowByLabel(t, u, "Last boot"); r.sub == "" || len(r.actions) != 0 {
		t.Errorf("stale Last boot row = %+v", r)
	}

	st.Device.Boot = ""
	u.SetState(st)
	for _, r := range u.ipodRows() {
		if r.label == "Last boot" {
			t.Errorf("a Last boot row with nothing to say: %+v", r)
		}
	}
}
