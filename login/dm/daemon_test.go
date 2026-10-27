package dm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/service/auth"
)

// TestMain lets the test binary be a session worker whose PAM is a
// script: the password "pw" authenticates, anything else fails.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "worker" && os.Args[2] == "session" {
		os.Exit(fakeSessionWorker())
	}
	os.Exit(m.Run())
}

func fakeSessionWorker() int {
	ch := newChannel(os.NewFile(3, "dm"))
	var req request
	if err := ch.recv(&req); err != nil || req.Op != opAuth {
		return 1
	}
	_ = ch.send(event{Kind: evPrompt, Prompt: &auth.Prompt{Kind: auth.PromptSecret, Text: "Password:"}})
	if err := ch.recv(&req); err != nil || req.Op != opAnswer || req.Answer == nil || *req.Answer != "pw" {
		_ = ch.send(event{Kind: evFailed, Text: "Authentication failure"})
		return 0
	}
	_ = ch.send(event{Kind: evAuthenticated})
	if err := ch.recv(&req); err != nil || req.Op != opStart {
		return 0
	}
	_ = ch.send(event{Kind: evStarted})
	_ = ch.send(event{Kind: evEnded, Text: "exited"})
	return 0
}

func testOptions() Options {
	return Options{Service: "lys-login", VT: 7, Self: os.Args[0]}
}

// serve runs the daemon's side of one greeter connection.
func serve(t *testing.T) (net.Conn, <-chan *pendingLogin, <-chan error) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	logins, errs := make(chan *pendingLogin, 1), make(chan error, 1)
	go func() {
		l, err := testOptions().serveGreeter(server)
		logins <- l
		errs <- err
	}()
	return client, logins, errs
}

