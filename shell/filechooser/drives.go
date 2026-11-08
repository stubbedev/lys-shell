package filechooser

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-desktop/styling"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/service/udisks"
)

// How long the drives may take: a listing, and a mount, unlock or
// eject, which may wait on a polkit prompt or a filesystem check.
const (
	drivesListTimeout   = 5 * time.Second
	drivesActionTimeout = 2 * time.Minute
)

// locations is what the Locations section lists: the places (/, the
// mounts, the network shares) and the drives' volumes.
type locations struct {
	places  []Place
	volumes []udisks.Volume
}

// drives is the drives' client, nil without one (no system bus yet).
func (c *Chooser) drives() *udisks.Client {
	if c.d.Drives == nil {
		return nil
	}
	return c.d.Drives()
}

// readLocations reads the places and the volumes; safe off the loop. A
// listing UDisks2 refuses lists the places alone.
func (c *Chooser) readLocations() locations {
	l := locations{places: c.d.Locations()}
	drives := c.drives()
	if drives == nil {
		return l
	}
	ctx, cancel := context.WithTimeout(context.Background(), drivesListTimeout)
	defer cancel()
	vols, err := drives.Volumes(ctx)
	if err != nil {
		log.Printf("file chooser: drives: %v", err)
	}
	l.volumes = vols
	return l
}

// watchLocations rereads Locations whenever a mount or a drive comes,
// goes or changes, until the returned stop.
func (c *Chooser) watchLocations() (stop func()) {
	var stops []func()
	changed := func() {
		l := c.readLocations()
		c.d.Invoke(func() { c.refreshLocations(l) })
	}
	if c.d.WatchLocations != nil {
		stops = append(stops, c.d.WatchLocations(changed))
	}
	if drives := c.drives(); drives != nil {
		stop, err := drives.Watch(changed)
		if err != nil {
			log.Printf("file chooser: %v", err)
		} else {
			stops = append(stops, stop)
		}
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}

// refreshLocations rebuilds Locations: a mount or a drive came or went.
func (c *Chooser) refreshLocations(l locations) {
	if c.reply != nil && c.ui != nil {
		c.fillLocations(l)
	}
}

// fillLocations lists the places, a mounted volume in place of the
// mount it is, and then the volumes not mounted, to mount on a click.
func (c *Chooser) fillLocations(l locations) {
	list := c.ui.locations
	list.Clear()
	byMount := map[string]udisks.Volume{}
	for _, v := range l.volumes {
		for _, mp := range v.MountPoints {
			byMount[mp] = v
		}
	}
	shown := map[dbus.ObjectPath]bool{}
	for _, p := range l.places {
		v, ok := byMount[p.Path]
		if !ok {
			list.Append(c.placeButton(p, func() { c.goTo(p.Path) }), false)
			continue
		}
		shown[v.Object] = true
		list.Append(c.volumeRow(v, Place{Label: p.Label, Path: p.Path, Icon: volumeIcon(v)}), false)
	}
	for _, v := range l.volumes {
		if shown[v.Object] {
			continue
		}
		at := ""
		if v.Mounted() {
			at = v.MountPoints[0]
		}
		list.Append(c.volumeRow(v, Place{Label: v.Label, Path: at, Icon: volumeIcon(v)}), false)
	}
}

// volumeIcon is the icon UDisks2 hints for v, else the drive's kind.
func volumeIcon(v udisks.Volume) string {
	switch {
	case v.Icon != "":
		return v.Icon
	case v.Locked:
		return "ld-lock-symbolic"
	case v.Removable:
		return "ld-usb-symbolic"
	}
	return "ld-hard-drive-symbolic"
}

// volumeRow is a volume's sidebar row: mounted, it opens where and, on
// a removable drive, has an eject button; not mounted, it mounts it,
// asking for the passphrase of a locked one first.
func (c *Chooser) volumeRow(v udisks.Volume, p Place) widget.Widget {
	var open func()
	switch {
	case v.Mounted():
		open = func() { c.goTo(p.Path) }
	case v.Locked:
		open = func() { c.askPassphrase(v) }
	default:
		open = func() { c.mountVolume(v.Object, v.Label) }
	}
	b := c.placeButton(p, open)
	if !v.Mounted() || !v.Removable {
		return b
	}
	row := widget.NewBox(widget.Row, 0, 0)
	row.Append(b, true)
	row.AppendAligned(c.iconButton("ld-eject-symbolic", "Eject “"+p.Label+"”", func() { c.ejectVolume(v, p.Label) }), false, widget.AlignCenter)
	return row
}

// mountVolume mounts the filesystem on object off the loop and opens
// where it was mounted; a refusal is a warning.
func (c *Chooser) mountVolume(object dbus.ObjectPath, label string) {
	drives := c.drives()
	if drives == nil {
		return
	}
	type mounted struct {
		at  string
		err error
	}
	offLoopFor(c, func() mounted {
		ctx, cancel := context.WithTimeout(context.Background(), drivesActionTimeout)
		defer cancel()
		at, err := drives.Mount(ctx, object)
		return mounted{at, err}
	}, func(m mounted) {
		if m.err != nil {
			c.warn("Cannot mount “" + label + "”: " + udisks.Message(m.err))
			return
		}
		c.clearNotice()
		c.goTo(m.at)
	})
}

// ejectVolume ejects v off the loop, leaving its folder first when it
// is shown; a volume in use says so.
func (c *Chooser) ejectVolume(v udisks.Volume, label string) {
	drives := c.drives()
	if drives == nil {
		return
	}
	offLoopFor(c, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), drivesActionTimeout)
		defer cancel()
		return drives.Eject(ctx, v)
	}, func(err error) {
		switch {
		case errors.Is(err, udisks.ErrBusy):
			c.warn("“" + label + "” is in use. Close the files on it, then eject it again.")
		case err != nil:
			c.warn("Cannot eject “" + label + "”: " + udisks.Message(err))
		default:
			c.clearNotice()
			if slices.ContainsFunc(v.MountPoints, func(mp string) bool { return underRoot(c.s.dir, mp) }) {
				c.goTo(c.d.Home)
			}
		}
	})
}

