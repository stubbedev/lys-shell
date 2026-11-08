package wallpaper

import (
	"context"
	"fmt"

	"github.com/stubbedev/lys-desktop/service/wallpaper/extract"
	"github.com/stubbedev/lys-desktop/styling/material"
)

// sample reads the wallpaper monitor shows for the material extraction:
// the raster its surface draws, shared with it, so the file is not read
// again. It waits for the loop and for a raster still being made.
func (s *Shell) sample(ctx context.Context, monitor string) ([]material.ARGB, error) {
	type result struct {
		pixels []material.ARGB
		err    error
	}
	done := make(chan result, 1)
	s.rasters.d.invoke(func() {
		s.sampleOnLoop(monitor, func(pixels []material.ARGB, err error) { done <- result{pixels, err} })
	})
	select {
	case r := <-done:
		return r.pixels, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// sampleOnLoop hands done the monitor's wallpaper read at
// extract.SampleSide, once its raster is made. It runs on the loop.
func (s *Shell) sampleOnLoop(monitor string, done func([]material.ARGB, error)) {
	out, ok := s.outputs[monitor]
	if !ok {
		done(nil, fmt.Errorf("monitor %s has no output", monitor))
		return
	}
	st, ok := s.svc.Monitors()[monitor]
	if !ok {
		done(nil, fmt.Errorf("monitor %s is not registered", monitor))
		return
	}
	path, fit := resolve(s.cfg, monitor, st)
	box, known := outputTarget(out)
	rs := s.rasters
	rs.acquire(rasterKey{path: path, fit: fit, box: box, known: known}, func(r *raster) {
		pixels := material.Sample(r.image.RGBA, extract.SampleSide)
		rs.release(r)
		done(pixels, nil)
	}, func() { done(nil, fmt.Errorf("wallpaper %s could not be prepared", path)) })
}
