package notifications

import (
	"path/filepath"

	"github.com/stubbedev/lys-desktop/fileuri"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Urgency is the spec's urgency hint (types/notification.rs's Urgency).
type Urgency uint8

// Urgency levels.
const (
	UrgencyLow Urgency = iota
	UrgencyNormal
	UrgencyCritical
)

// UrgencyFromByte is From<u8>: 0 low, 2 critical, anything else normal.
func UrgencyFromByte(v byte) Urgency {
	switch v {
	case 0:
		return UrgencyLow
	case 2:
		return UrgencyCritical
	}
	return UrgencyNormal
}

// Action is one notification action: the id ActionInvoked reports, its
// label (core/types.rs's Action), and what the button does when the
// sender said (ActionPurposesHint), which styles it.
type Action struct {
	ID      string
	Label   string
	Purpose string
}

// ActionPurposesHint is lys-shell's a{ss} hint from action id to the
// button purpose a portal notification gave it (call.accept,
// call.decline, call.hang-up).
const ActionPurposesHint = "x-lys-action-purposes"

// DefaultActionID is the spec's body-click action.
const DefaultActionID = "default"

// ParseActions is parse_dbus_actions: the flat id, label, ... list as
// pairs; a trailing id without a label labels itself.
func ParseActions(raw []string) []Action {
	actions := make([]Action, 0, (len(raw)+1)/2)
	for i := 0; i < len(raw); i += 2 {
		id, label := raw[i], raw[i]
		if i+1 < len(raw) {
			label = raw[i+1]
		}
		actions = append(actions, Action{ID: id, Label: label})
	}
	return actions
}

// hints are the decoded Notify hints the shell reads (from_props).
type hints struct {
	urgency          Urgency
	imagePath        string
	desktopEntry     string
	transient        bool
	resident         bool
	progress         Progress
	stackTag         string
	replyPlaceholder string
	purposes         map[string]string
	soundFile        string
	soundName        string
	suppressSound    bool
}

// decodeHints reads the spec hints; a missing or mistyped hint keeps
// its default. Inline image data (image-data, then the deprecated
// image_data and icon_data) is cached as a PNG whose path replaces
// image-path, as normalize_hints does; cache turns pixels into a path.
func decodeHints(raw map[string]dbus.Variant, cache func(imageData) (string, bool)) hints {
	h := hints{urgency: UrgencyNormal}
	if v, ok := raw["urgency"].Value().(byte); ok {
		h.urgency = UrgencyFromByte(v)
	}
	h.imagePath, _ = raw["image-path"].Value().(string)
	h.desktopEntry, _ = raw["desktop-entry"].Value().(string)
	h.transient, _ = raw["transient"].Value().(bool)
	h.resident, _ = raw["resident"].Value().(bool)
	h.progress = progressHint(raw["value"])
	h.stackTag = stackTagHint(raw)
	h.replyPlaceholder, _ = raw["x-kde-reply-placeholder-text"].Value().(string)
	h.soundFile = soundFileHint(raw["sound-file"])
	h.soundName, _ = raw["sound-name"].Value().(string)
	h.suppressSound, _ = raw["suppress-sound"].Value().(bool)
	h.purposes, _ = raw[ActionPurposesHint].Value().(map[string]string)
	for _, key := range []string{"image-data", "image_data", "icon_data"} {
		v, ok := raw[key]
		if !ok {
			continue
		}
		img, ok := parseImageData(v)
		if !ok {
			// A malformed preferred key does not fall through to the
			// deprecated ones: IncomingHints fails that key's
			// deserialization and keeps none.
			break
		}
		if path, ok := cache(img); ok {
			h.imagePath = path
		}
		break
	}
	return h
}

// imageData is the spec's (iiibiiay) image struct.
type imageData struct {
	width, height, rowstride int32
	hasAlpha                 bool
	bitsPerSample, channels  int32
	data                     []byte
}

// parseImageData decodes an (iiibiiay) variant.
func parseImageData(v dbus.Variant) (imageData, bool) {
	fields, ok := v.Value().([]any)
	if !ok || len(fields) != 7 {
		return imageData{}, false
	}
	var img imageData
	ints := []*int32{&img.width, &img.height, &img.rowstride, nil, &img.bitsPerSample, &img.channels}
	for i, dst := range ints {
		if dst == nil {
			if img.hasAlpha, ok = fields[i].(bool); !ok {
				return imageData{}, false
			}
			continue
		}
		if *dst, ok = fields[i].(int32); !ok {
			return imageData{}, false
		}
	}
	if img.data, ok = fields[6].([]byte); !ok {
		return imageData{}, false
	}
	return img, true
}

// Progress is the value hint: a share of 0-100 percent, or none.
type Progress struct {
	percent uint8
	set     bool
}

// NewProgress is percent, clamped to 0-100 as every server draws it.
func NewProgress(percent int64) Progress {
	return Progress{percent: uint8(min(max(percent, 0), 100)), set: true}
}

// Percent is the progress; false when the notification has none.
func (p Progress) Percent() (uint8, bool) { return p.percent, p.set }

func progressHint(v dbus.Variant) Progress {
	switch n := v.Value().(type) {
	case int32:
		return NewProgress(int64(n))
	case uint32:
		return NewProgress(int64(n))
	case int64:
		return NewProgress(n)
	case uint64:
		return NewProgress(int64(min(n, 100)))
	case int16:
		return NewProgress(int64(n))
	case uint16:
		return NewProgress(int64(n))
	case byte:
		return NewProgress(int64(n))
	}
	return Progress{}
}

// The stack-tag hints: a notification carrying one replaces the sender's
// previous one with the same tag.
const (
	stackTagDunst     = "x-dunst-stack-tag"
	stackTagCanonical = "x-canonical-private-synchronous"
)

func stackTagHint(raw map[string]dbus.Variant) string {
	for _, key := range []string{stackTagDunst, stackTagCanonical} {
		if tag, ok := raw[key].Value().(string); ok && tag != "" {
			return tag
		}
	}
	return ""
}

// InlineReplyActionID is KDE's inline-reply action key: the card shows a
// reply field for it, and the reply comes back as NotificationReplied.
const InlineReplyActionID = "inline-reply"

// soundFileHint is the sound-file hint as a path: an absolute path, or a
// file URI of one; anything else names no file.
func soundFileHint(v dbus.Variant) string {
	raw, _ := v.Value().(string)
	if path, ok := fileuri.Path(raw); ok {
		raw = path
	}
	if !filepath.IsAbs(raw) {
		return ""
	}
	return filepath.Clean(raw)
}

// withPurposes is actions with the purposes the hint gave their ids.
func withPurposes(actions []Action, purposes map[string]string) []Action {
	for i := range actions {
		actions[i].Purpose = purposes[actions[i].ID]
	}
	return actions
}
