package bar

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/useraccount"
)

// sessionCardTree walks the section into its session Item, its avatar
// tile, and its buttons.
func sessionCardTree(t *testing.T, card widget.Widget) (row *widget.Item, avatar *widget.Box, btns []*widget.Button) {
	t.Helper()
	cardBox, ok := card.(*widget.Box)
	if !ok || !cardBox.HasClass("card") || !cardBox.HasClass("dashboard-card") {
		t.Fatalf("the section root = %T %v, want the card dashboard-card", card, card)
	}
	row, ok = classedWidget(t, cardBox, "dashboard-user-session").(*widget.Item)
	if !ok {
		t.Fatal("the session row is not lys-ui's Item")
	}
	avatar = classedBox(t, row, "user-avatar")
	walkTree(row, func(w widget.Widget) bool {
		if b, ok := w.(*widget.Button); ok && b.HasClass("session-btn") {
			btns = append(btns, b)
		}
		return true
	})
	return row, avatar, btns
}

// The user session is lys-ui's Item in a card: the avatar tile as its
// media, the username as its title, and one ghost icon button per
// configured action, in order, with i18n tooltips.
func TestUserSessionIsAnItem(t *testing.T) {
	t.Setenv("USER", "lys-shell")
	t.Setenv("HOME", t.TempDir()) // no ~/.face: the user glyph
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	row, avatar, btns := sessionCardTree(t, userSessionSection(ctx))

	if len(avatar.Children()) != 1 {
		t.Fatalf("user-avatar children = %d, want the glyph", len(avatar.Children()))
	}
	glyph, isIcon := avatar.Children()[0].(*widget.Icon)
	if !isIcon || glyph.Name() != "ld-user-symbolic" || glyph.Tint() != 0 {
		t.Fatalf("the avatar = %v, want the untinted ld-user glyph", avatar.Children()[0])
	}
	if got := itemText(row, "title"); got != "lys-shell" {
		t.Errorf("username = %q, want $USER", got)
	}
	want := []config.SessionAction{config.SessionActionLock, config.SessionActionLogOut, config.SessionActionReboot, config.SessionActionPowerOff}
	keys := []string{"dropdown-dashboard-lock", "dropdown-dashboard-logout", "dropdown-dashboard-reboot", "dropdown-dashboard-power-off"}
	if len(btns) != len(want) {
		t.Fatalf("session buttons = %d, want %d", len(btns), len(want))
	}
	for i, b := range btns {
		spec := sessionButtonFor(want[i], cfg.Dashboard)
		if !b.HasClass("icon") || !b.HasClass("ghost") {
			t.Errorf("button %d carries classes %v, want a ghost icon button", i, b.Classes())
		}
		if got := b.TooltipText(); got != i18n.T(keys[i]) || spec.tooltip != got {
			t.Errorf("button %d tooltip = %q, want %q", i, got, i18n.T(keys[i]))
		}
	}
}

// The configured actions drive the buttons: a single-action session
// builds one button with that action's tooltip, and an empty list
// builds none.
func TestUserSessionButtonsFollowTheConfig(t *testing.T) {
	t.Setenv("USER", "lys-shell")
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.Dashboard.UserSession.Actions = []config.SessionAction{config.SessionActionLock}
	ctx := newTestContext(t, cfg)
	_, _, btns := sessionCardTree(t, userSessionSection(ctx))
	if len(btns) != 1 {
		t.Fatalf("buttons = %d, want 1", len(btns))
	}
	if got := btns[0].TooltipText(); got != i18n.T("dropdown-dashboard-lock") {
		t.Errorf("tooltip = %q, want %q", got, i18n.T("dropdown-dashboard-lock"))
	}

	cfg.Dashboard.UserSession.Actions = nil
	ctx = newTestContext(t, cfg)
	_, _, btns = sessionCardTree(t, userSessionSection(ctx))
	if len(btns) != 0 {
		t.Errorf("an empty action list built %d buttons", len(btns))
	}
}

// writeFace writes a 4x2 PNG at ~/.face, wide enough to exercise the
// center crop.
func writeFace(t *testing.T, home string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.White)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(home, ".face"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// The avatar tile is the face's background image (the Rust avatar's
// inline url()); without a face file it carries none and the glyph
// shows.
func TestUserSessionAvatarTakesTheFaceAsItsBackground(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USER", "lys-shell")
	t.Setenv("HOME", home)
	accountAt(t, home)
	writeFace(t, home)
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	_, avatar, _ := sessionCardTree(t, userSessionSection(ctx))
	face := filepath.Join(home, ".face")
	if !strings.Contains(avatar.InlineStyle(), `url('`+face+`')`) {
		t.Errorf("the tile carries %q, want the face's url", avatar.InlineStyle())
	}

	// No face file, no background image: the glyph shows.
	os.Remove(face)
	ctx = newTestContext(t, cfg)
	_, avatar, _ = sessionCardTree(t, userSessionSection(ctx))
	if avatar.InlineStyle() != "" {
		t.Errorf("without a face the tile carries %q", avatar.InlineStyle())
	}
}

// accountAt makes the dashboard's user one whose home is home.
func accountAt(t *testing.T, home string) {
	t.Helper()
	prev := currentAccount
	currentAccount = func() (useraccount.Account, error) {
		return useraccount.Account{Name: "lys-shell-test", RealName: "Test", Home: home}, nil
	}
	t.Cleanup(func() { currentAccount = prev })
}
