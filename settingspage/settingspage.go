// Package settingspage is the contract between lys-settings and the
// programs that open it: its command line, and the page list it prints
// so a launcher can offer its pages without linking the app.
package settingspage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
