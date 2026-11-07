// Package ipptest is a fake CUPS scheduler on loopback for tests: the
// queues a test sets answer CUPS-Get-Printers, Get-Printer-Attributes
// and CUPS-Get-Default, CUPS-Create-Local-Printer adds one, and
// Print-Job records what it is sent. It also stands in for a network
// printer answering its own attributes. Nothing reaches a real printer
// or the host's cupsd.
package ipptest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-shell/internal/ipp"
)

// Job is one Print-Job the scheduler took: the queue its URI named,
// its attribute groups and its document.
type Job struct {
	Printer  string
	Groups   []ipp.Group
	Document []byte
}

// Server is the fake scheduler.
type Server struct {
	// Addr is host:port, for ipp.NewClient.
	Addr string
	srv  *httptest.Server

	// device answers every Get-Printer-Attributes with its attributes,
	// as one network printer does at its own URI.
	device []ipp.Attr

	mu       sync.Mutex
	names    []string
	printers map[string][]ipp.Attr
	def      string
	gate     chan struct{}
	ops      []uint16
	replies  int
	jobs     []Job
}

// Start runs the scheduler on loopback until the test ends.
func Start(t *testing.T) *Server {
	t.Helper()
	s := &Server{printers: map[string][]ipp.Attr{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	s.cleanup(t)
	return s
}

// StartDevice runs a network printer on loopback, over TLS with a
// self-signed certificate (ipps) or plain HTTP (ipp), answering its
// attributes at any URI.
func StartDevice(t *testing.T, secure bool, attrs ...ipp.Attr) *Server {
	t.Helper()
	s := &Server{printers: map[string][]ipp.Attr{}, device: attrs}
	if secure {
		s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	} else {
		s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	}
	s.cleanup(t)
	return s
}

func (s *Server) cleanup(t *testing.T) {
	t.Cleanup(func() {
		s.Release()
		s.srv.Close()
	})
	u, err := url.Parse(s.srv.URL)
	if err != nil {
		t.Fatalf("ipptest: the server's URL %q: %v", s.srv.URL, err)
	}
	s.Addr = u.Host
}

// Client is a client of the scheduler.
func (s *Server) Client() *ipp.Client { return ipp.NewClient(s.Addr) }

// SetPrinter adds the queue name, or replaces its attributes, with
// printer-name set and the rest as given.
func (s *Server) SetPrinter(name string, attrs ...ipp.Attr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.names, name) {
		s.names = append(s.names, name)
	}
	s.printers[name] = append([]ipp.Attr{ipp.String(ipp.TagName, "printer-name", name)}, attrs...)
}

// SetDefault makes name the scheduler's default destination.
func (s *Server) SetDefault(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.def = name
}

// Hold makes every request wait until Release.
func (s *Server) Hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gate == nil {
		s.gate = make(chan struct{})
	}
}

// Release lets the held requests and every later one through.
func (s *Server) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gate != nil {
		close(s.gate)
		s.gate = nil
	}
}

// Ops are the operations asked for, in order, and Replies how many
// answers were sent.
func (s *Server) Ops() []uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ops)
}

// Replies is how many answers were sent.
func (s *Server) Replies() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replies
}

// Jobs are the Print-Jobs taken.
func (s *Server) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.jobs)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req, err := ipp.Decode(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.ops = append(s.ops, req.Code)
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
	}
	resp := s.answer(req, body[len(req.Encode()):])
	w.Header().Set("Content-Type", "application/ipp")
	if _, err := w.Write(resp.Encode()); err != nil {
		return
	}
	s.mu.Lock()
	s.replies++
	s.mu.Unlock()
}

// The status codes the fake answers with.
const (
	statusOK       uint16 = 0x0000
	statusNotFound uint16 = 0x0406
)

func (s *Server) answer(req ipp.Message, doc []byte) ipp.Message {
	resp := ipp.Message{Code: statusOK, RequestID: req.RequestID, Groups: []ipp.Group{{Tag: ipp.GroupOperation, Attrs: []ipp.Attr{
		ipp.String(ipp.TagCharset, "attributes-charset", "utf-8"),
		ipp.String(ipp.TagLanguage, "attributes-natural-language", "en"),
	}}}}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch req.Code {
	case ipp.OpCUPSGetPrinters:
		if len(s.names) == 0 {
			resp.Code = statusNotFound
		}
		for _, name := range s.names {
			resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.GroupPrinter, Attrs: s.printers[name]})
		}
	case ipp.OpCUPSGetDefault:
		if s.def == "" {
			resp.Code = statusNotFound
			break
		}
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.GroupPrinter, Attrs: []ipp.Attr{ipp.String(ipp.TagName, "printer-name", s.def)}})
	case ipp.OpGetPrinterAttributes:
		if s.device != nil {
			resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.GroupPrinter, Attrs: s.device})
			break
		}
		attrs, ok := s.printers[queueOf(req)]
		if !ok {
			resp.Code = statusNotFound
			break
		}
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.GroupPrinter, Attrs: attrs})
	case ipp.OpPrintJob:
		name := queueOf(req)
		if _, ok := s.printers[name]; !ok {
			resp.Code = statusNotFound
			break
		}
		s.jobs = append(s.jobs, Job{Printer: name, Groups: req.Groups, Document: doc})
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.GroupJob, Attrs: []ipp.Attr{ipp.Int("job-id", int32(len(s.jobs)))}}) //nolint:gosec // a test's handful of jobs
	case ipp.OpCUPSCreateLocalPrinter:
		var name, device string
		for _, g := range req.Groups {
			if g.Tag == ipp.GroupPrinter {
				name, device = g.Text("printer-name"), g.Text("device-uri")
			}
		}
		if name == "" || device == "" {
			resp.Code = 0x0400
			break
		}
		if _, ok := s.printers[name]; !ok {
			s.names = append(s.names, name)
		}
		s.printers[name] = []ipp.Attr{
			ipp.String(ipp.TagName, "printer-name", name), ipp.String(ipp.TagURI, "device-uri", device),
			ipp.Enum("printer-state", ipp.StateIdle),
		}
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.GroupPrinter, Attrs: []ipp.Attr{
			ipp.String(ipp.TagURI, "printer-uri-supported", ipp.PrinterURI(name)), ipp.Int("printer-id", int32(len(s.names))), //nolint:gosec // a test's handful of queues
		}})
	default:
		resp.Code = 0x0501
	}
	return resp
}

// Created is the device URI of each queue CUPS-Create-Local-Printer
// made, by name.
func (s *Server) Created(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.printers[name] {
		if a.Name == "device-uri" && len(a.Values) > 0 {
			return a.Values[0].Text
		}
	}
	return ""
}

// queueOf is the queue a request's printer-uri names.
func queueOf(req ipp.Message) string {
	for _, g := range req.Groups {
		if g.Tag != ipp.GroupOperation {
			continue
		}
		u, err := url.Parse(g.Text("printer-uri"))
		if err != nil {
			return ""
		}
		name, _ := url.PathUnescape(strings.TrimPrefix(u.Path, "/printers/"))
		return name
	}
	return ""
}
