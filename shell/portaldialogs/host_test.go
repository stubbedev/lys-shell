package portaldialogs

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

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
	// picked is the picture the file chooser answers, "" for a cancel.
	picked string
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
		Invoke: hs.do,
		Font:   face,
		Candidates: func(choices []string, _ string) []desktopentry.App {
			if len(choices) == 0 {
				return apps
			}
			var out []desktopentry.App
			for _, id := range choices {
				if i := slices.IndexFunc(apps, func(a desktopentry.App) bool { return a.AppID() == id }); i >= 0 {
					out = append(out, apps[i])
				}
			}
			return out
		},
		SetDefault: func(id, mime string) error {
			hs.mu.Lock()
			defer hs.mu.Unlock()
			hs.defaults = append(hs.defaults, id+"="+mime)
			return nil
		},
		PickImage: func(chosen func(string)) {
			hs.mu.Lock()
			picked := hs.picked
			hs.mu.Unlock()
			if picked != "" {
				chosen(picked)
			}
		},
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
			ok, _ := hs.h.Access(portalreq.Caller{Handle: "/r/test"}, AccessRequest{Title: "Camera", Subtitle: "Sub", Body: "Body", GrantLabel: "Allow", DenyLabel: "Deny", Icon: "camera-web"})
			got <- ok
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
	go func() {
		ok, _ := hs.h.ConfirmInstall(portalreq.Caller{Handle: "/r/1", AppID: "org.one"}, LauncherRequest{Name: "App"})
		first <- ok
	}()
	c := hs.open(t)
	if l := treetest.Labels(c.Root); !slices.Contains(l, "Install “App”?") || !slices.Contains(l, "App org.one") {
		t.Errorf("labels = %q", l)
	}
	second := make(chan bool, 1)
	go func() {
		ok, _ := hs.h.Account(portalreq.Caller{Handle: "/r/2", AppID: "org.two"}, "", AccountInfo{Name: "Ada"})
		second <- ok
	}()
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
	apps := []desktopentry.App{installApp(t, dir, "org.example.Viewer.desktop", "Image Viewer"), installApp(t, dir, "org.example.Editor.desktop", "Text Editor")}
	hs := newHarness(t, apps)
	got := make(chan string, 1)
	go func() {
		got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, ChooserRequest{ContentType: "image/png"})
	}()
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
	// The answer is the portal's app id; the default is recorded for
	// the desktop file.
	if id := <-got; id != "org.example.Editor" {
		t.Errorf("picked %q", id)
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if !slices.Equal(hs.defaults, []string{"org.example.Editor.desktop=image/png"}) {
		t.Errorf("set-default = %v", hs.defaults)
	}
}

