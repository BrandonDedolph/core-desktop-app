package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/ghrelease"
	"github.com/BrandonDedolph/core-desktop-app/internal/selfupdate"
	"github.com/BrandonDedolph/core-desktop-app/internal/version"
)

// selfUpdateBench is a program folder with core and core-app in it, this
// process pretending to be that core at running, and a fake GitHub with
// an app release tagged tag carrying this platform's pair (sums as given;
// "" means the true one).
func selfUpdateBench(t *testing.T, running, tag, appSum, cliSum string) (g *fakeGitHub, dir string) {
	t.Helper()
	g = newFakeGitHub(t)
	isolateCache(t)
	dir = t.TempDir()
	cliName, appName := progFile("core"), progFile("core-app")
	os.WriteFile(filepath.Join(dir, cliName), []byte("old core"), 0o755)
	os.WriteFile(filepath.Join(dir, appName), []byte("old core-app"), 0o755)

	prevExe, prevVer := selfExecutable, version.Version
	selfExecutable = func() (string, error) { return filepath.Join(dir, cliName), nil }
	version.Version = running
	t.Cleanup(func() { selfExecutable, version.Version = prevExe, prevVer })

	var assets []ghrelease.Asset
	for prog, sum := range map[string]string{selfupdate.App: appSum, selfupdate.CLI: cliSum} {
		n := selfupdate.AssetName(prog, runtime.GOOS, runtime.GOARCH)
		body := []byte("new " + prog)
		if sum == "" {
			h := sha256.Sum256(body)
			sum = hex.EncodeToString(h[:])
		}
		assets = append(assets, g.asset(n, body), g.asset(n+".sha256", []byte(sum+"  "+n+"\n")))
	}
	g.publish(ghrelease.Release{Tag: tag, URL: "https://github.com/owner/name/releases/tag/" + tag, Assets: assets})
	return g, dir
}

func TestSelfUpdateCheckSaysAndChangesNothing(t *testing.T) {
	g, dir := selfUpdateBench(t, "v0.3.0", "v0.4.0", "", "")
	out, _, err := runCore(t, "self-update", "--check", "--repo", "owner/name")
	if err != nil {
		t.Fatalf("self-update --check: %v", err)
	}
	for _, want := range []string{
		"this:   v0.3.0 · latest: v0.4.0",
		"page:   https://github.com/owner/name/releases/tag/v0.4.0",
		"replace: " + filepath.Join(dir, progFile("core")),
		"with " + selfupdate.AssetName("core", runtime.GOOS, runtime.GOARCH) + " (",
		"checked against " + selfupdate.AssetName("core-app", runtime.GOOS, runtime.GOARCH) + ".sha256",
		"--check: nothing was downloaded and nothing was replaced.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--check did not print %q:\n%s", want, out)
		}
	}
	if g.hits() != 0 {
		t.Errorf("--check downloaded %d files", g.hits())
	}
	if b, _ := os.ReadFile(selfExe(t)); string(b) != "old core" {
		t.Errorf("--check replaced core: %q", b)
	}
}

// progFile is a program's file name on this OS.
func progFile(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

func selfExe(t *testing.T) string {
	p, _ := selfExecutable()
	return p
}

func TestSelfUpdateCheckUpToDateAndDevBuild(t *testing.T) {
	selfUpdateBench(t, "v0.4.0", "v0.4.0", "", "")
	out, _, err := runCore(t, "self-update", "--check", "--repo", "owner/name")
	if err != nil || !strings.Contains(out, "up to date: nothing to do.") {
		t.Errorf("same version: %v\n%s", err, out)
	}
	version.Version = "1a2b3c4-dirty"
	out, _, err = runCore(t, "self-update", "--repo", "owner/name", "--yes")
	if err != nil || !strings.Contains(out, "development build (1a2b3c4-dirty)") {
		t.Errorf("dev build: %v\n%s", err, out)
	}
}

func TestSelfUpdateReplacesBoth(t *testing.T) {
	_, dir := selfUpdateBench(t, "v0.3.0", "v0.4.0", "", "")
	out, _, err := runCore(t, "self-update", "--yes", "--repo", "owner/name")
	if err != nil {
		t.Fatalf("self-update: %v\n%s", err, out)
	}
	for _, n := range []string{"core", "core-app"} {
		if b, _ := os.ReadFile(filepath.Join(dir, progFile(n))); string(b) != "new "+n {
			t.Errorf("%s = %q", n, b)
		}
	}
	if !strings.Contains(out, "updated to v0.4.0") {
		t.Errorf("output:\n%s", out)
	}
}

// A checksum that does not match leaves both programs as they were, and
// so does answering anything but yes.
func TestSelfUpdateBadChecksumAndNoReplaceNothing(t *testing.T) {
	_, dir := selfUpdateBench(t, "v0.3.0", "v0.4.0", "", strings.Repeat("0", 64))
	_, _, err := runCore(t, "self-update", "--yes", "--repo", "owner/name")
	if err == nil || !strings.Contains(err.Error(), "nothing was replaced") {
		t.Errorf("err = %v", err)
	}
	for _, n := range []string{"core", "core-app"} {
		if b, _ := os.ReadFile(filepath.Join(dir, progFile(n))); string(b) != "old "+n {
			t.Errorf("%s = %q after a bad checksum", n, b)
		}
	}

	g, dir2 := selfUpdateBench(t, "v0.3.0", "v0.4.0", "", "")
	root := Root()
	root.SetArgs([]string{"self-update", "--repo", "owner/name"})
	root.SetIn(strings.NewReader("n\n"))
	var buf strings.Builder
	root.SetOut(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "nothing was replaced.") || g.hits() != 0 {
		t.Errorf("a no: %d downloads\n%s", g.hits(), buf.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir2, progFile("core"))); string(b) != "old core" {
		t.Errorf("a no replaced core: %q", b)
	}
}
