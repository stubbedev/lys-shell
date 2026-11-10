package modes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-desktop/fileuri"
	"github.com/stubbedev/lys-desktop/opener/openertest"
)

func TestDrunLinksOpenWithTheBuiltInOpenerByDefault(t *testing.T) {
	browser := openertest.New(t).Handler(t, "browser", "x-scheme-handler/https")
	if err := NewDrun(DefaultDrunConfig(), nil).launch(drunEntry{isLink: true, url: "https://example.com/a b"}); err != nil {
		t.Fatal(err)
	}
	if got := browser.Ran(t); got != "https://example.com/a b|" {
		t.Errorf("the https handler ran with %q", got)
	}
}

func TestDrunLinksOpenWithTheConfiguredURLLauncher(t *testing.T) {
	desktop := openertest.New(t)
	browser := desktop.Handler(t, "browser", "x-scheme-handler/https")
	mine := desktop.Tool(t, "mylauncher")
	cfg := DefaultDrunConfig()
	cfg.URLLauncher = "mylauncher --flag"
	if err := NewDrun(cfg, nil).launch(drunEntry{isLink: true, url: "https://example.com/'q'"}); err != nil {
		t.Fatal(err)
	}
	if got := mine.Ran(t); got != "--flag https://example.com/'q'|" {
		t.Errorf("the configured launcher ran with %q", got)
	}
	if browser.HasRun() {
		t.Error("the default handler ran beside the configured launcher")
	}
}

func TestFileBrowserOpensFilesWithTheBuiltInOpenerByDefault(t *testing.T) {
	files := openertest.New(t).Handler(t, "files", "inode/directory")
	dir := filepath.Join(t.TempDir(), "a dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	wantClosed(t, NewFileBrowser(DefaultFileBrowserConfig()).open(dir))
	if got := files.Ran(t); got != fileuri.FromPath(dir)+"|" {
		t.Errorf("the directory handler ran with %q, want the directory's file URI", got)
	}
}
