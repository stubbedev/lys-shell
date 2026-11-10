package notifyui

import (
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/opener/openertest"
)

func TestOpenLinkHandsTheLinkAndTokenToItsSchemesHandler(t *testing.T) {
	desktop := openertest.New(t)
	browser := desktop.Handler(t, "browser", "x-scheme-handler/https")
	mail := desktop.Handler(t, "mail", "x-scheme-handler/mailto")
	if err := OpenLink("https://example.com/x", "tok"); err != nil {
		t.Fatal(err)
	}
	if got := browser.Ran(t); got != "https://example.com/x|tok" {
		t.Errorf("the https handler ran with %q, want the link and the token", got)
	}
	if err := OpenLink("mailto:a@b.example", ""); err != nil {
		t.Fatal(err)
	}
	if got := mail.Ran(t); got != "mailto:a@b.example|" {
		t.Errorf("the mailto handler ran with %q, want the link and no token", got)
	}
}

func TestOpenLinkWithoutAHandlerNamesTheLinkAndType(t *testing.T) {
	openertest.New(t)
	err := OpenLink("https://example.com/x", "")
	if err == nil {
		t.Fatal("a link nothing handles opened")
	}
	for _, want := range []string{"https://example.com/x", "x-scheme-handler/https"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}
