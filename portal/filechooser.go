package portal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/filechooser"
)

// fileChooser is org.freedesktop.impl.portal.FileChooser over the
// shell's dev.stubbe.lys.FileChooser1: OpenFile,
// SaveFile, and SaveFiles as a folder pick.
type fileChooser struct{ conn *dbus.Conn }

func fileChooserIface(conn *dbus.Conn) dbusx.Interface {
	return dbusx.Interface{
		Name:    "org.freedesktop.impl.portal.FileChooser",
		Methods: fileChooser{conn},
	}
}

func (f fileChooser) client() *filechooser.Client { return filechooser.NewClient(f.conn) }

// chooserOptions are the options OpenFile, SaveFile and SaveFiles
// share: the filters and the one to start on, the choices, and the
// confirm button's label. A current_filter that is not one of the
// filters starts on the first; with no filters it is the one filter.
func chooserOptions(options Vardict) (filters []filechooser.Filter, current int32, choices []portalreq.Choice, accept string, err error) {
	filters = fileFilters(options)
	if v, ok := options["current_filter"]; ok {
		var cf filechooser.Filter
		if err := dbus.Store([]any{v.Value()}, &cf); err != nil {
			return nil, 0, nil, "", fmt.Errorf("malformed current_filter: %w", err)
		}
		switch i := slices.IndexFunc(filters, func(f filechooser.Filter) bool { return reflect.DeepEqual(f, cf) }); {
		case len(filters) == 0:
			filters = []filechooser.Filter{cf}
		case i >= 0:
			current = int32(i)
		default:
			warnf("filechooser: current_filter %q is not one of the filters", cf.Name)
		}
	}
	if choices, err = optChoices(options); err != nil {
		return nil, 0, nil, "", err
	}
	return filters, current, choices, stringOr(options, "accept_label", ""), nil
}

// OpenFile opens existing files or directories.
func (f fileChooser) OpenFile(handle dbus.ObjectPath, appID, parent, title string, options Vardict) (uint32, Vardict, *dbus.Error) {
	filters, current, choices, accept, err := chooserOptions(options)
	if err != nil {
		warnf("filechooser: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	client := f.client()
	c, ok := beginOr(f.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	multiple, _ := optBool(options, "multiple")
	directory, _ := optBool(options, "directory")
	res, err := client.Open(c.ctx, c.caller, filechooser.OpenRequest{
		Title: title, Multiple: multiple, Directory: directory,
		Filters: filters, CurrentFolder: currentFolder(options),
		AcceptLabel: accept, CurrentFilter: current, Choices: choices,
	})
	return chooserResponse(c, "open", res, len(filters) > 0, len(choices) > 0, err)
}

// SaveFile chooses a save destination; current_file, an existing file
// saved again, starts in its folder with its name.
func (f fileChooser) SaveFile(handle dbus.ObjectPath, appID, parent, title string, options Vardict) (uint32, Vardict, *dbus.Error) {
	filters, current, choices, accept, err := chooserOptions(options)
	if err != nil {
		warnf("filechooser: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	client := f.client()
	c, ok := beginOr(f.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	folder, name := currentFolder(options), stringOr(options, "current_name", "")
	if b, _ := options["current_file"].Value().([]byte); len(b) > 0 {
		file := cString(b)
		folder, name = filepath.Dir(file), filepath.Base(file)
	}
	res, err := client.Save(c.ctx, c.caller, filechooser.SaveRequest{
		Title: title, CurrentName: name, Filters: filters, CurrentFolder: folder,
		AcceptLabel: accept, CurrentFilter: current, Choices: choices,
	})
	return chooserResponse(c, "save", res, len(filters) > 0, len(choices) > 0, err)
}

// SaveFiles picks a folder and replies one URI per requested file name
// inside it: a name already taken there, or earlier in the list, is
// numbered ("report (2).pdf") rather than overwritten, as the spec
// allows. A name that is not a plain file name (empty, ".", "..", or
// one with a slash) fails the call.
func (f fileChooser) SaveFiles(handle dbus.ObjectPath, appID, parent, title string, options Vardict) (uint32, Vardict, *dbus.Error) {
	raw, _ := options["files"].Value().([][]byte)
	names := make([]string, len(raw))
	for i, b := range raw {
		names[i] = cString(b)
		if names[i] == "" || names[i] == "." || names[i] == ".." || strings.Contains(names[i], "/") {
			warnf("filechooser: SaveFiles: %q is not a file name", names[i])
			return ResponseOther, Vardict{}, nil
		}
	}
	_, _, choices, accept, err := chooserOptions(options)
	if err != nil {
		warnf("filechooser: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	client := f.client()
	c, ok := beginOr(f.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	res, err := client.Open(c.ctx, c.caller, filechooser.OpenRequest{
		Title: title, Directory: true, CurrentFolder: currentFolder(options), AcceptLabel: accept, Choices: choices,
	})
	if err != nil || len(res.URIs) == 0 {
		return chooserResponse(c, "folder pick", filechooser.Result{}, false, false, err)
	}
	folder, ok := fileuri.Path(res.URIs[0])
	if !ok {
		warnf("filechooser: SaveFiles: the shell picked %q", res.URIs[0])
		return ResponseOther, Vardict{}, nil
	}
	taken := map[string]bool{}
	uris := make([]string, 0, len(names))
	for _, name := range names {
		free := freeName(folder, name, taken)
		taken[free] = true
		uris = append(uris, fileuri.FromPath(filepath.Join(folder, free)))
	}
	res.URIs = uris
	return chooserResponse(c, "save files", res, false, len(choices) > 0, nil)
}

// freeName is name, or its first numbered form ("name (2).ext") not in
// folder and not taken by an earlier name of the same request.
func freeName(folder, name string, taken map[string]bool) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	candidate := name
	for n := 2; ; n++ {
		if _, err := os.Lstat(filepath.Join(folder, candidate)); errors.Is(err, fs.ErrNotExist) && !taken[candidate] {
			return candidate
		}
		candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
	}
}

// chooserResponse is the dialog's answer as a reply: no URIs (or a
// closed request) is a cancel, a failed dialog (no shell) other; the
// filter chosen and the choices made ride along when the app offered
// them.
func chooserResponse(c *call, what string, res filechooser.Result, filters, choices bool, err error) (uint32, Vardict, *dbus.Error) {
	if code := c.response("filechooser "+what, len(res.URIs) > 0, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	results := Vardict{"uris": dbus.MakeVariant(res.URIs)}
	if filters && len(res.Filter.Rules) > 0 {
		results["current_filter"] = dbus.MakeVariant(res.Filter)
	}
	if choices {
		results["choices"] = dbus.MakeVariant(res.Choices)
	}
	return ResponseSuccess, results, nil
}

// fileFilters reads the filters option, a(sa(us)).
func fileFilters(options Vardict) []filechooser.Filter {
	var filters []filechooser.Filter
	if v, ok := options["filters"]; ok {
		if err := dbus.Store([]any{v.Value()}, &filters); err != nil {
			warnf("filechooser: malformed filters: %v", err)
			return nil
		}
	}
	return filters
}

// currentFolder reads the current_folder option, a NUL-terminated ay.
func currentFolder(options Vardict) string {
	b, _ := options["current_folder"].Value().([]byte)
	return cString(b)
}

// cString is a portal byte string: trailing NULs dropped, invalid
// UTF-8 replaced as from_utf8_lossy does.
func cString(b []byte) string {
	return strings.ToValidUTF8(strings.TrimRight(string(b), "\x00"), "�")
}