func TestAppChooserCancelAndNoType(t *testing.T) {
	dir := t.TempDir()
	hs := newHarness(t, []desktopentry.App{installApp(t, dir, "org.example.Viewer.desktop", "Image Viewer")})
	got := make(chan string, 1)
	go func() {
		got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, ChooserRequest{Choices: []string{"org.example.Viewer"}})
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
	go func() { got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, ChooserRequest{}) }()
	c := hs.open(t)
	var rowY, scrollY int
	hs.do(func() {
		widget.SizeWithin(c.Root, widget.Size{W: 1280, H: 720})
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
	go func() { got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, ChooserRequest{}) }()
	c := hs.open(t)
	var w int
	hs.do(func() { w = widget.SizeWithin(c.Root, widget.Size{W: 2000, H: 2000}).W })
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
		ok, _ := hs.h.Access(portalreq.Caller{Handle: "/r/mine"}, AccessRequest{Title: "Camera", GrantLabel: "Allow", DenyLabel: "Deny"})
		got <- ok
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

// Access shows a check button per yes/no choice and a dropdown per list
// choice, starting at their initial values, and a grant returns what the
// user left them at; a deny returns nothing.
func TestAccessChoices(t *testing.T) {
	hs := newHarness(t, nil)
	req := AccessRequest{Title: "Camera", GrantLabel: "Allow", DenyLabel: "Deny", Choices: []portalreq.Choice{
		{ID: "remember", Label: "Remember this", Initial: "false"},
		{ID: "scope", Label: "Scope", Options: []portalreq.Option{{ID: "once", Label: "Once"}, {ID: "always", Label: "Always"}}, Initial: "always"},
	}}
	type answer struct {
		ok     bool
		picked []portalreq.Selection
	}
	for _, grant := range []bool{true, false} {
		got := make(chan answer, 1)
		go func() {
			ok, picked := hs.h.Access(portalreq.Caller{Handle: "/r/c"}, req)
			got <- answer{ok, picked}
		}()
		c := hs.open(t)
		checks := treetest.All[*widget.CheckButton](c.Root)
		drops := treetest.All[*widget.Dropdown](c.Root)
		if len(checks) != 1 || len(drops) != 1 || checks[0].Checked() || drops[0].Selected() != 1 {
			t.Fatalf("%d checks, %d dropdowns at their initial values?", len(checks), len(drops))
		}
		hs.do(func() {
			checks[0].SetChecked(true)
			drops[0].SetSelected(0)
			label := "Deny"
			if grant {
				label = "Allow"
			}
			treetest.Button(t, c.Root, label).OnClick()
		})
		r := <-got
		if !grant {
			if r.ok || r.picked != nil {
				t.Errorf("deny = %+v", r)
			}
			continue
		}
		want := []portalreq.Selection{{ID: "remember", Value: "true"}, {ID: "scope", Value: "once"}}
		if !r.ok || !slices.Equal(r.picked, want) {
			t.Errorf("grant = %+v, want %+v", r, want)
		}
	}
}

// The account dialog offers the name and picture for editing: the
// shared info is what the user left, a removed picture is none, a
// chosen one replaces it, and a cleared name keeps the original.
func TestAccountEditsBeforeSharing(t *testing.T) {
	hs := newHarness(t, nil)
	type answer struct {
		ok   bool
		info AccountInfo
	}
	run := func(edit func(c app.LayerConfig)) answer {
		t.Helper()
		got := make(chan answer, 1)
		go func() {
			ok, info := hs.h.Account(portalreq.Caller{Handle: "/r/a", AppID: "org.app"}, "why", AccountInfo{Name: "Ada Lovelace", Image: "/home/ada/.face"})
			got <- answer{ok, info}
		}()
		c := hs.open(t)
		edit(c)
		hs.do(func() { treetest.Button(t, c.Root, "Share").OnClick() })
		return <-got
	}
	entry := func(c app.LayerConfig) *widget.Entry { return treetest.First[*widget.Entry](t, c.Root) }

	r := run(func(c app.LayerConfig) {
		if e := entry(c); e.Text() != "Ada Lovelace" {
			t.Errorf("name entry = %q", e.Text())
		}
		hs.do(func() {
			entry(c).SetText("  Ada  ")
			treetest.Button(t, c.Root, "Remove picture").OnClick()
		})
	})
	if !r.ok || r.info != (AccountInfo{Name: "Ada"}) {
		t.Errorf("edited = %+v", r)
	}
	hs.mu.Lock()
	hs.picked = "/pics/cat.png"
	hs.mu.Unlock()
	r = run(func(c app.LayerConfig) {
		hs.do(func() {
			entry(c).SetText("")
			treetest.Button(t, c.Root, "Remove picture").OnClick()
			treetest.Button(t, c.Root, "Choose picture…").OnClick()
		})
	})
	if !r.ok || r.info != (AccountInfo{Name: "Ada Lovelace", Image: "/pics/cat.png"}) {
		t.Errorf("chosen = %+v", r)
	}
}

// The portal names apps by app id, the desktop id without ".desktop":
// a choice lists its entry, and a desktop-file id matches none.
func TestCandidateAppsTakeAppIDs(t *testing.T) {
	data := t.TempDir()
	apps := filepath.Join(data, "applications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	installApp(t, apps, "org.example.Viewer.desktop", "Image Viewer")
	installApp(t, apps, "org.example.Editor.desktop", "Text Editor")
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	var ids []string
	for _, a := range CandidateApps([]string{"org.example.Editor", "org.example.Viewer"}, "") {
		ids = append(ids, a.AppID())
	}
	if !slices.Equal(ids, []string{"org.example.Editor", "org.example.Viewer"}) {
		t.Errorf("candidates = %q, want both choices in order", ids)
	}
	if got := CandidateApps([]string{"org.example.Viewer.desktop"}, ""); len(got) != 0 {
		t.Errorf("a desktop-file id choice listed %v", got)
	}
}

// chooserApps are two installed apps for the chooser tests.
func chooserApps(t *testing.T) []desktopentry.App {
	dir := t.TempDir()
	return []desktopentry.App{installApp(t, dir, "org.example.Viewer.desktop", "Image Viewer"), installApp(t, dir, "org.example.Editor.desktop", "Text Editor")}
}

// The last choice is selected, and Open answers the selected app; with
// no last choice listed nothing is selected and Open answers nothing.
func TestAppChooserPreselectsTheLastChoice(t *testing.T) {
	hs := newHarness(t, chooserApps(t))
	got := make(chan string, 1)
	go func() {
		got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, ChooserRequest{LastChoice: "org.example.Editor"})
	}()
	c := hs.open(t)
	if sel := treetest.First[*widget.ListBox](t, c.Root).Selected(); sel != 1 {
		t.Errorf("selected row %d, want the last choice's (1)", sel)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Open").OnClick() })
	select {
	case id := <-got:
		if id != "org.example.Editor" {
			t.Errorf("Open answered %q", id)
		}
	case <-time.After(treetest.Liveness):
		t.Fatal("Open answered nothing")
	}

	go func() {
		got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test2"}, ChooserRequest{LastChoice: "org.example.Gone"})
	}()
	c = hs.open(t)
	if sel := treetest.First[*widget.ListBox](t, c.Root).Selected(); sel != -1 {
		t.Errorf("selected row %d for a last choice not listed", sel)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Open").OnClick() })
	select {
	case id := <-got:
		t.Fatalf("Open with nothing selected answered %q", id)
	case <-time.After(50 * time.Millisecond):
	}
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	<-got
}

// UpdateChoices refills the chooser showing, keeping the selection,
// and reaches a chooser still waiting; it finds no chooser for another
// request or once the request is answered.
func TestAppChooserUpdateChoices(t *testing.T) {
	hs := newHarness(t, chooserApps(t))
	got := make(chan string, 2)
	viewer := ChooserRequest{Choices: []string{"org.example.Viewer"}, LastChoice: "org.example.Viewer"}
	go func() { got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/a"}, viewer) }()
	c := hs.open(t)
	list := treetest.First[*widget.ListBox](t, c.Root)
	if list.Len() != 1 {
		t.Fatalf("%d rows, want the one choice", list.Len())
	}
	if !hs.h.UpdateChoices("/r/a", []string{"org.example.Editor", "org.example.Viewer"}) {
		t.Fatal("UpdateChoices did not find the chooser showing")
	}
	if list.Len() != 2 || list.Selected() != 1 {
		t.Errorf("after the update: %d rows, row %d selected; want 2, the viewer (1)", list.Len(), list.Selected())
	}
	if hs.h.UpdateChoices("/r/other", []string{"org.example.Editor"}) {
		t.Error("UpdateChoices found a chooser for another request")
	}

	go func() { got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/b"}, viewer) }()
	waitFor(t, func() bool { return hs.h.UpdateChoices("/r/b", []string{"org.example.Editor"}) })
	hs.do(func() { list.OnActivate(0) })
	if id := <-got; id != "org.example.Editor" {
		t.Errorf("the updated chooser answered %q", id)
	}
	if hs.h.UpdateChoices("/r/a", nil) {
		t.Error("UpdateChoices found a chooser already answered")
	}
	c = hs.open(t)
	var names []string
	for _, l := range treetest.WithClass(c.Root, "portal-dialog-app-name") {
		names = append(names, l.(*widget.Label).Text())
	}
	if !slices.Equal(names, []string{"Text Editor"}) {
		t.Errorf("the waiting chooser shows %q, want its updated choice", names)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
	<-got
}

// The file or URI opened is named; with neither there is no line.
func TestAppChooserNamesTheTarget(t *testing.T) {
	hs := newHarness(t, chooserApps(t))
	for target, want := range map[string]bool{"report.pdf": true, "": false} {
		got := make(chan string, 1)
		go func() {
			got <- hs.h.ChooseApplication(portalreq.Caller{Handle: "/r/test"}, ChooserRequest{Target: target})
		}()
		c := hs.open(t)
		line := "Choose an application to open “" + target + "”."
		if has := slices.ContainsFunc(treetest.Labels(c.Root), func(l string) bool { return strings.HasPrefix(l, "Choose an application") }); has != want || want && !slices.Contains(treetest.Labels(c.Root), line) {
			t.Errorf("target %q: labels %q", target, treetest.Labels(c.Root))
		}
		hs.do(func() { treetest.Button(t, c.Root, "Cancel").OnClick() })
		<-got
	}
}

// waitFor polls cond until it holds, failing after treetest.Liveness.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(treetest.Liveness); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
	}
}

// pngBytes is a small encoded PNG.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// installDialog shows ConfirmInstall for r and answers it with click,
// after inspect looks at the dialog.
func installDialog(t *testing.T, hs *harness, r LauncherRequest, inspect func(app.LayerConfig), click string) (bool, string) {
	t.Helper()
	type answer struct {
		ok   bool
		name string
	}
	got := make(chan answer, 1)
	go func() {
		ok, name := hs.h.ConfirmInstall(portalreq.Caller{Handle: "/r/test"}, r)
		got <- answer{ok, name}
	}()
	c := hs.open(t)
	inspect(c)
	hs.do(func() { treetest.Button(t, c.Root, click).OnClick() })
	a := <-got
	return a.ok, a.name
}

// An editable name is in an entry and the edit comes back; emptied, the
// default name does. A fixed name has no entry.
func TestConfirmInstallEditsTheName(t *testing.T) {
	hs := newHarness(t, nil)
	for edit, want := range map[string]string{"My Launcher": "My Launcher", "  ": "App"} {
		ok, name := installDialog(t, hs, LauncherRequest{Name: "App", EditableName: true}, func(c app.LayerConfig) {
			entry := treetest.First[*widget.Entry](t, c.Root)
			if entry.Text() != "App" {
				t.Errorf("entry starts at %q", entry.Text())
			}
			hs.do(func() { entry.SetText(edit) })
		}, "Install")
		if !ok || name != want {
			t.Errorf("edited to %q: %v %q, want %q", edit, ok, name, want)
		}
	}
	ok, name := installDialog(t, hs, LauncherRequest{Name: "App"}, func(c app.LayerConfig) {
		if len(treetest.All[*widget.Entry](c.Root)) != 0 || !slices.Contains(treetest.Labels(c.Root), "Install “App”?") {
			t.Errorf("a fixed name: entries %d, labels %q", len(treetest.All[*widget.Entry](c.Root)), treetest.Labels(c.Root))
		}
	}, "Install")
	if !ok || name != "App" {
		t.Errorf("fixed name answered %v %q", ok, name)
	}
	if ok, name := installDialog(t, hs, LauncherRequest{Name: "App", EditableName: true}, func(app.LayerConfig) {}, "Cancel"); ok || name != "" {
		t.Errorf("cancel answered %v %q", ok, name)
	}
}

// A web app's URL is shown; an application shows none.
func TestConfirmInstallShowsTheTarget(t *testing.T) {
	hs := newHarness(t, nil)
	for target, want := range map[string]bool{"https://example.org/app": true, "": false} {
		installDialog(t, hs, LauncherRequest{Name: "App", Target: target}, func(c app.LayerConfig) {
			has := slices.ContainsFunc(treetest.Labels(c.Root), func(l string) bool { return strings.HasPrefix(l, "Opens ") })
			if has != want || want && !slices.Contains(treetest.Labels(c.Root), "Opens "+target) {
				t.Errorf("target %q: labels %q", target, treetest.Labels(c.Root))
			}
		}, "Cancel")
	}
}

// The icon's bytes are drawn: a raster as an image, an SVG as an icon,
// none as nothing.
func TestConfirmInstallDrawsTheIcon(t *testing.T) {
	hs := newHarness(t, nil)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="4" height="4"/></svg>`)
	for _, tc := range []struct {
		name         string
		icon         []byte
		images, svgs int
	}{{"png", pngBytes(t), 1, 0}, {"svg", svg, 0, 1}, {"none", nil, 0, 0}} {
		installDialog(t, hs, LauncherRequest{Name: "App", Icon: tc.icon}, func(c app.LayerConfig) {
			header := treetest.WithClass(c.Root, "portal-dialog-image")
			var images, svgs int
			for _, w := range header {
				switch w.(type) {
				case *widget.Image:
					images++
				case *widget.Icon:
					svgs++
				}
			}
			if images != tc.images || svgs != tc.svgs {
				t.Errorf("%s: %d images, %d icons in the header; want %d, %d", tc.name, images, svgs, tc.images, tc.svgs)
			}
		}, "Cancel")
	}
}

// Each device is a checked box naming it and the access asked; Allow
// answers the ones left checked, Deny answers none.
func TestChooseDevicesAnswersTheChecked(t *testing.T) {
	hs := newHarness(t, nil)
	devices := []Device{{ID: "d0", Name: "Pad", Writable: true}, {ID: "d1", Name: "Key"}}
	type answer struct {
		ok  bool
		ids []string
	}
	for _, tc := range []struct {
		click   string
		uncheck int
		want    answer
	}{
		{"Allow", -1, answer{true, []string{"d0", "d1"}}},
		{"Allow", 0, answer{true, []string{"d1"}}},
		{"Deny", -1, answer{false, nil}},
	} {
		got := make(chan answer, 1)
		go func() {
			ok, ids := hs.h.ChooseDevices(portalreq.Caller{Handle: "/r/test"}, devices)
			got <- answer{ok, ids}
		}()
		c := hs.open(t)
		checks := treetest.All[*widget.CheckButton](c.Root)
		if len(checks) != 2 || !checks[0].Checked() || !checks[1].Checked() {
			t.Fatalf("%d boxes, want two checked", len(checks))
		}
		for i, want := range []string{"Pad — read and write", "Key — read only"} {
			if l, ok := checks[i].Child().(*widget.Label); !ok || l.Text() != want {
				t.Errorf("box %d labelled %v, want %q", i, checks[i].Child(), want)
			}
		}
		if tc.uncheck >= 0 {
			hs.do(func() { checks[tc.uncheck].SetChecked(false) })
		}
		hs.do(func() { treetest.Button(t, c.Root, tc.click).OnClick() })
		if a := <-got; a.ok != tc.want.ok || !slices.Equal(a.ids, tc.want.ids) {
			t.Errorf("%s unchecking %d = %+v, want %+v", tc.click, tc.uncheck, a, tc.want)
		}
	}
}

// A requesting app's icon resolves through GTK's dash fallbacks; a
// path loads as a file.
func TestDialogIconFallsBackAlongTheName(t *testing.T) {
	treetest.IconTheme(t, "org.example")
	h := &Dialogs{}
	if ink := treetest.Ink(h.icon("org.example-app", 24)); ink == 0 {
		t.Error("an app icon with a present dash-shortened form drew nothing")
	}
	if h.icon("", 24) != nil {
		t.Error("no name built an icon")
	}
}

// A dialog stands on lys-ui's parts: the surface is its card, the
// requester a dim caption, the title a heading a step over the text
// size and the body dim text, Cancel an outline button and the confirm
// the suggested action; neither button takes the other's look.
func TestDialogTakesLysUiParts(t *testing.T) {
	hs := newHarness(t, nil)
	done := make(chan struct{})
	go func() {
		hs.h.Access(portalreq.Caller{Handle: "/r/p"}, AccessRequest{Title: "Camera", Body: "Use it?", GrantLabel: "Allow", DenyLabel: "Deny"})
		close(done)
	}()
	c := hs.open(t)
	one := func(class string) widget.Widget {
		t.Helper()
		ws := treetest.WithClass(c.Root, class)
		if len(ws) != 1 {
			t.Fatalf("%d .%s, want 1", len(ws), class)
		}
		return ws[0]
	}
	card := treetest.First[*widget.Card](t, c.Root)
	if !widget.HasClass(card, "portal-dialog-surface") || card.Title() != "Camera" || card.Description() != "Use it?" {
		t.Errorf("the surface is card %q / %q with classes %v, want lys-ui's card titled by the request", card.Title(), card.Description(), card.Classes())
	}
	r := one("portal-dialog-requester")
	if !widget.HasClass(r, css.Caption) || !widget.HasClass(r, css.DimLabel) {
		t.Error("the requester is not a dim caption")
	}
	if action := card.Action(); action == nil || len(treetest.WithClass(action, "portal-dialog-requester")) != 1 {
		t.Error("the requester is not at the card header's end")
	}
	cancel, confirm := one("portal-dialog-cancel"), one("portal-dialog-confirm")
	if !widget.HasClass(cancel, css.Outline) || widget.HasClass(cancel, css.SuggestedAction) {
		t.Error("Cancel is not lys-ui's outline button")
	}
	if !widget.HasClass(confirm, css.SuggestedAction) || widget.HasClass(confirm, css.Outline) {
		t.Error("the confirm is not the suggested action")
	}
	hs.do(func() { cancel.(*widget.Button).OnClick() })
	<-done
}
