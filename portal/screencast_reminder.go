package portal

import (
	"context"
	"strconv"
	"time"

	"github.com/stubbedev/lys-desktop/settingspage"
)

// persistPermanent is persist_mode 2: the grant outlives the app, kept
// in the permission store until revoked.
const persistPermanent uint32 = 2

const day = 24 * time.Hour

// Reminder actions: keeping the grant restarts its count, revoking it
// ends the share and, as Start fails, the frontend's token with it;
// opening the settings shows every grant on the Privacy page and shares
// meanwhile, asking again next time.
const (
	reminderKeep     = "keep"
	reminderRevoke   = "revoke"
	reminderSettings = "settings"
)

var reminderActions = []string{reminderKeep, "Keep", reminderRevoke, "Revoke", reminderSettings, "Open Settings"}

// castReminder asks again whether an app may keep sharing the screen
// without asking, once its persistent grant is days old.
type castReminder struct {
	// days is the configured age that asks again, 0 for never.
	days func() uint32
	now  func() time.Time
	// ask shows a notification and waits for the action picked, "" for
	// none (notifier.ask; a fake daemon in tests).
	ask func(ctx context.Context, appID, title, body string, actions []string) (string, error)
	// openSettings opens lys-settings on its Privacy page.
	openSettings func() error
}

// reminderVerdict is what a reminder decided about a replay.
type reminderVerdict uint8

const (
	// replayGrant shares as the grant allows.
	replayGrant reminderVerdict = iota
	// revokeGrant is the user taking the grant back.
	revokeGrant
	// withdrawnRequest is the app closing the request meanwhile.
	withdrawnRequest
)

// remind replays a persistent grant issued at issued, asking first
// when it is old enough, and returns when the grant counts from
// now on. A grant of unknown age counts from now, as does one the user
// keeps; an unanswered or failed reminder keeps the grant's age, so the
// next replay asks again.
func (r castReminder) remind(c *call, appID string, issued time.Time) (time.Time, reminderVerdict) {
	now := r.now().UTC()
	if issued.IsZero() {
		return now, replayGrant
	}
	days := r.days()
	age := int64(now.Sub(issued) / day)
	if days == 0 || age < int64(days) {
		return issued, replayGrant
	}
	key, err := r.ask(c.ctx, appID, appID+" can share your screen without asking",
		"You allowed it "+strconv.FormatInt(age, 10)+" days ago. Keep allowing it?", reminderActions)
	switch {
	case c.closed():
		return issued, withdrawnRequest
	case err != nil:
		warnf("screencast: no reminder that %s shares without asking: %v", appID, err)
		return issued, replayGrant
	case key == reminderRevoke:
		return issued, revokeGrant
	case key == reminderKeep:
		return now, replayGrant
	case key == reminderSettings:
		if err := r.openSettings(); err != nil {
			warnf("screencast: open the Privacy settings: %v", err)
		}
	}
	return issued, replayGrant
}

// castReminder asks through the backend's notifier once a grant is as
// old as the config says.
func (b *Backend) castReminder() castReminder {
	return castReminder{
		days: func() uint32 { return b.cfg.Config().Portal.ScreenCast.ReminderDays },
		now:  time.Now,
		ask:  b.notifier.ask,
		openSettings: func() error {
			return b.spawn(settingspage.Page{ID: settingspage.PrivacyID}.Argv(), nil)
		},
	}
}

// replayed is when the grant a share replays was given, asking first
// when it is an old persistent one; a share that replays nothing is a
// grant given now. Revoking it closes the session; it and the app
// closing the request end the share cancelled.
func (c *screenCast) replayed(call *call, s *session, cfg castState, restoring bool) (time.Time, uint32) {
	if !restoring || cfg.persistMode != persistPermanent {
		return c.reminder.now().UTC(), ResponseSuccess
	}
	issued, verdict := c.reminder.remind(call, s.appID, cfg.restoreIssued)
	switch verdict {
	case revokeGrant:
		c.sessions.close(s)
		return issued, ResponseCancelled
	case withdrawnRequest:
		return issued, ResponseCancelled
	}
	return issued, ResponseSuccess
}
