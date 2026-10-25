package bar

import (
	"context"
	"errors"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/portal"
	"github.com/stubbedev/lys-shell/service/network"
)

// This file is the network dropdown's VPNs: the section listing NM's
// VPN profiles (dropdowns/network/vpn_connections), each row toggling
// its tunnel and opening it in the editor, and the editor itself
// (vpn_form), the dropdown's second page. The form is built from the
// picked kind's fields rather than drawn per VPN type, so a plugin
// lys-shell has never heard of still gets the free-form key = value
// editor and lands in NetworkManager the same way.

// vpnControl is the VPN profiles the dropdown drives
// (*network.VPNService).
type vpnControl interface {
	Entries() []network.VPN
	Toggle(ctx context.Context, uuid string) error
	Add(ctx context.Context, kind, name string, values map[string]string) error
	Update(ctx context.Context, uuid, kind, name string, values map[string]string) error
	Remove(ctx context.Context, uuid string) error
	SettingsOf(ctx context.Context, uuid string) (network.ConnectionDict, error)
}

// fileOpener is the portal's file chooser (portal.OpenFile on the
// session bus).
type fileOpener func(ctx context.Context, title string, filters ...portal.FileFilter) (string, error)

// openFileOnSessionBus is the live fileOpener.
func openFileOnSessionBus(ctx context.Context, title string, filters ...portal.FileFilter) (string, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return "", err
	}
	return portal.OpenFile(ctx, conn, title, filters...)
}

// vpnFieldsMaxHeight is FIELDS_MAX_HEIGHT: WireGuard's nine fields run
// past the popover, so the form scrolls past this instead.
const vpnFieldsMaxHeight = 260

// The WireGuard field holding this end's private key, and the glyph of
// the button that generates one.
const (
	wireGuardPrivateKey = "private-key"
	iconGenerateKey     = "ld-refresh-cw-symbolic"
)

// vpnStateIcon is a row's glyph (vpn_item.rs's state_icon): the
// dropdown's own, not the bar icon the user themes. A failed attempt
// reads as not connected; the reason is the row's second line.
func vpnStateIcon(state network.VPNState) string {
	switch state {
	case network.VPNConnected:
		return "ld-lock-symbolic"
	case network.VPNConnecting:
		return "ld-refresh-cw-symbolic"
	}
	return "ld-unplug-symbolic"
}

// vpnStateLabel is state_label.
func vpnStateLabel(state network.VPNState) string {
	switch state {
	case network.VPNConnected:
		return i18n.T("dropdown-network-vpn-connected")
	case network.VPNConnecting:
		return i18n.T("dropdown-network-vpn-connecting")
	case network.VPNFailed:
		return i18n.T("dropdown-network-vpn-failed")
	}
	return i18n.T("dropdown-network-vpn-disconnected")
}

// vpnRowCaption is the row's second line: the failure reason as a
// sentence when there is one, the state otherwise.
func vpnRowCaption(row network.VPN) string {
	if row.Detail != "" {
		return sentence(row.Detail)
	}
	return vpnStateLabel(row.State)
}

// sentence capitalises a reason for its own line (vpn_item.rs): lys-shell's
// errors are written lowercase to read mid-sentence in a log; only the
// first letter is touched.
func sentence(reason string) string {
	reason = strings.TrimSpace(reason)
	first, size := utf8.DecodeRuneInString(reason)
	if size == 0 {
		return ""
	}
	return string(unicode.ToUpper(first)) + reason[size:]
}

