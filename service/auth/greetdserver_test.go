package auth

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
)

// The server half speaks to the client half: a login through a prompt,
// a failed one, and a start_session carrying the command.
func TestGreetdServerAndClientAgree(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	done := make(chan error, 1)
	var started GreetdRequest
	go func() {
		done <- func() error {
			req, err := ReadGreetdRequest(server)
			if err != nil || req.Type != GreetdCreateSession || req.Username != "alice" {
				return errors.Join(err, errors.New("want create_session for alice"))
			}
			if err := WriteGreetdPrompt(server, Prompt{Kind: PromptSecret, Text: "Password:"}); err != nil {
				return err
			}
			req, err = ReadGreetdRequest(server)
			if err != nil || req.Type != GreetdPostResponse || req.Response == nil || *req.Response != "pw" {
				return errors.Join(err, errors.New("want the password"))
			}
			if err := WriteGreetdSuccess(server); err != nil {
				return err
			}
			if started, err = ReadGreetdRequest(server); err != nil {
				return err
			}
			return WriteGreetdSuccess(server)
		}()
	}()
	g := NewGreetd(client, func() []string { return []string{"lys-wm", "--systemd"} }, []string{"A=1"})
	var seen []Prompt
	if err := g.Run("alice", answering("pw", &seen)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if started.Type != GreetdStartSession || !slices.Equal(started.Cmd, []string{"lys-wm", "--systemd"}) || !slices.Equal(started.Env, []string{"A=1"}) {
		t.Errorf("start_session = %+v", started)
	}
	if len(seen) != 1 || seen[0].Kind != PromptSecret {
		t.Errorf("prompts = %+v", seen)
	}
}

func TestGreetdServerErrorsReachTheClient(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	cancelled := make(chan GreetdRequest, 1)
	go func() {
		_, _ = ReadGreetdRequest(server)
		_ = WriteGreetdError(server, true, "Authentication failure")
		req, _ := ReadGreetdRequest(server)
		cancelled <- req
		_ = WriteGreetdSuccess(server)
	}()
	err := NewGreetd(client, func() []string { return nil }, nil).Run("alice", answering("pw", &[]Prompt{}))
	if err == nil || err.Error() != "Authentication failure" {
		t.Errorf("Run = %v, want the server's description alone", err)
	}
	if req := <-cancelled; req.Type != GreetdCancelSession {
		t.Errorf("after the error the client sent %+v, want cancel_session", req)
	}
}

func TestReadGreetdRequestRefusesBadFrames(t *testing.T) {
	frame := func(body string) *bytes.Buffer {
		b := binary.NativeEndian.AppendUint32(nil, uint32(len(body)))
		return bytes.NewBuffer(append(b, body...))
	}
	if _, err := ReadGreetdRequest(frame(`{"type":"reboot"}`)); err == nil {
		t.Error("an unknown request type was accepted")
	}
	if _, err := ReadGreetdRequest(bytes.NewBuffer(binary.NativeEndian.AppendUint32(nil, maxGreetdFrame+1))); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("an oversized frame = %v", err)
	}
	if err := WriteGreetdPrompt(&bytes.Buffer{}, Prompt{Kind: 99}); err == nil {
		t.Error("a prompt of no known kind was sent")
	}
}
