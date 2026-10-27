// Package control is lys-secrets' unlock channel: a unix socket in the
// user's runtime directory through which the lock screen (or the login
// screen) hands over the password it just verified, so the login
// collection unlocks with it. The password never crosses D-Bus. Only
// processes of the same user may connect, checked with SO_PEERCRED.
//
// Wire format: the client sends a big-endian u32 length and the
// password; the server answers one status byte.
package control

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/secrets/keyring"
)

const (
	statusOK byte = iota
	statusWrongPassword
	statusFailed
)

// maxPassword bounds what a client may send.
const maxPassword = 4096

const timeout = 10 * time.Second

// Errors a client tells apart.
var (
	ErrNotRunning    = errors.New("lys-secrets is not running")
	ErrWrongPassword = errors.New("lys-secrets: the password does not unlock the login collection")
)

// SocketPath is $XDG_RUNTIME_DIR/lys-secrets/control.
func SocketPath() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(dir) {
		return "", errors.New("XDG_RUNTIME_DIR is not set to an absolute path")
	}
	return filepath.Join(dir, "lys-secrets", "control"), nil
}

// Unlocker unlocks the login collection.
type Unlocker interface {
	UnlockLogin(password []byte) error
}

// Listen binds the socket at path in a 0700 directory, replacing a stale
// socket a dead server left.
func Listen(path string) (*net.UnixListener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("control: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // a directory needs its search bit; 0700 keeps it the user's alone
		return nil, fmt.Errorf("control: restrict %s: %w", filepath.Dir(path), err)
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("control: another lys-secrets serves %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("control: remove the stale %s: %w", path, err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("control: listen on %s: %w", path, err)
	}
	return ln, nil
}

// Serve answers unlock requests on ln until ctx ends.
func Serve(ctx context.Context, ln *net.UnixListener, u Unlocker) {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("control: accept: %v", err)
			}
			return
		}
		go handle(conn, u)
	}
}

func handle(conn *net.UnixConn, u Unlocker) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := samePeerUser(conn); err != nil {
		log.Printf("control: refused a connection: %v", err)
		return
	}
	password, err := readPassword(conn)
	if err != nil {
		log.Printf("control: %v", err)
		_, _ = conn.Write([]byte{statusFailed})
		return
	}
	defer clear(password)
	status := statusOK
	switch err := u.UnlockLogin(password); {
	case errors.Is(err, keyring.ErrWrongPassword):
		status = statusWrongPassword
		log.Printf("control: the password does not unlock the login collection")
	case err != nil:
		status = statusFailed
		log.Printf("control: unlock the login collection: %v", err)
	}
	_, _ = conn.Write([]byte{status})
}

func samePeerUser(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credErr != nil {
		return fmt.Errorf("peer credentials: %w", credErr)
	}
	if int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("peer uid %d is not this user's %d", cred.Uid, os.Getuid())
	}
	return nil
}

func readPassword(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, fmt.Errorf("read the password length: %w", err)
	}
	if n == 0 || n > maxPassword {
		return nil, fmt.Errorf("a password of %d bytes is outside 1-%d", n, maxPassword)
	}
	password := make([]byte, n)
	if _, err := io.ReadFull(r, password); err != nil {
		clear(password)
		return nil, fmt.Errorf("read the password: %w", err)
	}
	return password, nil
}

// ReadPassword reads a password a login screen wrote to an inherited
// descriptor: the whole stream, without a trailing newline.
func ReadPassword(r io.Reader) ([]byte, error) {
	password, err := io.ReadAll(io.LimitReader(r, maxPassword+1))
	if err != nil {
		clear(password)
		return nil, fmt.Errorf("read the login password: %w", err)
	}
	if len(password) > maxPassword {
		clear(password)
		return nil, fmt.Errorf("the login password is longer than %d bytes", maxPassword)
	}
	if len(password) > 0 && password[len(password)-1] == '\n' {
		password = password[:len(password)-1]
	}
	if len(password) == 0 {
		return nil, errors.New("the login password is empty")
	}
	return password, nil
}

// Unlock hands password to the lys-secrets serving path. No server is
// ErrNotRunning; a password that does not open the login collection is
// ErrWrongPassword.
func Unlock(path string, password []byte) error {
	if len(password) == 0 || len(password) > maxPassword {
		return fmt.Errorf("control: a password of %d bytes is outside 1-%d", len(password), maxPassword)
	}
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return ErrNotRunning
		}
		return fmt.Errorf("control: connect to %s: %w", path, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	msg := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(password)), uint32(len(password))) //nolint:gosec // bounded by maxPassword above
	msg = append(msg, password...)
	defer clear(msg)
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("control: send the password: %w", err)
	}
	status := make([]byte, 1)
	if _, err := io.ReadFull(conn, status); err != nil {
		return fmt.Errorf("control: read the answer: %w", err)
	}
	switch status[0] {
	case statusOK:
		return nil
	case statusWrongPassword:
		return ErrWrongPassword
	default:
		return errors.New("control: lys-secrets could not unlock the login collection (see its log)")
	}
}
