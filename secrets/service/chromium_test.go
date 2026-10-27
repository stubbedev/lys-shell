package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Chromium keeps its cookie encryption key in the Secret Service: the
// first run creates "Chromium Safe Storage", a second run reads it back
// and decrypts the cookie the first one stored.
func TestChromiumKeepsItsSafeStorageKey(t *testing.T) {
	chromium := requireTool(t, "chromium")
	tool := requireTool(t, "secret-tool")
	f := newFixture(t)
	if err := f.svc.UnlockLogin([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "kept", Value: "across-runs", MaxAge: 3600})
			_, _ = fmt.Fprint(w, "<html><body>set</body></html>")
			return
		}
		_, _ = fmt.Fprintf(w, "<html><body>cookie=%s</body></html>", r.Header.Get("Cookie"))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	profile, home := t.TempDir(), t.TempDir()
	browse := func(path string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, chromium, "--headless=new", "--no-sandbox", "--disable-gpu",
			"--password-store=gnome-libsecret", "--user-data-dir="+profile, "--dump-dom", "http://"+ln.Addr().String()+path)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("chromium %s: %v", path, err)
		}
		return string(out)
	}
	keys := func() string {
		t.Helper()
		out, err := run(t, "", tool, "search", "--all", "application", "chromium")
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		return out
	}

	browse("/set")
	first := keys()
	if strings.Count(first, "label = Chromium Safe Storage") != 1 {
		t.Fatalf("after the first run the keyring holds:\n%s", first)
	}
	if got := browse("/read"); !strings.Contains(got, "cookie=kept=across-runs") {
		t.Errorf("the second run did not decrypt the stored cookie: %s", got)
	}
	if second := keys(); second != first {
		t.Errorf("the second run changed the key:\n%s\nwas:\n%s", second, first)
	}
}