// The greeter's greetd client logs in through the daemon: the worker's
// prompt reaches it, its answer reaches the worker, and start_session
// hands the daemon the session to start, which then runs.
func TestGreeterLogsInThroughTheDaemon(t *testing.T) {
	client, logins, errs := serve(t)
	g := auth.NewGreetd(client, func() []string { return []string{"Hyprland"} }, []string{"LANG=C"})
	var prompts []auth.Prompt
	err := g.Run("alice", func(p auth.Prompt) (string, bool) {
		prompts = append(prompts, p)
		return "pw", true
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	login := <-logins
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Text != "Password:" {
		t.Errorf("prompts = %+v", prompts)
	}
	if !slices.Equal(login.cmd, []string{"Hyprland"}) || !slices.Equal(login.env, []string{"LANG=C"}) {
		t.Errorf("session = %v %v", login.cmd, login.env)
	}
	if err := login.run(); err != nil {
		t.Errorf("run: %v", err)
	}
}

// A wrong password is an authentication error to the greeter. The
// greeter retries on a connection of its own, which the daemon serves
// while the greeter runs, and that login goes through.
func TestAFailedLoginCanBeRetried(t *testing.T) {
	conns := make(chan net.Conn)
	exited := make(chan struct{})
	logins := make(chan *pendingLogin, 1)
	go func() {
		l, _ := testOptions().serveConns(context.Background(), conns, exited)
		logins <- l
	}()
	attempt := func(password string) error {
		server, client := net.Pipe()
		t.Cleanup(func() { _ = client.Close() })
		conns <- server
		err := auth.NewGreetd(client, func() []string { return []string{"Hyprland"} }, nil).Run("alice", func(auth.Prompt) (string, bool) { return password, true })
		if err != nil {
			_ = client.Close()
		}
		return err
	}
	if err := attempt("wrong"); err == nil || !strings.Contains(err.Error(), "Authentication failure") {
		t.Fatalf("wrong password = %v", err)
	}
	if err := attempt("pw"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	login := <-logins
	if login == nil {
		t.Fatal("no session after the retry")
	}
	if err := login.run(); err != nil {
		t.Error(err)
	}
}

// The greeter going away ends the serving without a session.
func TestServeConnsEndsWithTheGreeter(t *testing.T) {
	exited := make(chan struct{})
	close(exited)
	if l, err := testOptions().serveConns(context.Background(), make(chan net.Conn), exited); l != nil || err != nil {
		t.Errorf("serveConns = %v, %v; want neither a login nor an error", l, err)
	}
}

// Requests out of turn are errors, never a session.
func TestRequestsOutOfTurnAreRefused(t *testing.T) {
	client, _, errs := serve(t)
	read := func() string {
		t.Helper()
		var resp map[string]any
		if err := readFrame(client, &resp); err != nil {
			t.Fatal(err)
		}
		return resp["type"].(string) + ":" + strings.TrimSpace(asString(resp["error_type"]))
	}
	for _, req := range []map[string]any{
		{"type": auth.GreetdStartSession, "cmd": []string{"sh"}, "env": []string{}},
		{"type": auth.GreetdPostResponse, "response": "pw"},
	} {
		if err := writeFrame(client, req); err != nil {
			t.Fatal(err)
		}
		if got := read(); got != "error:error" {
			t.Errorf("%v = %s, want an error", req["type"], got)
		}
	}
	if err := writeFrame(client, map[string]any{"type": auth.GreetdCancelSession}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "success:" {
		t.Errorf("cancel without a login = %s", got)
	}
	_ = client.Close()
	if err := <-errs; err != nil {
		t.Errorf("a greeter closing between requests = %v, want a clean end", err)
	}
}

// A greeter that hangs up inside a request is an error, unlike one that
// hangs up between requests.
func TestATruncatedRequestIsAnError(t *testing.T) {
	client, logins, errs := serve(t)
	if _, err := client.Write([]byte{0x10, 0}); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if l, err := <-logins, <-errs; l != nil || err == nil {
		t.Errorf("serveGreeter = %v, %v; want an error and no login", l, err)
	}
}

func TestKioskConfigQuotesTheGreeter(t *testing.T) {
	got := kioskConfig([]string{"/nix/store/x/bin/lys-login", "greet", "--sessions", "/run/my sessions", `--env`, `A="b"`})
	want := "binds: []\nautostart:\n  - \"/nix/store/x/bin/lys-login greet --sessions '/run/my sessions' --env 'A=\\\"b\\\"'\"\n"
	if got != want {
		t.Errorf("kiosk config =\n%s\nwant\n%s", got, want)
	}
}

func TestShellOf(t *testing.T) {
	passwd := "root:x:0:0::/root:/run/current-system/sw/bin/bash\nalice:x:1000:100::/home/alice:/bin/zsh\nnoshell:x:1001:100::/home/n:\n"
	for name, want := range map[string]string{"alice": "/bin/zsh", "noshell": "/bin/sh", "ghost": "/bin/sh"} {
		if got := shellOf(passwd, name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

func TestRefusalTextIsPAMsReason(t *testing.T) {
	refused := fmt.Errorf("login: %w", &auth.RefusedError{Step: "authentication", Reason: "Authentication failure"})
	if got := refusalText(refused); got != "Authentication failure" {
		t.Errorf("refused = %q, want PAM's reason alone", got)
	}
	other := errors.New("pam_start: no such service")
	if got := refusalText(other); got != other.Error() {
		t.Errorf("other = %q, want the whole error", got)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// writeFrame and readFrame speak greetd's framing as a raw greeter.
func writeFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(binary.NativeEndian.AppendUint32(nil, uint32(len(body))), body...))
	return err
}

func readFrame(r io.Reader, v any) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return err
	}
	body := make([]byte, binary.NativeEndian.Uint32(head[:]))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// A connection the greeter left open does not hold up the next: the
// daemon serves each on its own.
func TestAnIdleConnectionDoesNotBlockTheNext(t *testing.T) {
	conns := make(chan net.Conn)
	logins := make(chan *pendingLogin, 1)
	go func() {
		l, _ := testOptions().serveConns(context.Background(), conns, make(chan struct{}))
		logins <- l
	}()
	idleServer, idleClient := net.Pipe()
	t.Cleanup(func() { _ = idleClient.Close() })
	conns <- idleServer
	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	conns <- server
	if err := auth.NewGreetd(client, func() []string { return []string{"Hyprland"} }, nil).Run("alice", func(auth.Prompt) (string, bool) { return "pw", true }); err != nil {
		t.Fatalf("login beside an idle connection: %v", err)
	}
	login := <-logins
	if login == nil {
		t.Fatal("no session")
	}
	if err := login.run(); err != nil {
		t.Error(err)
	}
	if _, err := idleClient.Write([]byte{0}); err == nil {
		t.Error("the idle connection stayed open after the login")
	}
}