// vpnSlug is slug: a plugin key as a Fluent message-id fragment.
// vpnc's keys are "IPSec gateway" and "Xauth password", which verbatim
// build an id no Fluent file can declare.
func vpnSlug(key string) string {
	var b strings.Builder
	for _, c := range key {
		switch {
		case c < utf8.RuneSelf && (unicode.IsLetter(c) || unicode.IsDigit(c)):
			b.WriteRune(unicode.ToLower(c))
		case !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// vpnTranslated is the shell's string for id, or fallback when lys-shell
// ships none: what keeps an unknown plugin's vocabulary usable.
func vpnTranslated(id, fallback string) string {
	if i18n.Shell().Has(id) {
		return i18n.T(id)
	}
	return fallback
}

// vpnFieldName is label_for.
func vpnFieldName(f network.VPNField) string {
	return vpnTranslated("dropdown-network-vpn-field-"+vpnSlug(f.Key), f.Label)
}

// vpnFieldLabel is field_label: the name, required ones marked.
func vpnFieldLabel(f network.VPNField) string {
	if f.Required {
		return vpnFieldName(f) + " *"
	}
	return vpnFieldName(f)
}

// vpnSectionHeading is section_heading's text.
func vpnSectionHeading(section string) string {
	return vpnTranslated("dropdown-network-vpn-section-"+section, section)
}

// vpnChoiceLabel is choice_label: a choice lys-shell cannot sign into says
// so before it is picked.
func vpnChoiceLabel(c network.VPNChoice) string {
	if c.NativeSignIn {
		return c.Label
	}
	return c.Label + " — " + i18n.T("dropdown-network-vpn-no-native-sign-in")
}

// parseRaw is parse_raw: one key = value per line, # comments, blank
// lines and lines without a key ignored.
func parseRaw(text string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if key = strings.TrimSpace(key); ok && key != "" {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

// vpnTyped is one typed box's key and value.
type vpnTyped struct{ key, value string }

// mergeVPNValues is merge: the raw editor's keys with the typed fields
// over them. A typed field wins (it is the one the user sees), an
// emptied box clears its key rather than reviving the raw one: no blank
// value reaches NetworkManager.
func mergeVPNValues(raw map[string]string, typed []vpnTyped) map[string]string {
	values := maps.Clone(raw)
	if values == nil {
		values = map[string]string{}
	}
	for _, t := range typed {
		values[t.key] = t.value
	}
	maps.DeleteFunc(values, func(_, v string) bool { return strings.TrimSpace(v) == "" })
	return values
}

// vpnLeftovers is leftovers: the saved values the kind's form has no
// box for, kept in the raw editor so an edit never drops them.
func vpnLeftovers(values map[string]string, kind network.VPNKind) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		if !slices.ContainsFunc(kind.Fields, func(f network.VPNField) bool { return f.Key == k }) {
			out[k] = v
		}
	}
	return out
}

// renderRaw is render_raw: the values as the raw editor shows them,
// sorted.
func renderRaw(values map[string]string) string {
	lines := make([]string, 0, len(values))
	for k, v := range values {
		lines = append(lines, k+" = "+v)
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// vpnMalformedMessage is malformed_message: what the box wanted.
func vpnMalformedMessage(f network.VPNField) string {
	id := map[network.VPNFormat]string{
		network.FormatText:     "dropdown-network-vpn-invalid-text",
		network.FormatHost:     "dropdown-network-vpn-invalid-host",
		network.FormatHostPort: "dropdown-network-vpn-invalid-host-port",
		network.FormatIPList:   "dropdown-network-vpn-invalid-ip-list",
		network.FormatCIDRList: "dropdown-network-vpn-invalid-cidr-list",
		network.FormatKey:      "dropdown-network-vpn-invalid-key",
		network.FormatNumber:   "dropdown-network-vpn-invalid-number",
	}[f.Format]
	if id == "" {
		id = "dropdown-network-vpn-invalid-text"
	}
	return i18n.T(id, i18n.Str("field", vpnFieldName(f)))
}

// vpnMalformed is malformed: every field holding a value not in its
// format.
func vpnMalformed(kind network.VPNKind, values map[string]string) []network.VPNField {
	var out []network.VPNField
	for _, f := range kind.Fields {
		if v, ok := values[f.Key]; ok && !f.Format.Accepts(v) {
			out = append(out, f)
		}
	}
	return out
}

// vpnMissingRequired is missing_required: every required field left
// empty, all at once, in drawing order.
func vpnMissingRequired(kind network.VPNKind, values map[string]string) []network.VPNField {
	var out []network.VPNField
	for _, f := range kind.Fields {
		if f.Required && values[f.Key] == "" {
			out = append(out, f)
		}
	}
	return out
}

// vpnRawHint is raw_hint: for a typed kind the keys it covers, for an
// untyped one the plugin's service its documentation is indexed by.
func vpnRawHint(kind network.VPNKind, ok bool) string {
	switch {
	case !ok:
		return i18n.T("dropdown-network-vpn-raw-hint")
	case !kind.IsTyped():
		return i18n.T("dropdown-network-vpn-raw-hint-unknown", i18n.Str("service", kind.ID))
	}
	keys := make([]string, len(kind.Fields))
	for i, f := range kind.Fields {
		keys[i] = f.Key
	}
	return i18n.T("dropdown-network-vpn-raw-hint-typed", i18n.Str("covered", strings.Join(keys, ", ")))
}

// publicKeyReadout is attach_key_generation's readout: the public key
// for the private key typed, and nothing for a half-typed or mistyped
// one (an old public key would hand the other end a key that opens
// nothing).
func publicKeyReadout(private string) string {
	if public, ok := network.PublicKeyFor(private); ok {
		return i18n.T("dropdown-network-vpn-public-key", i18n.Str("key", public))
	}
	return i18n.T("dropdown-network-vpn-public-key-empty")
}

// kindsWith is the kinds the form offers, plus the one a saved profile
// names when this machine no longer has its plugin: the profile still
// opens, in the raw editor, and saving keeps its type instead of
// rewriting it as the first kind.
func kindsWith(kinds []network.VPNKind, kind string) []network.VPNKind {
	if kind == "" || slices.ContainsFunc(kinds, func(k network.VPNKind) bool { return k.ID == kind }) {
		return kinds
	}
	return append(slices.Clone(kinds), network.VPNKind{ID: kind, Label: kind})
}

// readWgQuick reads and parses a wg-quick file.
func readWgQuick(path string) (map[string]string, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // the file the user picked
	if err != nil {
		return nil, false
	}
	return network.ParseWgQuick(string(data), filepath.Base(path))
}

// netVPNs is VpnConnections: the VPN label, one row per profile, and
// the "Add VPN" row that gives a machine with none its first.
type netVPNs struct {
	*widget.Box
	v    *networkView
	rows *widget.ItemGroup
	add  *widget.Item
}

func newNetVPNs(v *networkView) *netVPNs {
	s := &netVPNs{v: v}
	card, rows := itemCard()
	card.AddClass("network-list")
	s.rows = rows
	s.Box = dropdownSection(sectionLabel(v.font, i18n.T("dropdown-network-vpn")), card)
	// The add row activates as a whole (vpn_connections/mod.rs:75-107).
	s.add = widget.NewItem(v.font, i18n.T("dropdown-network-vpn-add"), "")
	s.add.SetSmall(true)
	s.add.AddClass("vpn-item", "vpn-add")
	s.add.SetOnActivate(v.vpnAdd)
	s.add.SetMedia(widget.NewThemeIcon("ld-plus-symbolic", itemIconPx), false)
	s.rows.Append(s.add)
	s.SetVisible(v.vpn != nil)
	return s
}

// apply rebuilds the rows from one read of the profiles, the add row
// last.
func (s *netVPNs) apply(rows []network.VPN) {
	s.rows.Clear()
	for _, row := range rows {
		s.rows.Append(s.row(row))
	}
	s.rows.Append(s.add)
}

// row is VpnItem on lys-ui's Item: the state glyph as the media, the
// name over the state or the gateway's reason, and the edit button as
// the action. Activating the row toggles the tunnel (vpn_item.rs:
// 190-197); the edit button keeps its own click. The name and a
// reason keep to one line each, the tooltip holding the reason whole.
func (s *netVPNs) row(r network.VPN) *widget.Item {
	v := s.v
	uuid := r.UUID
	row := widget.NewItem(v.font, r.Name, vpnRowCaption(r))
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.AddClass("vpn-item")
	row.SetOnActivate(func() { v.vpnToggle(uuid) })
	icon := widget.NewThemeIcon(vpnStateIcon(r.State), itemIconPx)
	icon.AddClass("vpn-state-icon")
	switch r.State {
	case network.VPNConnected:
		row.AddClass("connected")
	case network.VPNConnecting:
		row.AddClass("connecting")
	}
	row.SetMedia(icon, false)
	if r.Detail != "" {
		row.AddClass("failed")
		row.SetTooltip(r.Detail)
	}
	gear := widget.NewThemeIcon("ld-settings-symbolic", int(v.px))
	edit := dropdownButton(gear, func() { v.vpnEdit(uuid) }, "network-vpn-edit", css.Ghost, css.Icon, css.Small)
	edit.SetTooltip(i18n.T("dropdown-network-vpn-edit"))
	row.AddAction(edit)
	return row
}

// vpnEntry is a typed box and the key it fills.
type vpnEntry struct {
	key   string
	entry *widget.Entry
}

// vpnPicker is a fixed-choice field and the values behind its labels.
type vpnPicker struct {
	key    string
	picker *widget.Dropdown
	values []string
}

// netVPNForm is VpnForm: the editor page. The header goes back and, for
// WireGuard, imports a wg-quick file; the scrolled body holds the name,
// the type picker (creating only: another type is another profile),
// the kind's fields, and the raw editor; the footer holds the error and
// Delete (confirmed in a modal alert), Cancel, Save.
type netVPNForm struct {
	*widget.Box
	v          *networkView
	kinds      []network.VPNKind
	selected   int
	editing    string // the profile's UUID; "" creates one
	head       *widget.Item
	importBtn  *widget.Button
	name       *widget.Entry
	kindField  *widget.Field
	kindSlot   *widget.Box
	kindPicker *widget.Dropdown
	fields     *widget.Box
	entries    []vpnEntry
	pickers    []vpnPicker
	advanced   *widget.Button
	advLabel   *widget.Label
	rawHint    *widget.Label
	raw        *widget.TextArea
	rawTyped   bool // the kind has a typed form; the raw editor is extra
	rawOpen    bool
	errLabel   *widget.Label
	deleteBtn  *widget.Button
	// confirming is confirming_delete: the alert is up, a second
	// Delete click asks nothing more.
	confirming bool
}

func newNetVPNForm(v *networkView) *netVPNForm {
	th := widget.Current()
	f := &netVPNForm{v: v, Box: widget.NewBox(widget.Column, 0, 0)}
	f.AddClass(css.Card, "network-vpn-form")

	// The header goes back, names the page, and imports. Its title is
	// always "New VPN" (vpn_form/mod.rs:341-346): creating and editing
	// share it.
	f.head = widget.NewItem(v.font, i18n.T("dropdown-network-vpn-new"), "")
	f.head.SetSmall(true)
	backBtn := dropdownButton(widget.NewThemeIcon("ld-arrow-left-symbolic", int(v.px)), f.cancel, "network-vpn-back", css.Ghost, css.Icon, css.Small)
	backBtn.SetTooltip(i18n.T("dropdown-network-vpn-back"))
	f.head.SetMedia(backBtn, true)
	f.importBtn = dropdownButton(widget.NewThemeIcon("ld-folder-open-symbolic", int(v.px)), f.importFile, "network-vpn-import", css.Ghost, css.Icon, css.Small)
	f.importBtn.SetTooltip(i18n.T("dropdown-network-vpn-import"))
	f.head.AddAction(f.importBtn)
	f.Append(f.head, false)

	body := widget.NewBox(widget.Column, th.Spacing, 0)
	f.name = widget.NewEntry(v.font, v.px, 0)
	f.name.AddClass("network-password-input")
	body.Append(widget.NewField(v.font, i18n.T("dropdown-network-vpn-name"), f.name), false)
	f.kindSlot = widget.NewBox(widget.Column, 0, 0)
	f.kindField = widget.NewField(v.font, i18n.T("dropdown-network-vpn-type"), f.kindSlot)
	body.Append(f.kindField, false)
	f.fields = widget.NewBox(widget.Column, th.Spacing, 0)
	body.Append(f.fields, false)
	f.advLabel = widget.NewLabel(v.font, v.px, "", 0)
	f.advanced = dropdownButton(f.advLabel, f.toggleAdvanced, "network-vpn-advanced", css.Ghost, css.Small)
	f.advanced.SetHAlign(widget.AlignStart)
	body.Append(f.advanced, false)
	f.rawHint = widget.NewLabel(v.font, v.px, "", 0)
	f.rawHint.AddClass(css.Caption, css.DimLabel)
	f.rawHint.SetWrap(true)
	body.Append(f.rawHint, false)
	f.raw = widget.NewTextArea(monoFont(v.ctx, v.font, v.px), v.px*0.9, 0)
	f.raw.AddClass("network-vpn-raw")
	body.Append(f.raw, false)
	scroll := dropdownScroll(body, "network-vpn-form-scroll")
	scroll.SetMaxContentHeight(vpnFieldsMaxHeight)

	foot := itemBody()
	foot.Append(scroll, true)
	f.errLabel = v.errorLabel()
	foot.Append(f.errLabel, false)
	f.deleteBtn = v.ghostText(i18n.T("dropdown-network-vpn-delete"), "network-vpn-delete", f.askDelete)
	foot.Append(formActions(
		f.deleteBtn,
		v.outlineText(i18n.T("dropdown-network-cancel"), "network-password-cancel", f.cancel),
		v.primaryText(i18n.T("dropdown-network-vpn-save"), "network-password-connect", f.save),
	), false)
	f.Append(foot, true)
	return f
}

// monoFont is the configured monospace face for the raw editor, the
// dropdown's own when it cannot be loaded.
func monoFont(ctx ModuleContext, fallback render.Font, px float64) render.Font {
	if ctx.Config == nil || ctx.Config.General.FontMono == "" {
		return fallback
	}
	face, err := app.Font(ctx.Config.General.FontMono, px)
	if err != nil {
		return fallback
	}
	return app.FontFallback(face)
}

// kind is the picked kind.
func (f *netVPNForm) kind() (network.VPNKind, bool) {
	if f.selected < 0 || f.selected >= len(f.kinds) {
		return network.VPNKind{}, false
	}
	return f.kinds[f.selected], true
}

// open resets the form onto kinds, picking kind (the first when "").
func (f *netVPNForm) open(uuid, name, kind string, values map[string]string) {
	var kinds []network.VPNKind
	if f.v.vpnKinds != nil {
		kinds = f.v.vpnKinds()
	}
	f.kinds = kindsWith(kinds, kind)
	f.selected = max(0, slices.IndexFunc(f.kinds, func(k network.VPNKind) bool { return k.ID == kind }))
	f.editing = uuid
	f.setError("")
	f.name.SetText(name)
	labels := make([]string, len(f.kinds))
	for i, k := range f.kinds {
		labels[i] = k.Label
	}
	f.kindPicker = widget.NewDropdown(f.v.font, f.v.px, labels, f.selected)
	f.kindPicker.OnSelect = f.kindSelected
	f.kindSlot.Clear()
	f.kindSlot.Append(f.kindPicker, false)
	creating := uuid == ""
	f.kindField.SetVisible(creating)
	f.deleteBtn.SetVisible(!creating)
	f.rebuild(values)
}

// showNew is VpnFormInput::ShowNew: empty, the name box focused.
func (f *netVPNForm) showNew() {
	f.open("", "", "", nil)
	f.v.focus(f.name)
}

// showEdit is VpnFormInput::ShowEdit: prefilled from what is stored.
func (f *netVPNForm) showEdit(uuid, name, kind string, values map[string]string) {
	f.open(uuid, name, kind, values)
}

// kindSelected is KindSelected: another type, other fields.
func (f *netVPNForm) kindSelected(i int) {
	if i < 0 || i >= len(f.kinds) || i == f.selected {
		return
	}
	f.selected = i
	f.setError("")
	f.rebuild(nil)
}

// rebuild redraws the fields for the picked kind, prefilled from
// values. A kind without a typed form is the raw editor, open; a typed
// one keeps it behind the advanced toggle, holding what the form has no
// box for, and opened when that is anything.
func (f *netVPNForm) rebuild(values map[string]string) {
	f.entries, f.pickers = nil, nil
	f.fields.Clear()
	kind, ok := f.kind()
	f.importBtn.SetVisible(ok && kind.ID == network.WireGuard)
	f.rawTyped = ok && kind.IsTyped()
	if !f.rawTyped {
		f.raw.SetText(renderRaw(values))
		f.rawOpen = true
		f.syncRaw(kind, ok)
		return
	}
	extra := vpnLeftovers(values, kind)
	f.raw.SetText(renderRaw(extra))
	f.rawOpen = len(extra) > 0
	section := ""
	for _, field := range kind.Fields {
		if field.Section != "" && field.Section != section {
			f.fields.Append(sectionLabel(f.v.font, vpnSectionHeading(field.Section)), false)
			section = field.Section
		}
		current, has := values[field.Key]
		var control widget.Widget
		if len(field.Choices) > 0 {
			control = f.picker(field, current, has)
		} else {
			control = f.entry(kind, field, current)
		}
		row := widget.NewField(f.v.font, vpnFieldLabel(field), control)
		if !field.Required {
			row.AddClass("optional")
		}
		f.fields.Append(row, false)
	}
	f.syncRaw(kind, ok)
}

// picker draws a fixed-choice field. A stored value this build does not
// offer keeps the first choice rather than rewriting itself unseen.
func (f *netVPNForm) picker(field network.VPNField, current string, has bool) *widget.Dropdown {
	labels := make([]string, len(field.Choices))
	values := make([]string, len(field.Choices))
	for i, c := range field.Choices {
		labels[i], values[i] = vpnChoiceLabel(c), c.Value
	}
	selected := 0
	if i := slices.Index(values, current); has && i >= 0 {
		selected = i
	}
	p := widget.NewDropdown(f.v.font, f.v.px, labels, selected)
	f.pickers = append(f.pickers, vpnPicker{key: field.Key, picker: p, values: values})
	return p
}

// entry draws a typed box: masked with the reveal toggle when secret,
// and for WireGuard's private key with the generate button and the
// public key it derives under it.
func (f *netVPNForm) entry(kind network.VPNKind, field network.VPNField, current string) widget.Widget {
	var entry *widget.Entry
	if field.Secret {
		entry = newSecretEntry(f.v.font, f.v.px).entry
	} else {
		entry = widget.NewEntry(f.v.font, f.v.px, 0)
	}
	entry.AddClass("network-password-input")
	input := widget.NewBox(widget.Column, widget.Current().Spacing/2, 0)
	input.Append(entry, false)
	entry.SetPlaceholder(field.Placeholder)
	entry.SetText(current)
	entry.OnActivate = func(string) { f.save() }
	var readout *widget.RichLabel
	// WireGuard's key generation is the entry's primary icon in Rust
	if kind.ID == network.WireGuard && field.Key == wireGuardPrivateKey {
		// The entry's primary icon generates a keypair
		// (vpn_form/mod.rs:147-160); the reveal toggle keeps the
		// secondary slot.
		entry.SetLeadingIcon(iconGenerateKey, f.v.px, func() {
			entry.SetText(network.GenerateKeyPair().Private)
		})
		readout = widget.NewRichLabel(f.v.font, f.v.px*0.85, publicKeyReadout(current), 0)
		readout.SetVariants(app.FontVariantsOf(f.v.font))
		readout.AddClass("network-vpn-public-key", css.Caption, css.DimLabel, css.Monospace)
		readout.SetWrap(true)
		readout.SetSelectable(true)
		input.Append(readout, false)
	}
	// A box stops being wrong the moment it is edited.
	entry.OnChanged = func(text string) {
		entry.RemoveClass("error")
		if readout != nil {
			readout.SetMarkup(publicKeyReadout(text))
		}
	}
	f.entries = append(f.entries, vpnEntry{key: field.Key, entry: entry})
	return input
}

// syncRaw shows the raw editor's parts for the current state.
func (f *netVPNForm) syncRaw(kind network.VPNKind, ok bool) {
	f.advanced.SetVisible(f.rawTyped)
	if f.rawOpen {
		f.advLabel.SetText(i18n.T("dropdown-network-vpn-advanced-hide"))
	} else {
		f.advLabel.SetText(i18n.T("dropdown-network-vpn-advanced-show"))
	}
	f.rawHint.SetText(vpnRawHint(kind, ok))
	f.rawHint.SetVisible(f.rawOpen)
	f.raw.SetVisible(f.rawOpen)
}

func (f *netVPNForm) toggleAdvanced() {
	f.rawOpen = !f.rawOpen
	kind, ok := f.kind()
	f.syncRaw(kind, ok)
}

// values is both editors: the raw keys with the typed boxes and the
// pickers over them.
func (f *netVPNForm) values() map[string]string {
	typed := make([]vpnTyped, 0, len(f.entries)+len(f.pickers))
	for _, e := range f.entries {
		typed = append(typed, vpnTyped{e.key, e.entry.Text()})
	}
	for _, p := range f.pickers {
		if i := p.picker.Selected(); i >= 0 && i < len(p.values) {
			typed = append(typed, vpnTyped{p.key, p.values[i]})
		}
	}
	return mergeVPNValues(parseRaw(f.raw.Text()), typed)
}

// save validates, marks every refused box, and hands a valid profile to
// the view, which writes it off the loop.
func (f *netVPNForm) save() {
	kind, ok := f.kind()
	if !ok {
		return
	}
	name := f.name.Text()
	if strings.TrimSpace(name) == "" {
		f.setError(i18n.T("dropdown-network-vpn-name-required"))
		return
	}
	values := f.values()
	missing := vpnMissingRequired(kind, values)
	malformed := vpnMalformed(kind, values)
	f.markBad(append(missing, malformed...))
	switch {
	case len(missing) > 0:
		f.setError(i18n.T("dropdown-network-vpn-field-required", i18n.Str("field", vpnFieldName(missing[0]))))
		return
	case len(malformed) > 0:
		f.setError(vpnMalformedMessage(malformed[0]))
		return
	}
	f.setError("")
	f.v.vpnSave(f.editing, kind.ID, name, values)
}

// markBad puts the error mark on the refused boxes and takes it off the
// rest.
func (f *netVPNForm) markBad(bad []network.VPNField) {
	for _, e := range f.entries {
		if slices.ContainsFunc(bad, func(b network.VPNField) bool { return b.Key == e.key }) {
			e.entry.AddClass("error")
		} else {
			e.entry.RemoveClass("error")
		}
	}
}

// fail is VpnFormInput::Failed: NM refused; the form stays, values
// intact, so the user can correct what it refused.
func (f *netVPNForm) fail(reason string) { f.setError(reason) }

func (f *netVPNForm) setError(reason string) {
	f.errLabel.SetText(reason)
	f.errLabel.SetVisible(reason != "")
}

func (f *netVPNForm) cancel() {
	f.v.vpnCloseForm()
}

// askDelete is confirm_delete: a profile is recoverable from nowhere
// else (a WireGuard private key lives only in NM), so Delete asks in a
// modal alert - Cancel the default - and nothing goes until it is
// accepted.
func (f *netVPNForm) askDelete() {
	if f.editing == "" || f.confirming {
		return
	}
	f.confirming = true
	uuid := f.editing
	f.v.ctx.showAlert(alertSpec{
		message: i18n.T("dropdown-network-vpn-delete-confirm", i18n.Str("name", f.name.Text())),
		detail:  i18n.T("dropdown-network-vpn-delete-confirm-detail"),
		cancel:  i18n.T("dropdown-network-cancel"),
		accept:  i18n.T("dropdown-network-vpn-delete"),
	}, func(accepted bool) {
		f.confirming = false
		if accepted {
			f.v.vpnDelete(uuid)
		}
	})
}

// importFile is ImportClicked: a wg-quick file through the portal's
// chooser, its outcome handled back on the loop.
func (f *netVPNForm) importFile() {
	open := f.v.openFile
	if open == nil {
		return
	}
	life := f.v.life
	go func() {
		path, err := open(life, i18n.T("dropdown-network-vpn-import"),
			portal.FileFilter{Name: i18n.T("dropdown-network-vpn-import-filter"), Patterns: []string{"*.conf"}})
		f.v.ctx.Invoke(func() { f.importDone(path, err) })
	}()
}

// importDone reads the picked file here rather than off the loop, as
// the Rust form does: a wg-quick file is a few hundred bytes on local
// disk. Dismissing the chooser is not a failure, and saying so would
// be noise.
func (f *netVPNForm) importDone(path string, err error) {
	switch {
	case errors.Is(err, portal.ErrCancelled):
		return
	case err != nil:
		log.Printf("network: VPN import: %v", err)
		return
	}
	if values, ok := readWgQuick(path); ok {
		f.imported(values["interface"], values)
	} else {
		f.setError(i18n.T("dropdown-network-vpn-import-failed"))
	}
}

// imported is Imported: the file's values, and its interface as the
// name unless one is typed already.
func (f *netVPNForm) imported(name string, values map[string]string) {
	f.setError("")
	if strings.TrimSpace(f.name.Text()) == "" {
		f.name.SetText(name)
	}
	f.rebuild(values)
}

// The page names in the dropdown's body stack.
const (
	netPageBrowse = "browse"
	netPageEdit   = "edit"
)

// vpnToggle connects a VPN that is down and disconnects one that is up;
// the rows follow NM's state on their own.
func (v *networkView) vpnToggle(uuid string) {
	vpn := v.vpn
	if vpn == nil {
		return
	}
	go func() {
		if err := vpn.Toggle(context.Background(), uuid); err != nil {
			log.Printf("network: VPN toggle: %v", err)
		}
	}()
}

// vpnAdd opens the editor empty.
func (v *networkView) vpnAdd() {
	v.vpnForm.showNew()
	v.body.Show(netPageEdit)
}

// vpnEdit reads the saved profile (not what the row shows) and opens
// the editor on it. A profile that cannot be read stays closed: an
// editor open on stale values could save them over it. (Rust logs and
// keeps the list, as here.)
func (v *networkView) vpnEdit(uuid string) {
	vpn := v.vpn
	if vpn == nil {
		return
	}
	name := ""
	if i := slices.IndexFunc(v.cur.vpns, func(r network.VPN) bool { return r.UUID == uuid }); i >= 0 {
		name = v.cur.vpns[i].Name
	}
	go func() {
		dict, err := vpn.SettingsOf(v.life, uuid)
		v.ctx.Invoke(func() {
			if err != nil {
				log.Printf("network: cannot read VPN profile %s: %v", uuid, err)
				return
			}
			v.vpnForm.showEdit(uuid, name, network.KindOf(dict), network.ReadProfileValues(dict))
			v.body.Show(netPageEdit)
		})
	}()
}

// vpnSave writes the form's profile: created without a UUID, rewritten
// in place with one. The page goes back at once; a refusal brings the
// form back with the reason.
func (v *networkView) vpnSave(uuid, kind, name string, values map[string]string) {
	vpn := v.vpn
	if vpn == nil {
		return
	}
	v.body.Show(netPageBrowse)
	go func() {
		var err error
		if uuid == "" {
			err = vpn.Add(context.Background(), kind, name, values)
		} else {
			err = vpn.Update(context.Background(), uuid, kind, name, values)
		}
		v.vpnWritten(err, "cannot save VPN profile")
	}()
}

// vpnDelete removes a profile.
func (v *networkView) vpnDelete(uuid string) {
	vpn := v.vpn
	if vpn == nil {
		return
	}
	v.body.Show(netPageBrowse)
	go func() { v.vpnWritten(vpn.Remove(context.Background(), uuid), "cannot delete VPN profile") }()
}

// vpnWritten reopens the form with NM's refusal; success needs nothing,
// NM announces the profile and the rows rebuild.
func (v *networkView) vpnWritten(err error, what string) {
	if err == nil {
		return
	}
	log.Printf("network: %s: %v", what, err)
	v.ctx.Invoke(func() {
		v.vpnForm.fail(err.Error())
		v.body.Show(netPageEdit)
	})
}

// vpnCloseForm is VpnFormOutput::Cancel.
func (v *networkView) vpnCloseForm() { v.body.Show(netPageBrowse) }
