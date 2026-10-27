package auth

import (
	"fmt"
	"io"
)

// GreetdRequest is one request a greeter sends over the greetd
// protocol, as the server side reads it.
type GreetdRequest struct {
	Type     string   `json:"type"`
	Username string   `json:"username"`
	Response *string  `json:"response"`
	Cmd      []string `json:"cmd"`
	Env      []string `json:"env"`
}

// The request types.
const (
	GreetdCreateSession = "create_session"
	GreetdPostResponse  = "post_auth_message_response"
	GreetdStartSession  = "start_session"
	GreetdCancelSession = "cancel_session"
)

// ReadGreetdRequest reads one request from a greeter; an unknown type
// is an error.
func ReadGreetdRequest(r io.Reader) (GreetdRequest, error) {
	var req GreetdRequest
	if err := readGreetdFrame(r, &req); err != nil {
		return GreetdRequest{}, err
	}
	switch req.Type {
	case GreetdCreateSession, GreetdPostResponse, GreetdStartSession, GreetdCancelSession:
		return req, nil
	}
	return GreetdRequest{}, fmt.Errorf("greetd: unknown request type %q", req.Type)
}

// WriteGreetdSuccess answers a request with success.
func WriteGreetdSuccess(w io.Writer) error {
	return writeGreetdFrame(w, greetdSuccess{Type: "success"})
}

// WriteGreetdError answers with an error: an authentication failure, or
// any other.
func WriteGreetdError(w io.Writer, authFailure bool, description string) error {
	kind := "error"
	if authFailure {
		kind = "auth_error"
	}
	return writeGreetdFrame(w, greetdError{Type: "error", ErrorType: kind, Description: description})
}

// WriteGreetdPrompt sends a PAM prompt for the greeter to show and
// answer.
func WriteGreetdPrompt(w io.Writer, p Prompt) error {
	kind := map[PromptKind]string{PromptVisible: "visible", PromptSecret: "secret", PromptInfo: "info", PromptError: "error"}[p.Kind]
	if kind == "" {
		return fmt.Errorf("greetd: no auth_message_type for prompt kind %d", p.Kind)
	}
	return writeGreetdFrame(w, greetdAuthMessage{Type: "auth_message", AuthMessageType: kind, AuthMessage: p.Text})
}

// The responses, each with exactly its variant's fields.
type (
	greetdSuccess struct {
		Type string `json:"type"`
	}
	greetdError struct {
		Type        string `json:"type"`
		ErrorType   string `json:"error_type"`
		Description string `json:"description"`
	}
	greetdAuthMessage struct {
		Type            string `json:"type"`
		AuthMessageType string `json:"auth_message_type"`
		AuthMessage     string `json:"auth_message"`
	}
)
