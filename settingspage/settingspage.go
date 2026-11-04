// Package settingspage is the contract between lys-settings and the
// programs that open it: its command line, and the page list it prints
// so a launcher can offer its pages without linking the app.
package settingspage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sync"
	"time"
)

const (
	// Program is the settings app's executable.
	Program = "lys-settings"
	// PageFlag opens the app on a page, by ID.
	PageFlag = "--page"
	// ListFlag prints the pages with Encode and exits.
	ListFlag = "--list-pages"
)

// Page is one page of the app, its title in the desktop's language.
type Page struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon"`
}

// Argv opens the app on a page.
func (p Page) Argv() []string { return []string{Program, PageFlag, p.ID} }

// ErrNotInstalled is the app missing from the path, which is a choice and not a
// failure: lys-settings is a package of its own.
var ErrNotInstalled = errors.New("settingspage: lys-settings is not installed")

const listLimit = 5 * time.Second

// List asks the installed app for its pages.
func List(ctx context.Context) ([]Page, error) {
	ctx, cancel := context.WithTimeout(ctx, listLimit)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, Program, ListFlag)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return nil, ErrNotInstalled
	case err != nil:
		return nil, fmt.Errorf("settingspage: %s %s: %w: %s", Program, ListFlag, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return Decode(out)
}

// NewLister is List for a long-lived process that asks every time its launcher
// opens: the pages of an installed app do not change, so the first list is kept.
// An app that is missing or failing is asked again.
func NewLister() func(context.Context) ([]Page, error) {
	var mu sync.Mutex
	var kept []Page
	return func(ctx context.Context) ([]Page, error) {
		mu.Lock()
		defer mu.Unlock()
		if kept != nil {
			return kept, nil
		}
		pages, err := List(ctx)
		if err != nil {
			return nil, err
		}
		kept = pages
		return pages, nil
	}
}

// Encode is the list as ListFlag prints it.
func Encode(pages []Page) ([]byte, error) {
	if err := validate(pages); err != nil {
		return nil, err
	}
	out, err := json.Marshal(pages)
	if err != nil {
		return nil, fmt.Errorf("settingspage: encode %d pages: %w", len(pages), err)
	}
	return append(out, '\n'), nil
}

// Decode reads what ListFlag printed; a page without an ID, a title or
// a unique ID is an error, so no launcher offers a row that cannot open.
func Decode(data []byte) ([]Page, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var pages []Page
	if err := dec.Decode(&pages); err != nil {
		return nil, fmt.Errorf("settingspage: decode the page list: %w", err)
	}
	if err := validate(pages); err != nil {
		return nil, err
	}
	return pages, nil
}

func validate(pages []Page) error {
	if pages == nil {
		return errors.New("settingspage: no page list")
	}
	seen := make([]string, 0, len(pages))
	for i, p := range pages {
		if p.ID == "" || p.Title == "" {
			return fmt.Errorf("settingspage: page %d has no ID or title: %+v", i, p)
		}
		if slices.Contains(seen, p.ID) {
			return fmt.Errorf("settingspage: page %q is listed twice", p.ID)
		}
		seen = append(seen, p.ID)
	}
	return nil
}
