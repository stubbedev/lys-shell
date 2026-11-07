package bar

import (
	"log"

	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/gtksettings"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/mime"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/sounds"
)

func startSounds(svc *notifications.Service, audio *pulse.Service, cfg config.NotificationConfig) (*sounds.Announcer, func()) {
	gtk, err := gtksettings.Load(xdg.OS)
	if err != nil {
		log.Printf("sounds: %v; the %s sound theme applies where settings.ini could not be read", err, sounds.FallbackTheme)
	}
	muted := func() bool {
		out, ok := audio.DefaultOutput()
		return ok && out.Muted
	}
	a := sounds.NewAnnouncer(svc, audio, sounds.NewTheme(gtk.SoundThemeName, mime.DataDirs()), muted, cfg.SoundPolicy())
	events, stop := svc.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(events)
	}()
	return a, func() {
		stop()
		<-done
	}
}
