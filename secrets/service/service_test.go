package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/secrets/keyring"
)

var testKDF = keyring.KDF{Time: 1, MemKiB: 64, Threads: 1}

// fakePrompter answers prompts from a script: each answer is a password,
// or "" to dismiss.
type fakePrompter struct {
	mu      sync.Mutex
	answers []string
	asked   []string
}

func (f *fakePrompter) next(kind string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, kind)
	if len(f.answers) == 0 {
		return nil, false, nil
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return []byte(a), a != "", nil
}

func (f *fakePrompter) Password(_ context.Context, collection string, retry bool) ([]byte, bool, error) {
	kind := "password " + collection
	if retry {
		kind += " retry"
	}
	return f.next(kind)
}

func (f *fakePrompter) NewPassword(_ context.Context, collection string) ([]byte, bool, error) {
	return f.next("new " + collection)
}

func (f *fakePrompter) script(answers ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers, f.asked = answers, nil
}

func (f *fakePrompter) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

type fixture struct {
	bus      *dbustest.Bus
	dir      string
	prompter *fakePrompter
	svc      *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{bus: dbustest.Session(t), dir: t.TempDir(), prompter: &fakePrompter{}}
	f.start(t)
	return f
}

func (f *fixture) start(t *testing.T) {
	t.Helper()
	svc, err := Start(f.bus.Conn(t), Config{Dir: f.dir, KDF: testKDF, Prompter: f.prompter})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	f.svc = svc
}

func requireTool(t *testing.T, bin string) string {
	t.Helper()
	path, err := exec.LookPath(bin)
	if err != nil {
		t.Skipf("%s is not installed (the nix dev shell carries it)", bin)
	}
	return path
}

func run(t *testing.T, stdin string, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if err != nil {
		return out.String(), errors.Join(err, errors.New(errOut.String()))
	}
	return out.String(), nil
}

func TestSecretToolStoresLooksUpAndClears(t *testing.T) {
	tool := requireTool(t, "secret-tool")
	f := newFixture(t)
	if err := f.svc.UnlockLogin([]byte("login-pw")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "s3cret", tool, "store", "--label=Git token", "service", "git", "user", "me"); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := run(t, "", tool, "lookup", "service", "git", "user", "me")
	if err != nil || got != "s3cret" {
		t.Fatalf("lookup = %q, %v", got, err)
	}
	if got, err := run(t, "", tool, "lookup", "service", "mail"); err == nil {
		t.Errorf("lookup of a missing item = %q, want a failure", got)
	}
	if _, err := run(t, "", tool, "clear", "service", "git"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, err := run(t, "", tool, "lookup", "service", "git", "user", "me"); err == nil {
		t.Errorf("lookup after clear = %q, want a failure", got)
	}
	if prompts := f.prompter.log(); len(prompts) != 0 {
		t.Errorf("an unlocked login collection prompted: %v", prompts)
	}
	// Nothing secret reaches the disk in the clear.
	_, _ = run(t, "persisted-value", tool, "store", "--label=Kept", "service", "kept")
	data, err := os.ReadFile(filepath.Join(f.dir, LoginID+collectionExt))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"persisted-value", "kept", "Kept", "login-pw"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("the collection file holds %q in the clear", leak)
		}
	}
}

