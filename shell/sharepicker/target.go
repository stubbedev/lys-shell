package sharepicker

import (
	"fmt"
	"strconv"
	"strings"
)

// TargetKind is what a Target names.
type TargetKind uint8

// The kinds of target.
const (
	TargetOutput TargetKind = iota
	TargetWindow
	TargetRegion
)

// Target is one source the user picked: an output by connector name, a
// window by its ext-foreign-toplevel identifier (or XDPH id), or a
// region of an output in its logical coordinates.
type Target struct {
	Kind TargetKind
	// Name is the output connector, or the window identifier.
	Name                string
	X, Y, Width, Height int32
}

// Payload is the target as the picker replies it (and a restore token
// keeps it): screen:<name>, window:<id>, region:<out>@x,y,w,h.
func (t Target) Payload() string {
	switch t.Kind {
	case TargetWindow:
		return "window:" + t.Name
	case TargetRegion:
		return fmt.Sprintf("region:%s@%d,%d,%d,%d", t.Name, t.X, t.Y, t.Width, t.Height)
	}
	return "screen:" + t.Name
}

// ParseTarget reads one payload; anything malformed is no target.
func ParseTarget(payload string) (Target, bool) {
	if name, ok := strings.CutPrefix(payload, "screen:"); ok {
		return Target{Kind: TargetOutput, Name: name}, name != ""
	}
	if id, ok := strings.CutPrefix(payload, "window:"); ok {
		return Target{Kind: TargetWindow, Name: id}, id != ""
	}
	spec, ok := strings.CutPrefix(payload, "region:")
	if !ok {
		return Target{}, false
	}
	out, rect, ok := strings.Cut(spec, "@")
	nums := strings.Split(rect, ",")
	if !ok || out == "" || len(nums) != 4 {
		return Target{}, false
	}
	var v [4]int32
	for i, n := range nums {
		parsed, err := strconv.ParseInt(n, 10, 32)
		if err != nil {
			return Target{}, false
		}
		v[i] = int32(parsed)
	}
	if v[2] <= 0 || v[3] <= 0 {
		return Target{}, false
	}
	return Target{Kind: TargetRegion, Name: out, X: v[0], Y: v[1], Width: v[2], Height: v[3]}, true
}

// ParseReply reads the picker's answer: `[r]/<payload>[;<payload>...]`,
// r granting a restore token. Empty is a cancel; any malformed piece
// rejects the whole reply.
func ParseReply(reply string) (allowToken bool, targets []Target, ok bool) {
	flags, payload, found := strings.Cut(reply, "/")
	if reply == "" || !found {
		return false, nil, false
	}
	targets, ok = ParseTargets(payload)
	return strings.Contains(flags, "r"), targets, ok
}

// ParseTargets reads `<payload>[;<payload>...]`; any malformed piece
// rejects them all.
func ParseTargets(payload string) ([]Target, bool) {
	var targets []Target
	for piece := range strings.SplitSeq(payload, ";") {
		t, ok := ParseTarget(piece)
		if !ok {
			return nil, false
		}
		targets = append(targets, t)
	}
	return targets, true
}

// JoinTargets writes targets as ParseTargets reads them.
func JoinTargets(targets []Target) string {
	pieces := make([]string, len(targets))
	for i, t := range targets {
		pieces[i] = t.Payload()
	}
	return strings.Join(pieces, ";")
}
