package search

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/lys-desktop/i18n"
)

const (
	// scriptHeaderBytes is how far into a file its directives may sit.
	scriptHeaderBytes = 8 << 10
	// scriptCopyTimeout bounds a script whose output is the result.
	scriptCopyTimeout = 30 * time.Second
	// scriptDirective leads each header line.
	scriptDirective = "@lys."
)

// ScriptOutput says what happens to a script's run.
type ScriptOutput uint8

// The script output modes.
const (
	// ScriptSilent runs it detached; nobody reads its output.
	ScriptSilent ScriptOutput = iota
	// ScriptTerminal runs it in a terminal.
	ScriptTerminal
	// ScriptCopy waits for it and copies its stdout.
	ScriptCopy
)

// ScriptArg is one argument a script asks for.
type ScriptArg struct {
	Name     string
	Optional bool
}

// Script is one script command, as its header says.
type Script struct {
	Path, Title, Subtitle, Keywords, Icon string
	Output                                ScriptOutput
	Args                                  []ScriptArg
}

// ParseScript reads a script's directives from its leading comment
// lines. A file with no @lys.title is not a command; a directive it
// cannot read is an error naming the line, so a typo does not silently
// run the script differently.
func ParseScript(path string, head io.Reader) (Script, bool, error) {
	s := Script{Path: path}
	scan := bufio.NewScanner(head)
	for line := 1; scan.Scan(); line++ {
		text := strings.TrimSpace(scan.Text())
		if text == "" || (line == 1 && strings.HasPrefix(text, "#!")) {
			continue
		}
		comment := strings.TrimLeft(text, "#/-; ")
		if comment == text {
			break
		}
		rest, ok := strings.CutPrefix(comment, scriptDirective)
		if !ok {
			continue
		}
		key, value, _ := strings.Cut(rest, " ")
		value = strings.TrimSpace(value)
		switch key {
		case "title":
			s.Title = value
		case "subtitle":
			s.Subtitle = value
		case "keywords":
			s.Keywords = value
		case "icon":
			s.Icon = value
		case "output":
			switch value {
			case "silent":
				s.Output = ScriptSilent
			case "terminal":
				s.Output = ScriptTerminal
			case "copy":
				s.Output = ScriptCopy
			default:
				return s, false, fmt.Errorf("%s:%d: @lys.output must be silent, terminal or copy, not %q", path, line, value)
			}
		case "argument":
			name, flag, _ := strings.Cut(value, " ")
			if name == "" || (flag != "" && strings.TrimSpace(flag) != "optional") {
				return s, false, fmt.Errorf("%s:%d: @lys.argument takes a name and, optionally, the word optional: %q", path, line, value)
			}
			s.Args = append(s.Args, ScriptArg{Name: name, Optional: flag != ""})
		default:
			return s, false, fmt.Errorf("%s:%d: unknown directive @lys.%s", path, line, key)
		}
	}
	return s, s.Title != "", nil
}

// Scripts offers the executables of a directory as commands. The format
// is in docs/guide/launcher.md.
type Scripts struct {
	env Env
	dir string
}

// NewScripts reads dir.
func NewScripts(env Env, dir string) *Scripts { return &Scripts{env: env, dir: dir} }

// Spec implements Provider.
func (*Scripts) Spec() Spec { return Spec{Section: SectionCommands, Weight: 8} }

// Whole implements Whole.
func (*Scripts) Whole() {}

// Load implements Provider.
func (s *Scripts) Load(_ context.Context, publish func([]Entry)) {
	listing, err := os.ReadDir(s.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("launcher: script commands: %v", err)
		}
		return
	}
	var entries []Entry
	for _, d := range listing {
		if d.IsDir() {
			continue
		}
		path := filepath.Join(s.dir, d.Name())
		sc, ok, err := readScript(path)
		if err != nil {
			log.Printf("launcher: script command: %v", err)
			continue
		}
		if !ok {
			continue
		}
		id := "script:" + d.Name()
		icon := sc.Icon
		if icon == "" {
			icon = "ld-terminal-symbolic"
		}
		entries = append(entries, Entry{ID: id, Learn: id, Title: sc.Title, Subtitle: sc.Subtitle, Keywords: sc.Keywords, Icon: appIcon(icon, icon), Data: sc})
	}
	publish(entries)
}

func readScript(path string) (Script, bool, error) {
	file, err := os.Open(path) //nolint:gosec // the user's own commands directory
	if err != nil {
		return Script{}, false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Script{}, false, err
	}
	if info.Mode()&0o111 == 0 {
		return Script{}, false, nil
	}
	return ParseScript(path, io.LimitReader(file, scriptHeaderBytes))
}

// Actions implements Provider.
func (s *Scripts) Actions(e Entry) []Action {
	sc, ok := e.Data.(Script)
	if !ok {
		return nil
	}
	return []Action{
		{Title: i18n.T("launcher-action-run"), Icon: "ld-play-symbolic", Run: func(ctx context.Context) Outcome {
			return s.next(ctx, sc, nil)
		}},
		{Title: i18n.T("launcher-action-reveal"), Icon: "ld-folder-open-symbolic", Run: func(context.Context) Outcome {
			s.env.Open(filepath.Dir(sc.Path))
			return Close{}
		}},
		copyAction(i18n.T("launcher-action-copy-path"), sc.Path),
	}
}

// next asks for the next missing argument, or runs the script.
func (s *Scripts) next(ctx context.Context, sc Script, got []string) Outcome {
	if len(got) == len(sc.Args) {
		return s.run(ctx, sc, got)
	}
	arg := sc.Args[len(got)]
	return Push{Level: &Level{
		Title: sc.Title, Placeholder: arg.Name, SubmitTitle: i18n.T("launcher-action-run"),
		Submit: func(ctx context.Context, text string) Outcome {
			if text == "" && !arg.Optional {
				return Stay{Message: i18n.T("launcher-script-argument-required", i18n.Str("name", arg.Name))}
			}
			return s.next(ctx, sc, append(slices.Clip(got), text))
		},
	}}
}

// run runs the script as an argv: arguments stay data whatever they
// hold.
func (s *Scripts) run(ctx context.Context, sc Script, args []string) Outcome {
	argv := append([]string{sc.Path}, args...)
	switch sc.Output {
	case ScriptTerminal:
		s.env.inTerminal(argv...)
		return Close{}
	case ScriptCopy:
		ctx, cancel := context.WithTimeout(ctx, scriptCopyTimeout)
		defer cancel()
		var out, errOut bytes.Buffer
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the user's own script
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			detail := strings.TrimSpace(errOut.String())
			if detail == "" {
				detail = err.Error()
			}
			return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", sc.Title), i18n.Str("error", detail))}
		}
		return Copy{Text: strings.TrimRight(out.String(), "\n")}
	}
	s.env.Spawn(argv)
	return Close{}
}
