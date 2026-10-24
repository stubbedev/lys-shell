package version

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// semver is MAJOR.MINOR.PATCH with an optional pre-release.
var semver = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

func TestVersionIsSemver(t *testing.T) {
	if !semver.MatchString(Version) {
		t.Fatalf("Version %q is not MAJOR.MINOR.PATCH", Version)
	}
	for _, bad := range []string{"0.8", "v0.8.54", "0.8.54 ", "0.8.x"} {
		if semver.MatchString(bad) {
			t.Errorf("the semver pattern accepts %q", bad)
		}
	}
}

// nix/lys-shell.nix reads the version off the line that starts with
// `const Version = "` and ends with its closing quote; the source must
// keep that one line.
func TestVersionLineIsWhatNixReads(t *testing.T) {
	src, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatal(err)
	}
	want := `const Version = "` + Version + `"`
	n := 0
	for line := range strings.SplitSeq(string(src), "\n") {
		if strings.HasPrefix(line, `const Version = "`) {
			n++
			if line != want {
				t.Errorf("version line %q, want %q", line, want)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d lines start `const Version = \"`, want exactly one", n)
	}
}