// With no collection at all, libsecret creates the default one, which
// asks for its new password.
func TestSecretToolCreatesTheDefaultCollection(t *testing.T) {
	tool := requireTool(t, "secret-tool")
	f := newFixture(t)
	f.prompter.script("new-pw")
	if _, err := run(t, "v", tool, "store", "--label=x", "k", "v"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if got := f.prompter.log(); len(got) != 1 || !strings.HasPrefix(got[0], "new ") {
		t.Errorf("prompts = %v, want one new-password prompt", got)
	}
	if got, err := run(t, "", tool, "lookup", "k", "v"); err != nil || got != "v" {
		t.Errorf("lookup = %q, %v", got, err)
	}
	// Dismissing the creation stores nothing.
	f2 := &fixture{bus: dbustest.Session(t), dir: t.TempDir(), prompter: &fakePrompter{}}
	f2.start(t)
	f2.prompter.script("")
	if _, err := run(t, "v", tool, "store", "--label=x", "k", "v"); err == nil {
		t.Error("store with a dismissed creation prompt succeeded")
	}
	if ids, _ := (disk{f2.dir}).ids(); len(ids) != 0 {
		t.Errorf("a dismissed creation left collections %v", ids)
	}
}

// A locked collection is unlocked through a prompt that repeats on a
// wrong password; dismissing it leaves the collection locked.
func TestLockedCollectionPromptsForItsPassword(t *testing.T) {
	tool := requireTool(t, "secret-tool")
	f := newFixture(t)
	if err := f.svc.UnlockLogin([]byte("right")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "v", tool, "store", "--label=x", "k", "v"); err != nil {
		t.Fatal(err)
	}
	f.svc.Lock()

	f.prompter.script("wrong", "right")
	if got, err := run(t, "", tool, "lookup", "k", "v"); err != nil || got != "v" {
		t.Fatalf("lookup through the prompt = %q, %v", got, err)
	}
	if got := f.prompter.log(); strings.Join(got, ",") != "password login,password login retry" {
		t.Errorf("prompts = %v", got)
	}

	f.svc.Lock()
	f.prompter.script("")
	if got, _ := run(t, "", tool, "lookup", "k", "v"); got != "" {
		t.Errorf("lookup with a dismissed prompt = %q, want nothing", got)
	}
	if !f.svc.colls[LoginID].kr.Locked() {
		t.Error("a dismissed prompt unlocked the collection")
	}
}

func TestPythonSecretStorageOverDH(t *testing.T) {
	python := requireTool(t, "python3")
	if _, err := run(t, "", python, "-c", "import secretstorage"); err != nil {
		t.Skip("python secretstorage is not installed (the nix dev shell carries it)")
	}
	f := newFixture(t)
	if err := f.svc.UnlockLogin([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	script := `
import secretstorage
conn = secretstorage.dbus_init()
coll = secretstorage.get_default_collection(conn)
assert not coll.is_locked()
item = coll.create_item("py", {"app": "py", "n": "1"}, b"\x00binary\xffsecret", replace=True)
assert coll.session.encrypted and coll.session.aes_key is not None
found = list(coll.search_items({"app": "py"}))
assert len(found) == 1, found
assert found[0].get_secret() == b"\x00binary\xffsecret"
assert found[0].get_label() == "py"
found[0].set_label("renamed")
found[0].set_attributes({"app": "py", "n": "2"})
assert list(coll.search_items({"n": "2"}))[0].get_label() == "renamed"
found[0].delete()
assert not list(coll.search_items({"app": "py"}))
print("ok")
`
	out, err := run(t, "", python, "-c", script)
	if err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("secretstorage: %q, %v", out, err)
	}
}

func TestGitCredentialLibsecret(t *testing.T) {
	helper := os.Getenv("GIT_CREDENTIAL_LIBSECRET")
	if helper == "" {
		t.Skip("GIT_CREDENTIAL_LIBSECRET is not set (the nix dev shell sets it)")
	}
	f := newFixture(t)
	if err := f.svc.UnlockLogin([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	cred := "protocol=https\nhost=example.com\nusername=me\npassword=git-pw\n\n"
	if _, err := run(t, cred, helper, "store"); err != nil {
		t.Fatalf("store: %v", err)
	}
	out, err := run(t, "protocol=https\nhost=example.com\n\n", helper, "get")
	if err != nil || !strings.Contains(out, "password=git-pw") || !strings.Contains(out, "username=me") {
		t.Fatalf("get = %q, %v", out, err)
	}
	if _, err := run(t, cred, helper, "erase"); err != nil {
		t.Fatalf("erase: %v", err)
	}
	if out, _ := run(t, "protocol=https\nhost=example.com\n\n", helper, "get"); strings.Contains(out, "password=") {
		t.Errorf("get after erase = %q", out)
	}
}

// A restart finds the collection locked; only the right password opens
// it, and the items survive.
func TestRestartKeepsItemsBehindThePassword(t *testing.T) {
	tool := requireTool(t, "secret-tool")
	f := newFixture(t)
	if err := f.svc.UnlockLogin([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "kept", tool, "store", "--label=x", "k", "v"); err != nil {
		t.Fatal(err)
	}
	f.svc.Close()
	f.start(t)
	if !f.svc.colls[LoginID].kr.Locked() {
		t.Fatal("a restarted service is unlocked")
	}
	if err := f.svc.UnlockLogin([]byte("nope")); !errors.Is(err, keyring.ErrWrongPassword) {
		t.Fatalf("UnlockLogin(wrong) = %v", err)
	}
	if !f.svc.colls[LoginID].kr.Locked() {
		t.Fatal("a wrong password unlocked it")
	}
	if err := f.svc.UnlockLogin([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if got, err := run(t, "", tool, "lookup", "k", "v"); err != nil || got != "kept" {
		t.Errorf("lookup after restart = %q, %v", got, err)
	}
}

func TestStartLeavesAnotherProviderAlone(t *testing.T) {
	bus := dbustest.Session(t)
	other := bus.Conn(t)
	if _, err := other.RequestName(BusName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	_, err := Start(bus.Conn(t), Config{Dir: t.TempDir(), KDF: testKDF, Prompter: &fakePrompter{}})
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("Start = %v, want ErrNameTaken", err)
	}
	var owner string
	if err := other.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, BusName).Store(&owner); err != nil || owner != other.Names()[0] {
		t.Errorf("owner = %q, %v; want the first provider kept", owner, err)
	}
}

// A session belongs to the client that opened it, and ends when that
// client leaves the bus.
func TestSessionsBelongToTheirClient(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.UnlockLogin([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	alice, mallory := f.bus.Conn(t), f.bus.Conn(t)
	svc := alice.Object(BusName, rootPath)
	var out dbus.Variant
	var sess dbus.ObjectPath
	if err := svc.Call(ifaceService+".OpenSession", 0, algPlain, dbus.MakeVariant("")).Store(&out, &sess); err != nil {
		t.Fatal(err)
	}
	var item, prompt dbus.ObjectPath
	props := map[string]dbus.Variant{propItemLabel: dbus.MakeVariant("a"), propItemAttrs: dbus.MakeVariant(map[string]string{"k": "v"})}
	coll := alice.Object(BusName, dbus.ObjectPath(aliasBase+"default"))
	if err := coll.Call(ifaceCollection+".CreateItem", 0, props, Secret{Session: sess, Parameters: []byte{}, Value: []byte("x"), ContentType: "text/plain"}, false).Store(&item, &prompt); err != nil {
		t.Fatal(err)
	}
	var secret Secret
	err := mallory.Object(BusName, item).Call(ifaceItem+".GetSecret", 0, sess).Store(&secret)
	var de dbus.Error
	if !errors.As(err, &de) || de.Name != errNoSession {
		t.Errorf("another client's session = %v, want NoSession", err)
	}
	if err := alice.Object(BusName, item).Call(ifaceItem+".GetSecret", 0, sess).Store(&secret); err != nil || string(secret.Value) != "x" {
		t.Fatalf("owner's GetSecret = %q, %v", secret.Value, err)
	}
	_ = alice.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.svc.mu.Lock()
		_, open := f.svc.sessions[sess]
		f.svc.mu.Unlock()
		if !open {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session outlived its client")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestUnknownAlgorithmIsNotSupported(t *testing.T) {
	f := newFixture(t)
	err := f.bus.Conn(t).Object(BusName, rootPath).Call(ifaceService+".OpenSession", 0, "rot13", dbus.MakeVariant("")).Err
	var de dbus.Error
	if !errors.As(err, &de) || de.Name != errNotSupported {
		t.Errorf("OpenSession(rot13) = %v, want NotSupported", err)
	}
}
