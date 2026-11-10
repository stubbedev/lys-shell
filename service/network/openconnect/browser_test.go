package openconnect

import (
	"errors"
	"testing"

	"github.com/stubbedev/lys-desktop/opener/openertest"

	"github.com/stubbedev/lys-shell/service/network/secrets"
)

func TestOpenInBrowserOpensTheSignInWithTheHTTPSHandler(t *testing.T) {
	browser := openertest.New(t).Handler(t, "browser", "x-scheme-handler/https")
	if err := openInBrowser("https://idp.example.com/sso?x=1"); err != nil {
		t.Fatal(err)
	}
	if got := browser.Ran(t); got != "https://idp.example.com/sso?x=1|" {
		t.Errorf("the browser ran with %q", got)
	}
}

func TestOpenInBrowserWithoutAHandlerIsAnAuthError(t *testing.T) {
	openertest.New(t)
	err := openInBrowser("https://idp.example.com/sso")
	if _, ok := errors.AsType[*secrets.AuthenticationFailedError](err); !ok {
		t.Errorf("no https handler = %v, want an authentication failure", err)
	}
}
