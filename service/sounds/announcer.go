package sounds

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/glob"
	"github.com/stubbedev/lys-desktop/service/pulse/native"
	"github.com/stubbedev/lys-desktop/service/sounds/soundpolicy"

	"github.com/stubbedev/lys-shell/service/notifications"
)

// Player plays a sample through the audio server's cache, loading it on
// the first play (*pulse.Service).
type Player interface {
	PlaySample(ctx context.Context, p native.SamplePlay, load func() (native.Sample, error)) error
}

// playTimeout bounds one play: the audio server answers at once or not at
// all.
const playTimeout = 2 * time.Second

// Announcer plays the sound of each new notification that may make one.
type Announcer struct {
	svc    *notifications.Service
	player Player
	theme  Theme
	muted  func() bool
	decode func(string) (native.Sample, error)

	mu      sync.Mutex
	policy  soundpolicy.Policy
	found   map[string]string
	missed  map[string]bool
	decodes int
}

// NewAnnouncer plays notification sounds of svc through player, sound
// names resolving in theme; muted reports the default sink muted.
func NewAnnouncer(svc *notifications.Service, player Player, theme Theme, muted func() bool, policy soundpolicy.Policy) *Announcer {
	return &Announcer{svc: svc, player: player, theme: theme, muted: muted, decode: Decode, policy: policy, found: map[string]string{}, missed: map[string]bool{}}
}

// SetPolicy replaces the sound configuration.
func (a *Announcer) SetPolicy(p soundpolicy.Policy) {
	a.mu.Lock()
	a.policy = p
	a.mu.Unlock()
}

// Decodes counts the sound files decoded so far.
func (a *Announcer) Decodes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.decodes
}

// Run follows the service's feed until stop, playing each new
// notification's sound.
func (a *Announcer) Run(events <-chan notifications.Event) {
	for ev := range events {
		if ev.Kind == notifications.EventAdd && !ev.Replaced {
			a.Announce(ev.Notif)
		}
	}
}

// Announce plays n's sound when the policy, do-not-disturb, a share and
// the sink's mute all let it.
func (a *Announcer) Announce(n *notifications.Notification) {
	a.mu.Lock()
	policy := a.policy
	a.mu.Unlock()
	path, event, ok := a.sound(policy, n)
	if !ok {
		return
	}
	props := native.PropList{
		"media.role":       "event",
		"event.id":         event,
		"media.name":       "Notification: " + n.Summary,
		"application.name": n.AppName,
	}
	ctx, cancel := context.WithTimeout(context.Background(), playTimeout)
	defer cancel()
	if err := a.player.PlaySample(ctx, native.SamplePlay{Name: "lys-shell:" + path, Volume: native.LinearVolume(policy.Volume), Props: props}, func() (native.Sample, error) { return a.load(path) }); err != nil {
		log.Printf("sounds: %v", err)
	}
}

func (a *Announcer) load(path string) (native.Sample, error) {
	a.mu.Lock()
	a.decodes++
	a.mu.Unlock()
	return a.decode(path)
}

func (a *Announcer) sound(policy soundpolicy.Policy, n *notifications.Notification) (path, event string, ok bool) {
	switch {
	case policy.Mode == soundpolicy.Off, policy.Mode == soundpolicy.Critical && n.Urgency != notifications.UrgencyCritical:
		return "", "", false
	case n.SuppressSound, !a.svc.Audible(n), a.muted():
		return "", "", false
	}
	for _, pattern := range policy.MuteApps {
		if glob.Match(pattern, n.AppName) || (n.DesktopEntry != "" && glob.Match(pattern, n.DesktopEntry)) {
			return "", "", false
		}
	}
	if n.SoundFile != "" {
		return n.SoundFile, n.SoundName, true
	}
	name := n.SoundName
	if name == "" {
		name = policy.Default
	}
	if path, ok := a.lookup(name); ok {
		return path, name, true
	}
	if name == policy.Default {
		return "", "", false
	}
	if path, ok := a.lookup(policy.Default); ok {
		return path, policy.Default, true
	}
	return "", "", false
}

// lookup resolves name in the theme once: later calls answer from what
// the first found, and a miss is logged the first time only.
func (a *Announcer) lookup(name string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if path, ok := a.found[name]; ok {
		return path, true
	}
	if a.missed[name] {
		return "", false
	}
	path, err := a.theme.Lookup(name)
	if err != nil {
		a.missed[name] = true
		log.Printf("sounds: %q: %v; notifications asking for it play the default sound", name, err)
		return "", false
	}
	a.found[name] = path
	return path, true
}