// underRoot reports dir at or below root.
func underRoot(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, strings.TrimSuffix(root, "/")+"/")
}

// unlockRow is the sidebar's passphrase prompt for a locked volume:
// the volume asked about, the passphrase entry, and what went wrong.
type unlockRow struct {
	box    *widget.Box
	title  *widget.Label
	entry  *widget.Entry
	err    *widget.Label
	volume udisks.Volume
}

func (c *Chooser) newUnlockRow() *unlockRow {
	th := widget.Current()
	u := &unlockRow{box: widget.NewBox(widget.Column, th.Spacing/2, th.Padding/2)}
	u.box.AddClass("file-chooser-unlock")
	u.title = c.label("", "file-chooser-unlock-title", css.Caption)
	u.title.SetWrap(true)
	u.entry = widget.NewEntry(c.d.Font, widgets.TextPx(styling.Body, 1), 0)
	u.entry.AddClass("file-chooser-passphrase")
	u.entry.SetEcho(widget.EchoPassword)
	u.entry.SetPlaceholder("Passphrase")
	u.entry.OnActivate = func(string) { c.unlockVolume() }
	u.entry.OnEscape = func() bool { c.closeUnlock(); return true }
	u.err = c.label("", "file-chooser-unlock-error", css.Caption, css.Warning)
	u.err.SetWrap(true)
	u.err.SetVisible(false)
	unlock, _ := c.textButton("Unlock", "file-chooser-unlock-button", css.SuggestedAction, c.unlockVolume)
	u.box.Append(u.title, false)
	u.box.Append(u.entry, false)
	u.box.Append(u.err, false)
	u.box.Append(unlock, false)
	u.box.SetVisible(false)
	return u
}

// askPassphrase opens the passphrase prompt for a locked volume.
func (c *Chooser) askPassphrase(v udisks.Volume) {
	u := c.ui.unlock
	u.volume = v
	u.title.SetText("Unlock “" + v.Label + "”")
	u.entry.SetText("")
	u.err.SetVisible(false)
	u.box.SetVisible(true)
	c.focus(u.entry)
}

func (c *Chooser) closeUnlock() {
	c.ui.unlock.entry.SetText("")
	c.ui.unlock.box.SetVisible(false)
	c.focus(c.focusView())
}

// unlockVolume unlocks the volume asked about with the passphrase
// typed, off the loop, then mounts it and opens it. The passphrase
// leaves the entry at once and lives in a slice zeroed after the call;
// a wrong one keeps the prompt open with UDisks2's answer.
func (c *Chooser) unlockVolume() {
	u, drives := c.ui.unlock, c.drives()
	if drives == nil || u.entry.Text() == "" {
		return
	}
	passphrase := []byte(u.entry.Text())
	u.entry.SetText("")
	v := u.volume
	type unlocked struct {
		object dbus.ObjectPath
		err    error
	}
	offLoopFor(c, func() unlocked {
		ctx, cancel := context.WithTimeout(context.Background(), drivesActionTimeout)
		defer cancel()
		object, err := drives.Unlock(ctx, v.Object, passphrase)
		return unlocked{object, err}
	}, func(r unlocked) {
		if r.err != nil {
			u.err.SetText(udisks.Message(r.err))
			u.err.SetVisible(true)
			c.focus(u.entry)
			return
		}
		c.closeUnlock()
		c.mountVolume(r.object, v.Label)
	})
}
