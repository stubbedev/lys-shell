package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Commits group under the area their subject names, areas sorted
// ignoring case and the area-less ones last, each group in commit
// order; the release commit is left out.
func TestNotesGroupByArea(t *testing.T) {
	got := notes([]commit{
		{"a1", "lock: the screen locks"},
		{"b2", "bar: the clock ticks"},
		{"c3", "A logo of its own"},
		{"d4", "bar: the clock keeps time"},
		{"e5", "chore: release v0.1.0"},
		{"f6", "Note: not an area"},
	})
	want := "### bar\n\n- the clock ticks (b2)\n- the clock keeps time (d4)\n\n" +
		"### lock\n\n- the screen locks (a1)\n\n" +
		"### Note\n\n- not an area (f6)\n\n" +
		"### Other\n\n- A logo of its own (c3)\n"
	if got != want {
		t.Errorf("notes =\n%s\nwant\n%s", got, want)
	}
}

// A subject that only mentions a release, or names an area without the
// space after the colon, is an ordinary commit.
func TestNotesKeepNonReleaseCommits(t *testing.T) {
	got := notes([]commit{
		{"a1", "chore: release v0.1.0 notes"},
		{"b2", "see http://example.com"},
	})
	want := "### chore\n\n- release v0.1.0 notes (a1)\n\n### Other\n\n- see http://example.com (b2)\n"
	if got != want {
		t.Errorf("notes =\n%s\nwant\n%s", got, want)
	}
}

// repo is a git repository with one commit per subject, tagging the
// commits named in tags (by subject index).
func repo(t *testing.T, subjects []string, tags map[int]string) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	run("init", "-q")
	for i, s := range subjects {
		run("commit", "-q", "--allow-empty", "-m", s)
		if tag, ok := tags[i]; ok {
			run("tag", "-a", tag, "-m", tag)
		}
	}
}

func subjects(commits []commit) []string {
	out := make([]string, len(commits))
	for i, c := range commits {
		out[i] = c.subject
	}
	return out
}

// The first release takes all history; a later one only what follows
// the previous release tag, not a non-release tag in between.
func TestSinceThePreviousRelease(t *testing.T) {
	repo(t, []string{"bar: one", "bar: two", "chore: release v0.1.0", "bar: three", "chore: release v0.2.0"},
		map[int]string{1: "draft", 2: "v0.1.0", 4: "v0.2.0"})
	first, err := since("v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(subjects(first), "|"); got != "bar: one|bar: two|chore: release v0.1.0" {
		t.Errorf("first release commits = %q", got)
	}
	second, err := since("v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(subjects(second), "|"); got != "bar: three|chore: release v0.2.0" {
		t.Errorf("second release commits = %q", got)
	}
}

// A tag that does not exist is an error naming it, not empty notes.
func TestSinceUnknownTag(t *testing.T) {
	repo(t, []string{"bar: one"}, nil)
	_, err := since("v9.9.9")
	if err == nil || !strings.Contains(err.Error(), "v9.9.9") {
		t.Errorf("since(v9.9.9) = %v, want an error naming the tag", err)
	}
}
