package config

import (
	"strings"
	"testing"
)

func TestReducedMotionTurnsEveryTransitionIntoAFade(t *testing.T) {
	a := DefaultsAnimations()
	slide, none := AnimationSlideUp, AnimationNone
	a.Transition = AnimationBounce
	a.Launcher.Enter = &slide
	a.Osd.Enter = &none
	if got := a.TransitionFor(AnimLauncher, false); got != AnimationSlideUp {
		t.Fatalf("launcher without reduced motion: %v", got)
	}
	a.ReducedMotion = true
	for _, s := range []AnimSurface{AnimNotifications, AnimDropdown, AnimLauncher, AnimPower} {
		for _, exiting := range []bool{false, true} {
			if got := a.TransitionFor(s, exiting); got != AnimationFade {
				t.Errorf("surface %d exiting %v: %v", s, exiting, got)
			}
			if a.DurationFor(s, exiting) == 0 {
				t.Errorf("surface %d exiting %v has no fade", s, exiting)
			}
		}
	}
	if got := a.TransitionFor(AnimOsd, false); got != AnimationNone {
		t.Errorf("a surface set to none: %v", got)
	}
	if a.PagesSlide() {
		t.Error("pages still slide")
	}
	if css := a.CSSOverrides(); !strings.Contains(css, "--cfg-anim-spin: 0s") || strings.Contains(css, "--duration-normal: 0ms") {
		t.Errorf("indicators should stop and colour fades stay: %s", css)
	}
	a.ReducedMotion, a.Enabled = true, false
	if got := a.TransitionFor(AnimLauncher, false); got != AnimationNone {
		t.Errorf("animations off: %v", got)
	}
}

func TestTheCompositorsReducedMotionSurvivesAReload(t *testing.T) {
	s := Load(t.TempDir(), DiscardDiagnostics)
	var heard int
	s.Subscribe(func(_, next *Config) {
		if next.Animations.Reduced() {
			heard++
		}
	})
	s.SetSessionReducedMotion(true)
	s.SetSessionReducedMotion(true)
	if heard != 1 || !s.Config().Animations.Reduced() {
		t.Fatalf("heard %d, reduced %v", heard, s.Config().Animations.Reduced())
	}
	if err := s.SetByPath("animations.duration", ParseCLIValue("99")); err != nil {
		t.Fatal(err)
	}
	if !s.Config().Animations.Reduced() {
		t.Error("a config change forgot it")
	}
	s.SetSessionReducedMotion(false)
	if s.Config().Animations.Reduced() {
		t.Error("it stays after the compositor stops asking")
	}
}
