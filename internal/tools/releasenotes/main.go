// Command releasenotes prints a release's notes in Markdown: the commits
// since the previous release tag (all history for the first release),
// grouped by the area their subject names ("bar: …"). The release commit
// `just release-*` makes is left out.
//
//	releasenotes TAG
package main

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// otherArea heads the commits whose subject names no area.
const otherArea = "Other"

var (
	areaSubject   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._/-]*): (.+)$`)
	releaseCommit = regexp.MustCompile(`^chore: release v[0-9]+\.[0-9]+\.[0-9]+$`)
)

// commit is one commit's short hash and subject.
type commit struct {
	hash    string
	subject string
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: releasenotes TAG")
		os.Exit(2)
	}
	commits, err := since(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasenotes:", err)
		os.Exit(1)
	}
	fmt.Print(notes(commits))
}

// since is the commits TAG adds over the release tag before it, oldest
// first.
func since(tag string) ([]commit, error) {
	prev, err := git("tag", "--merged", tag+"^", "--list", "v*.*.*", "--sort=-v:refname")
	if err != nil {
		return nil, err
	}
	rng := tag
	if p, _, _ := strings.Cut(prev, "\n"); p != "" {
		rng = p + ".." + tag
	}
	out, err := git("log", "--no-merges", "--reverse", "--format=%h%x00%s", rng)
	if err != nil {
		return nil, err
	}
	var commits []commit
	for line := range strings.Lines(out) {
		hash, subject, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "\x00")
		if !ok {
			return nil, fmt.Errorf("git log line %q has no subject", line)
		}
		commits = append(commits, commit{hash: hash, subject: subject})
	}
	return commits, nil
}

// git runs git with args, returning its output.
func git(args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...) //nolint:gosec // argv, never a shell string
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// notes groups commits under their areas, sorted ignoring case, with
// the area-less ones last; each group keeps the commits' order.
func notes(commits []commit) string {
	groups := map[string][]string{}
	for _, c := range commits {
		if releaseCommit.MatchString(c.subject) {
			continue
		}
		area, subject := otherArea, c.subject
		if m := areaSubject.FindStringSubmatch(c.subject); m != nil {
			area, subject = m[1], m[2]
		}
		groups[area] = append(groups[area], fmt.Sprintf("- %s (%s)\n", subject, c.hash))
	}
	areas := make([]string, 0, len(groups))
	for area := range groups {
		if area != otherArea {
			areas = append(areas, area)
		}
	}
	slices.SortFunc(areas, func(a, b string) int {
		return cmp.Or(strings.Compare(strings.ToLower(a), strings.ToLower(b)), strings.Compare(a, b))
	})
	if _, ok := groups[otherArea]; ok {
		areas = append(areas, otherArea)
	}
	var b strings.Builder
	for i, area := range areas {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### %s\n\n", area)
		for _, line := range groups[area] {
			b.WriteString(line)
		}
	}
	return b.String()
}
