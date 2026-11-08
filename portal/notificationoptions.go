package portal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/service/notifications"
)

// replyPurpose is the button purpose of a messenger's reply button, which
// the daemon draws as an inline reply field.
const replyPurpose = "im.reply-with-text"

// defaultSoundName is the theme sound a sound="default" notification
// plays.
const defaultSoundName = "message-new-instant"

// maxSoundBytes bounds a sound an app hands over as a file descriptor.
const maxSoundBytes = 8 << 20

// requirement is a category or button purpose and the daemon capability
// it needs to mean anything.
type requirement struct{ name, capability string }

var buttonPurposes = []requirement{
	{replyPurpose, "inline-reply"},
	{"call.accept", "actions"},
	{"call.decline", "actions"},
	{"call.hang-up", "actions"},
}

var categories = []requirement{
	{"im.received", "body"},
	{"call.incoming", "persistence"},
	{"call.ongoing", "persistence"},
	{"call.missed", "body"},
	{"alarm.ringing", "persistence"},
	{"alarm.pending", "body"},
}

// ringingCategories stay up until acted on, at critical urgency, which
// do-not-disturb's allow-critical lets through.
var ringingCategories = []string{"call.incoming", "alarm.ringing"}

func ringing(notification Vardict) bool {
	category, _ := optString(notification, "category")
	return slices.Contains(ringingCategories, category)
}

// supportedOptions lists the categories and button purposes whose needs
// the daemon's capabilities meet now.
func (n *notifier) supportedOptions() Vardict {
	ctx, cancel := context.WithTimeout(context.Background(), shellCallTimeout)
	defer cancel()
	var caps []string
	if err := n.daemon().CallWithContext(ctx, notifications.Interface+".GetCapabilities", 0).Store(&caps); err != nil {
		warnf("notification: daemon capabilities: %v; no category or button purpose is supported", err)
	}
	met := func(reqs []requirement) []string {
		out := []string{}
		for _, r := range reqs {
			if slices.Contains(caps, r.capability) {
				out = append(out, r.name)
			}
		}
		return out
	}
	return Vardict{"category": dbus.MakeVariant(met(categories)), "button-purpose": dbus.MakeVariant(met(buttonPurposes))}
}

// soundHints maps the v2 sound option: "silent" suppresses the sound,
// "default" plays the theme's message sound, and ("file", h) plays the
// app's file, copied once into the runtime directory under its content
// hash, since the descriptor dies with the call.
func (n *notifier) soundHints(appID string, notification Vardict, hints map[string]dbus.Variant) {
	v, ok := notification["sound"]
	if !ok {
		return
	}
	if name, ok := v.Value().(string); ok {
		switch name {
		case "silent":
			hints["suppress-sound"] = dbus.MakeVariant(true)
		case "default":
			hints["sound-name"] = dbus.MakeVariant(defaultSoundName)
		}
		return
	}
	kind, inner, ok := serializedIcon(v)
	if !ok || kind != "file" {
		return
	}
	fd, ok := inner.Value().(dbus.UnixFD)
	if !ok {
		warnf("notification: sound for %s: a file sound without a descriptor", appID)
		return
	}
	path, err := n.sounds.store(fd)
	if err != nil {
		warnf("notification: sound for %s: %v", appID, err)
		return
	}
	hints["sound-file"] = dbus.MakeVariant(path)
}

// soundCache keeps the sound files apps hand over, one file per content.
type soundCache struct {
	dir    func() (string, bool)
	mu     sync.Mutex
	writes int
}

func newSoundCache() *soundCache {
	return &soundCache{dir: func() (string, bool) {
		dir, ok := xdg.OS.RuntimeDir(identity.Name)
		return filepath.Join(dir, "sounds"), ok
	}}
}

// store copies the sound behind fd into the cache, once per content, and
// returns its path.
func (c *soundCache) store(fd dbus.UnixFD) (string, error) {
	f, err := dbusx.File(fd, "sound")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSoundBytes+1))
	if err != nil {
		return "", fmt.Errorf("read the sound: %w", err)
	}
	if len(data) > maxSoundBytes {
		return "", fmt.Errorf("sound larger than %d bytes", maxSoundBytes)
	}
	dir, ok := c.dir()
	if !ok {
		return "", errors.New("no runtime directory to keep the sound in: XDG_RUNTIME_DIR is not an absolute path")
	}
	sum := sha256.Sum256(data)
	path := filepath.Join(dir, hex.EncodeToString(sum[:]))
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("make %s: %w", dir, err)
	}
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("keep the sound: %w", err)
	}
	c.writes++
	return path, nil
}
