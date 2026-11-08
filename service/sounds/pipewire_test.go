package sounds

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-desktop/service/pulse/native"
	"github.com/stubbedev/lys-desktop/service/sounds/soundpolicy"
	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/service/notifications"
)

const pipewirePulseConf = `
context.properties = {
    core.daemon = true
    core.name   = pipewire-0
    support.dbus = false
}
context.spa-libs = {
    support.* = support/libspa-support
    audio.convert.* = audioconvert/libspa-audioconvert
}
context.modules = [
    { name = libpipewire-module-protocol-native }
    { name = libpipewire-module-metadata }
    { name = libpipewire-module-spa-node-factory }
    { name = libpipewire-module-client-node }
    { name = libpipewire-module-access }
    { name = libpipewire-module-adapter }
    { name = libpipewire-module-link-factory }
    { name = libpipewire-module-protocol-pulse
        args = { server.address = [ "unix:PULSE_SOCKET" ] }
    }
]
context.objects = [
    { factory = metadata
        args = {
            metadata.name = default
            metadata.values = [
                { key = default.audio.sink value = { name = test-sink } }
            ]
        }
    }
    { factory = spa-node-factory
        args = {
            factory.name    = support.node.driver
            node.name       = Dummy-Driver
            node.group      = pipewire.dummy
            priority.driver = 20000
        }
    }
    { factory = adapter
        args = {
            factory.name     = support.null-audio-sink
            node.name        = test-sink
            media.class      = Audio/Sink
            audio.position   = [ FL FR ]
            object.linger    = true
        }
    }
]
`

func startPipeWirePulse(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("pipewire")
	if err != nil {
		t.Fatalf("pipewire not on PATH (the nix dev shell carries it): %v", err)
	}
	dir := sockdir.Dir(t)
	sock := filepath.Join(dir, "pulse-native")
	conf := filepath.Join(dir, "pw.conf")
	if err := os.WriteFile(conf, []byte(strings.ReplaceAll(pipewirePulseConf, "PULSE_SOCKET", sock)), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+dir, "PIPEWIRE_RUNTIME_DIR="+dir, "PIPEWIRE_REMOTE=pipewire-0")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		if t.Failed() {
			out, _ := os.ReadFile(logPath)
			t.Logf("pipewire log:\n%s", out)
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(sock); err == nil {
			return "unix:" + sock
		}
		if time.Now().After(deadline) {
			t.Fatal("pipewire never opened its pulse socket")
		}
	}
}

func TestANotificationSoundPlaysOnAPrivatePipeWire(t *testing.T) {
	server := startPipeWirePulse(t)
	t.Setenv("PATH", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var (
		mu     sync.Mutex
		inputs []uint32
	)
	watch, err := native.Dial(ctx, native.Options{Server: server, ClientName: "watcher", OnSubscribe: func(ev native.SubscribeEvent) {
		if ev.Facility == native.FacilitySinkInput && ev.Operation == native.OpNew {
			mu.Lock()
			inputs = append(inputs, ev.Index)
			mu.Unlock()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watch.Close() })
	if err := watch.Subscribe(ctx, native.MaskSinkInput); err != nil {
		t.Fatal(err)
	}
	audio, err := pulse.Connect(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audio.Close() })
	data := t.TempDir()
	theme := filepath.Join(data, "sounds/freedesktop/stereo")
	chirp, err := os.ReadFile("testdata/chirp.oga")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(theme, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(theme, "message-new-instant.oga"), chirp, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	svc := notifications.NewService()
	a := NewAnnouncer(svc, audio, NewTheme("", []string{data}), func() bool { return false }, soundpolicy.Policy{Mode: soundpolicy.All, Default: "message-new-instant"})
	a.Announce(&notifications.Notification{ID: 1, AppName: "chat", SoundName: "message-new-instant"})
	a.Announce(&notifications.Notification{ID: 2, AppName: "chat", SoundName: "message-new-instant"})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		mu.Lock()
		n := len(inputs)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipewire started %d sample playbacks, want 2", n)
		}
	}
	if a.Decodes() != 1 {
		t.Errorf("two plays decoded %d times, want once", a.Decodes())
	}
}
