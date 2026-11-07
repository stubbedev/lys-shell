package pulse

import (
	"context"
	"fmt"

	"github.com/stubbedev/lys-shell/service/pulse/native"
)

// PlaySample plays p through the server's sample cache: the first play of
// p.Name on a connection uploads what load returns, every later one sends
// only the name. p.Props describe the playback (media.role and friends),
// so it follows the user's routing.
func (s *Service) PlaySample(ctx context.Context, p native.SamplePlay, load func() (native.Sample, error)) error {
	s.mu.RLock()
	sess := s.sess
	_, err := s.currentLocked()
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("pulse: play %s: %w", p.Name, err)
	}
	sess.samplesMu.Lock()
	defer sess.samplesMu.Unlock()
	if !sess.samples[p.Name] {
		sample, err := load()
		if err != nil {
			return fmt.Errorf("pulse: play %s: %w", p.Name, err)
		}
		if err := sess.conn.UploadSample(ctx, p.Name, sample); err != nil {
			return err
		}
		if sess.samples == nil {
			sess.samples = map[string]bool{}
		}
		sess.samples[p.Name] = true
	}
	return sess.conn.PlaySample(ctx, p)
}
