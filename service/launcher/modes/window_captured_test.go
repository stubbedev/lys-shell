package modes

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/sockdir"
)

func captured(t *testing.T, compositor, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "compositors", compositor, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type replay struct {
	mu       sync.Mutex
	requests []string
}

func (r *replay) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func (r *replay) note(request string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
}

func serve(t *testing.T, path string, handle func(net.Conn)) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				handle(conn)
			}()
		}
	}()
}

func serveHyprland(t *testing.T, replies map[string]string) *replay {
	t.Helper()
	runtime := sockdir.Dir(t)
	const signature = "captured"
	base := filepath.Join(runtime, "hypr", signature)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", signature)
	r := &replay{}
	serve(t, filepath.Join(base, ".socket.sock"), func(conn net.Conn) {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		request := string(buf[:n])
		r.note(request)
		reply, ok := replies[request]
		if !ok {
			t.Errorf("unexpected request %q", request)
			return
		}
		_, _ = conn.Write([]byte(reply))
	})
	return r
}

func titles(windows []WindowInfo) []string {
	out := make([]string, len(windows))
	for i, w := range windows {
		out[i] = w.Title
	}
	return out
}

func TestCapturedHyprlandWindows(t *testing.T) {
	const (
		missingFocusLua = `dispatch hl.dsp.focus({window = "address:0xdead"})`
		missingFocusOld = "dispatch focuswindow address:0xdead"
	)
	for _, tc := range []struct {
		compositor string
		focus      string
		shut       string
		missing    string
	}{
		{
			compositor: "hyprland-0.57.0",
			focus:      `dispatch hl.dsp.focus({window = "address:%s"})`,
			shut:       `dispatch hl.dsp.window.close({window = "address:%s"})`,
			missing:    "focus-missing-reply.txt",
		},
		{
			compositor: "hyprland-0.56.2",
			focus:      "dispatch focuswindow address:%s",
			shut:       "dispatch closewindow address:%s",
			missing:    "focus-missing-reply.txt",
		},
	} {
		t.Run(tc.compositor, func(t *testing.T) {
			clients := captured(t, tc.compositor, "clients.json")
			var raw []struct {
				Address string `json:"address"`
				Class   string `json:"class"`
			}
			if err := json.Unmarshal([]byte(clients), &raw); err != nil {
				t.Fatal(err)
			}
			if len(raw) != 3 {
				t.Fatalf("captured clients = %+v", raw)
			}
			target := raw[0].Address
			request := func(format string) string { return strings.ReplaceAll(format, "%s", target) }
			replies := map[string]string{
				"j/clients":         clients,
				"j/activeworkspace": captured(t, tc.compositor, "activeworkspace.json"),
				request(tc.focus):   "ok",
				request(tc.shut):    "ok",
			}
			if tc.compositor == "hyprland-0.57.0" {
				replies[missingFocusLua] = captured(t, tc.compositor, tc.missing)
			} else {
				invalid := captured(t, tc.compositor, "lua-dispatch-reply.txt")
				replies[`dispatch hl.dsp.focus({window = "address:`+target+`"})`] = invalid
				replies[`dispatch hl.dsp.window.close({window = "address:`+target+`"})`] = invalid
				replies[missingFocusLua] = invalid
				replies[missingFocusOld] = captured(t, tc.compositor, tc.missing)
			}
			r := serveHyprland(t, replies)
			var b hyprlandBackend

			got, err := b.List(bg)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(titles(got), []string{"live-a", "live-b", "live-c"}) {
				t.Fatalf("listed %+v", got)
			}
			if got[0].ID != target || got[0].Class != "live-a" || got[0].Workspace != "1" || !got[0].OnCurrentWorkspace {
				t.Errorf("live-a = %+v", got[0])
			}
			if !got[1].Focused || got[0].Focused || got[2].Focused {
				t.Errorf("the focused window is live-b: %+v", got)
			}
			if got[2].Workspace != "2" || got[2].OnCurrentWorkspace {
				t.Errorf("live-c lives on workspace 2: %+v", got[2])
			}

			if err := b.Focus(bg, target); err != nil {
				t.Errorf("focus: %v", err)
			}
			if err := b.Close(bg, target); err != nil {
				t.Errorf("close: %v", err)
			}
			requests := r.recorded()
			if !slices.Contains(requests, request(tc.focus)) || !slices.Contains(requests, request(tc.shut)) {
				t.Errorf("requests = %q", requests)
			}
			if err := b.Focus(bg, "0xdead"); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(captured(t, tc.compositor, tc.missing))) {
				t.Errorf("focusing a missing window = %v", err)
			}
		})
	}
}

func TestCapturedHyprland057RefusesLegacyDispatch(t *testing.T) {
	const compositor = "hyprland-0.57.0"
	r := serveHyprland(t, map[string]string{
		`dispatch hl.dsp.focus({window = "address:0x1"})`: captured(t, compositor, "focus-missing-reply.txt"),
		"dispatch focuswindow address:0x1":                captured(t, compositor, "legacy-dispatch-reply.txt"),
	})
	if err := (hyprlandBackend{}).Focus(bg, "0x1"); err == nil {
		t.Error("a window the compositor did not find is an error")
	}
	if got := r.recorded(); len(got) != 1 {
		t.Errorf("a Lua instance's failure must not retry in the legacy language: %q", got)
	}
}
