package pulse

import (
	"context"
	"fmt"

	"github.com/stubbedev/lys-shell/service/pulse/native"
)

// PlaySample plays the sound name on the default sink through the
// server's sample cache: the first play on a connection uploads what load
// returns, every later one sends only the name. props describe the
// playback (media.role and friends), so it follows the user's routing.
func (s *Service) PlaySample(ctx context.Context, name string, props native.PropList, load func() (native.Sample, error)) error {
	s.mu.RLock()
	sess := s.sess
	_, err := s.currentLocked()
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("pulse: play %s: %w", name, err)
	}
	sess.samplesMu.Lock()
	defer sess.samplesMu.Unlock()
	if !sess.samples[name] {
		sample, err := load()
		if err != nil {
			return fmt.Errorf("pulse: play %s: %w", name, err)
		}
		if err := sess.conn.UploadSample(ctx, name, sample); err != nil {
			return err
		}
		if sess.samples == nil {
			sess.samples = map[string]bool{}
		}
		sess.samples[name] = true
	}
	return sess.conn.PlaySample(ctx, name, props)
}
