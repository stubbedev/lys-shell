package portaldialogs

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopentry"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

type fakeWindow struct{ closed bool }

func (w *fakeWindow) Close() { w.closed = true }

// harness runs the host with a synchronous loop: the UI work happens
// on the asking goroutine, and Open hands the tree to the test.
type harness struct {
	mu       sync.Mutex
	cfg      *config.Config
	h        *Dialogs
	opened   chan app.LayerConfig
	windows  []*fakeWindow
	openErr  error
	defaults []string
}

func newHarness(t *testing.T, apps []desktopentry.App) *harness {
	t.Helper()
	face := treetest.Font(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	hs := &harness{cfg: cfg, opened: make(chan app.LayerConfig, 4)}
	hs.h = New(Deps{
		Config: func() *config.Config { return hs.cfg },
		Open: func(c app.LayerConfig) (Window, error) {
			hs.mu.Lock()
			defer hs.mu.Unlock()
			if hs.openErr != nil {
				return nil, hs.openErr
			}
			w := &fakeWindow{}
			hs.windows = append(hs.windows, w)
			hs.opened <- c
			return w, nil
		},
		Invoke:     hs.do,
		Font:       face,
		Candidates: func([]string, string) []desktopentry.App { return apps },
		SetDefault: func(id, mime string) error {
			hs.mu.Lock()
			defer hs.mu.Unlock()
			hs.defaults = append(hs.defaults, id+"="+mime)
			return nil
		},
		Avatar: func() string { return "" },
		Describe: func(appID string) (string, string) {
			if appID == "" {
				return "An application", ""
			}
			return "App " + appID, ""
		},
	})
	return hs
}

// do runs fn as the loop: the dialog's work and the test's clicks
// take turns, as on the one UI goroutine.
func (hs *harness) do(fn func()) {
	treetest.Loop.Lock()
	defer treetest.Loop.Unlock()
	fn()
}

// open waits for the next mapped dialog.
func (hs *harness) open(t *testing.T) app.LayerConfig {
	t.Helper()
	select {
	case c := <-hs.opened:
		return c
	case <-time.After(treetest.Liveness):
		t.Fatal("no dialog opened")
		return app.LayerConfig{}
	}
}

func TestConfirmDialogsAnswer(t *testing.T) {
	hs := newHarness(t, nil)
	for _, tc := range []struct {
		click string
		want  bool
	}{{"Allow", true}, {"Deny", false}} {
		got := make(chan bool, 1)
		go func() {
			got <- hs.h.Access(portalreq.Caller{Handle: "/r/test"}, AccessRequest{Title: "Camera", Subtitle: "Sub", Body: "Body", GrantLabel: "Allow", DenyLabel: "Deny", Icon: "camera-web"})
		}()
		c := hs.open(t)
		if l := treetest.Labels(c.Root); !slices.Contains(l, "Camera") || !slices.Contains(l, "Sub\nBody") {
			t.Errorf("labels = %q", l)
		}
		// Unanchored and unsized: the compositor centers the
		// content-sized dialog, as the GTK window is.
		if c.Layer != app.LayerOverlay || c.Namespace != "lys-shell-portal-dialog" || c.Keyboard != app.KeyboardOnDemand ||
			c.Anchor != 0 || c.Width != 0 || c.Height != 0 {
			t.Errorf("surface = %+v", c)
		}
		if root, ok := c.Root.(*widget.Box); !ok || root.Element() != "window" || !widget.HasClass(root, "portal-dialog-window") {
			t.Errorf("root %T, want window.portal-dialog-window", c.Root)
		}
		if !widget.HasClass(treetest.Button(t, c.Root, "Allow"), "suggested-action") || widget.HasClass(treetest.Button(t, c.Root, "Deny"), "suggested-action") {
			t.Error("the confirm button is not the suggested action alone")
		}
		if icons := treetest.WithClass(c.Root, "portal-dialog-image"); len(icons) != 1 {
			t.Errorf("%d .portal-dialog-image headers, want the permission icon", len(icons))
		}
		hs.do(func() { treetest.Button(t, c.Root, tc.click).OnClick() })
		if r := <-got; r != tc.want {
			t.Errorf("%s answered %v", tc.click, r)
		}
	}
	hs.mu.Lock()
	closed := hs.windows[0].closed && hs.windows[1].closed
	hs.mu.Unlock()
	if !closed {
		t.Error("an answered dialog stayed mapped")
	}
}

// A request arriving while a dialog shows waits its turn instead of
// cancelling it, and shows once the first is answered; Escape answers
// no. Each dialog names the app asking.
func TestRequestsQueueAndEscape(t *testing.T) {
	hs := newHarness(t, nil)
	first := make(chan bool, 1)
	go func() { first <- hs.h.ConfirmInstall(portalreq.Caller{Handle: "/r/1", AppID: "org.one"}, "App", "") }()
	c := hs.open(t)
	if l := treetest.Labels(c.Root); !slices.Contains(l, "Install “App”?") || !slices.Contains(l, "App org.one") {
		t.Errorf("labels = %q", l)
	}
	second := make(chan bool, 1)
	go func() { second <- hs.h.Account(portalreq.Caller{Handle: "/r/2", AppID: "org.two"}, "") }()
	select {
	case r := <-first:
		t.Fatalf("a second request answered the first %v", r)
	case <-hs.opened:
		t.Fatal("the second dialog mapped over the first")
	case <-time.After(50 * time.Millisecond):
	}
	hs.do(func() { treetest.Button(t, c.Root, "Install").OnClick() })
	if r := <-first; !r {
		t.Error("the first dialog's answer was lost")
	}
	c = hs.open(t)
	if l := treetest.Labels(c.Root); !slices.Contains(l, "An application is requesting your name and avatar.") || !slices.Contains(l, "App org.two") {
		t.Errorf("account labels = %q", l)
	}
	hs.do(func() { c.OnKey(nil, escapeKeycode, 0) })
	if r := <-second; r {
		t.Error("Escape answered yes")
	}
}

// A waiting request that is dismissed is answered no and never shows;
// the one showing stays.
func TestDismissingAWaitingRequest(t *testing.T) {
	hs := newHarness(t, nil)
	first := make(chan bool, 1)
	go func() { first <- hs.h.ConfirmWallpaper(portalreq.Caller{Handle: "/r/1"}, "file:///a.png") }()
	c := hs.open(t)
	second := make(chan bool, 1)
	go func() { second <- hs.h.ConfirmWallpaper(portalreq.Caller{Handle: "/r/2"}, "file:///b.png") }()
	deadline := time.Now().Add(treetest.Liveness)
	for {
		var waiting int
		hs.do(func() { waiting = hs.h.queue.Len() })
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second request never queued")
		}
		time.Sleep(time.Millisecond)
	}
	hs.h.Dismiss("/r/2")
	if r := <-second; r {
		t.Error("a dismissed waiting request answered yes")
	}
	hs.do(func() { treetest.Button(t, c.Root, "Set wallpaper").OnClick() })
	if r := <-first; !r {
		t.Error("the showing dialog lost its answer")
	}
	select {
	case <-hs.opened:
		t.Error("a dismissed request showed")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOpenFailureAnswersNo(t *testing.T) {
	hs := newHarness(t, nil)
	hs.openErr = errors.New("no layer shell")
	if hs.h.ConfirmWallpaper(portalreq.Caller{Handle: "/r/test"}, "file:///x.png") {
		t.Error("an unmapped dialog answered yes")
	}
}

func TestWallpaperPreviewShowsTheImage(t *testing.T) {
	hs := newHarness(t, nil)
	got := make(chan bool, 1)
	go func() {
		got <- hs.h.ConfirmWallpaper(portalreq.Caller{Handle: "/r/test"}, "file:///home/u/My%20Walls/a.png")
	}()
	c := hs.open(t)
	var img *widget.Image
	treetest.Walk(c.Root, func(w widget.Widget) {
		if i, ok := w.(*widget.Image); ok {
			img = i
		}
	})
	if img == nil {
		t.Error("no preview image")
	}
	hs.do(func() { treetest.Button(t, c.Root, "Set wallpaper").OnClick() })
	if !<-got {
		t.Error("confirm answered no")
	}
}

// installApp writes a loadable entry and loads it.
func installApp(t *testing.T, dir, id, name string) desktopentry.App {
	t.Helper()
	path := filepath.Join(dir, id)
	if err := os.WriteFile(path, []byte("[Desktop Entry]\nType=Application\nName="+name+"\nExec=sh\nComment="+name+" app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, ok := desktopentry.LoadApp(id, path)
	if !ok {
		t.Fatal("entry did not load")
	}
	return a
}

func TestAppChooserFiltersAndPicks(t *testing.T) {
	dir := t.TempDir()
	apps := []desktopentry.App{installApp(t, dir, "viewer.desktop", "Image Viewer"), installApp(t, dir, "editor.desktop", "Text Editor")}
	hs := newHarness(t, apps)
	got := make(chan string, 1)
	go func() { got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, nil, "image/png", "") }()
	c := hs.open(t)
	// The search is a gtk::SearchEntry; its search-changed delay runs
	// instantly here.
	defer widget.SetAnimationsInstant(true)()
	var search *widget.SearchEntry
	var remember *widget.CheckButton
	treetest.Walk(c.Root, func(w widget.Widget) {
		switch v := w.(type) {
		case *widget.SearchEntry:
			search = v
		case *widget.CheckButton:
			remember = v
		}
	})
	if search == nil || remember == nil {
		t.Fatal("no search entry or remember box")
	}
	if l, ok := remember.Child().(*widget.Label); !ok || l.Text() != "Always use for this file type" || !widget.HasClass(remember, "portal-dialog-remember") {
		t.Errorf("remember %v, want the labeled .portal-dialog-remember checkbutton", remember.Child())
	}
	list := treetest.First[*widget.ListBox](t, c.Root)
	if !widget.HasClass(list, "portal-dialog-app-list") || list.Len() != 2 {
		t.Fatalf("app list %v with %d rows, want the two apps in .portal-dialog-app-list", list.Classes(), list.Len())
	}
	if icons := treetest.WithClass(list, "portal-dialog-app-icon"); len(icons) != 2 {
		t.Errorf("%d .portal-dialog-app-icon, want one per row", len(icons))
	}
	viewer, editor := list.RowAt(0), list.RowAt(1)
	hs.do(func() { search.SetText("text") })
	if viewer.ChildVisible() || !editor.ChildVisible() {
		t.Errorf("filter: viewer %v editor %v", viewer.ChildVisible(), editor.ChildVisible())
	}
	hs.do(func() { search.SetText("") })
	if !viewer.ChildVisible() {
		t.Error("clearing the search kept a row hidden")
	}
	hs.do(func() { remember.SetChecked(true) })
	hs.do(func() { list.OnActivate(1) })
	if id := <-got; id != "editor.desktop" {
		t.Errorf("picked %q", id)
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if !slices.Equal(hs.defaults, []string{"editor.desktop=image/png"}) {
		t.Errorf("set-default = %v", hs.defaults)
	}
}

func TestAppChooserCancelAndNoType(t *testing.T) {
	dir := t.TempDir()
	hs := newHarness(t, []desktopentry.App{installApp(t, dir, "viewer.desktop", "Image Viewer")})
	got := make(chan string, 1)
	go func() {
		got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, []string{"viewer.desktop"}, "", "")
	}()
	c := hs.open(t)
	treetest.Walk(c.Root, func(w widget.Widget) {
		if _, ok := w.(*widget.CheckButton); ok {
			t.Error("an \"always use\" box without a content type")
		}
	})
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	if id := <-got; id != "" {
		t.Errorf("cancel picked %q", id)
	}
}

// A short app list packs at the top of its scroll, not mid-view.
func TestAppChooserShortListPacksAtTheTop(t *testing.T) {
	hs := newHarness(t, []desktopentry.App{installApp(t, t.TempDir(), "viewer.desktop", "Image Viewer")})
	got := make(chan string, 1)
	go func() { got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, nil, "", "") }()
	c := hs.open(t)
	var rowY, scrollY int
	hs.do(func() {
		c.Root.Measure(widget.Constraints{Max: widget.Size{W: 1280, H: 720}})
		c.Root.Arrange(render.Rect{W: 1280, H: 720})
		rowY = treetest.First[*widget.ListBox](t, c.Root).RowAt(0).Bounds().Y
		scrollY = treetest.First[*widget.Scroll](t, c.Root).Bounds().Y
	})
	if rowY-scrollY > 8 {
		t.Errorf("the only row sits %dpx into the list, want it at the top", rowY-scrollY)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	<-got
}

// A long app description ellipsizes inside the dialog instead of
// widening it past its 420px request.
func TestAppChooserLongDescriptionKeepsTheWidth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writer.desktop")
	entry := "[Desktop Entry]\nType=Application\nName=Writer\nExec=sh\nComment=" + strings.Repeat("Create and edit text and graphics ", 6) + "\n"
	if err := os.WriteFile(path, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	long, ok := desktopentry.LoadApp("writer.desktop", path)
	if !ok {
		t.Fatal("entry did not load")
	}
	hs := newHarness(t, []desktopentry.App{long})
	got := make(chan string, 1)
	go func() { got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, nil, "", "") }()
	c := hs.open(t)
	var w int
	hs.do(func() { w = c.Root.Measure(widget.Constraints{Max: widget.Size{W: 2000, H: 2000}}).W })
	if w > surfaceWidth+80 {
		t.Errorf("dialog %dpx wide, want about its %dpx request", w, surfaceWidth)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	<-got
}

// Dismissing the request a dialog shows answers it no and takes it
// down; dismissing another request leaves it up.
func TestDismissTakesDownItsRequestOnly(t *testing.T) {
	hs := newHarness(t, nil)
	got := make(chan bool, 1)
	go func() {
		got <- hs.h.Access(portalreq.Caller{Handle: "/r/mine"}, AccessRequest{Title: "Camera", GrantLabel: "Allow", DenyLabel: "Deny"})
	}()
	hs.open(t)
	hs.h.Dismiss("/r/other")
	hs.h.Dismiss("")
	select {
	case r := <-got:
		t.Fatalf("dismissing another request answered %v", r)
	case <-time.After(50 * time.Millisecond):
	}
	hs.h.Dismiss("/r/mine")
	select {
	case r := <-got:
		if r {
			t.Error("a dismissed dialog answered yes")
		}
	case <-time.After(treetest.Liveness):
		t.Fatal("Dismiss left the dialog waiting")
	}
	hs.mu.Lock()
	closed := hs.windows[0].closed
	hs.mu.Unlock()
	if !closed {
		t.Error("a dismissed dialog stayed mapped")
	}
}
