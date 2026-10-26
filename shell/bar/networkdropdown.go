package bar

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/network/secrets"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// netScanTimeout is SCAN_TIMEOUT.
const netScanTimeout = 30 * time.Second

// netListState is ListState.
type netListState int

const (
	netNormal netListState = iota
	netPasswordEntry
	netConnecting
	netScanning
)

// netState is one read of NetworkManager for the dropdown.
type netState struct {
	snap    network.Snapshot
	aps     []network.AccessPoint
	known   map[string]bool
	request secrets.Request
	asking  bool
	vpns    []network.VPN
}

// netProgress is ConnectionProgress: the network being joined, its
// current step, and the failure that ended it.
type netProgress struct {
	ssid, step, err string
}

// networkView is the network dropdown: the header (wifi or wired icon,
// scan, the wifi switch), the active connections, NetworkManager's
// pending credential prompt, the VPNs, and the available networks with
// their password form.
type networkView struct {
	ctx  ModuleContext
	font render.Font
	px   float64
	popoverHook

	*widget.Box
	headerIcon   *widget.Icon
	scanBtn      *widget.Button
	wifiSwitch   *widget.Switch
	syncing      bool
	active       *netActive
	secret       *netSecretForm
	vpns         *netVPNs
	vpnForm      *netVPNForm
	body         *widget.Stack
	availSection *widget.Box
	password     *netPasswordForm
	listCard     *widget.Box
	list         *widget.ItemGroup
	noNetworks   *widget.StatusPage
	noAdapter    *widget.StatusPage
	state        netListState
	cur          netState
	cache        []apSnapshot
	selection    *apSnapshot
	pendingSSID  string
	progress     netProgress

	netDeps

	// reads is the view's one reader: NetworkManager ticks, the other
	// feeds, and every re-read after a write go through it.
	reads *refresher[netState]

	once   sync.Once
	life   context.Context
	cancel context.CancelFunc
}

// wifiControl is the wireless device the dropdown drives
// (*network.Wifi).
type wifiControl interface {
	AccessPoints(ctx context.Context) ([]network.AccessPoint, error)
	KnownSSIDs() map[string]bool
	ScanAndWait(ctx context.Context, timeout time.Duration)
	ConnectAndWait(ctx context.Context, ap dbus.ObjectPath, password string, progress func(network.DeviceState)) error
	Disconnect(ctx context.Context) error
	Forget(ctx context.Context, ssid string)
	SetEnabled(ctx context.Context, on bool) error
}

// secretAgent is NetworkManager's pending credential prompt
// (*network.Agent).
type secretAgent interface {
	Request() (secrets.Request, bool)
	Submit(values map[string]string)
	Cancel()
}

// netDeps are the services the dropdown drives beside the snapshot
// source; nil members are absent (no wifi device, no agent).
type netDeps struct {
	wifiCtl wifiControl
	agent   secretAgent
	vpn     vpnControl
	// vpnKinds lists the kinds the VPN editor offers
	// (network.AvailableKinds).
	vpnKinds func() []network.VPNKind
	// openFile is the file chooser a wg-quick import goes through.
	openFile fileOpener
	// changes are the extra feeds a re-read follows (saved profiles,
	// the agent, the VPNs).
	changes []func(context.Context) <-chan struct{}
}

// networkDeps wires the dropdown to the live network service.
func networkDeps(n *network.Service) netDeps {
	d := netDeps{vpnKinds: network.AvailableKinds, openFile: openFileOnSessionBus}
	if n == nil {
		return d
	}
	if n.Wifi != nil {
		d.wifiCtl = n.Wifi
	}
	if n.Agent != nil {
		d.agent = n.Agent
		d.changes = append(d.changes, n.Agent.Changes)
	}
	if n.Settings != nil {
		d.changes = append(d.changes, n.Settings.Changes)
	}
	if n.VPN != nil {
		d.vpn = n.VPN
		d.changes = append(d.changes, n.VPN.Changes)
	}
	return d
}

func networkDropdown(ctx ModuleContext) widget.Widget {
	return newNetworkView(ctx, networkDeps(ctx.Networking))
}

func newNetworkView(ctx ModuleContext, deps netDeps) *networkView {
	font, px := dropdownFont(ctx)
	v := &networkView{ctx: ctx, font: font, px: px, netDeps: deps}
	life, cancel := context.WithCancel(context.Background()) //nolint:gosec // dropdownClosed cancels it
	v.life, v.cancel = life, cancel
	v.reads = newRefresher(ctx, life, v.read, v.apply)
	// The popover frame: the header strip full width over the content
	// (network/mod.rs:63-183).
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "network-dropdown")

	scanIcon := widget.NewThemeIcon("ld-refresh-cw-symbolic", int(px))
	v.scanBtn = dropdownButton(scanIcon, v.startScan, "network-scan-btn")
	v.scanBtn.AddClass(css.Ghost, css.Icon)
	v.wifiSwitch = widget.NewSwitch(false)
	v.wifiSwitch.OnChanged = v.wifiToggled
	header, icon, _ := dropdownHeaderParts(font, "ld-wifi-symbolic", i18n.T("dropdown-network-title"), v.scanBtn, v.wifiSwitch)
	v.headerIcon = icon

	v.active = newNetActive(v)
	v.secret = newNetSecretForm(v)

	// The content wraps the active connections, the secret form, and
	// the body stack (network/mod.rs:129-183).
	content := dropdownContent("network-content")
	content.Append(v.active, false)
	content.Append(v.secret, false)

	browse := widget.NewBox(widget.Column, 2*widget.Current().Spacing, 0)
	v.vpns = newNetVPNs(v)
	browse.Append(v.vpns, false)
	v.password = newNetPasswordForm(v)
	v.list = widget.NewItemGroup()
	v.listCard = widget.NewBox(widget.Column, 0, 0)
	v.listCard.AddClass(css.Card, "network-list")
	v.listCard.Append(dropdownScroll(v.list, "network-list-scroll"), true)
	v.availSection = dropdownSection(sectionLabel(v.font, i18n.T("dropdown-network-available")), v.password, v.listCard)
	browse.Append(v.availSection, true)
	noNetworks := widgets.EmptyState(font, px, "ld-wifi-off-symbolic", i18n.T("dropdown-network-no-networks-title"), i18n.T("dropdown-network-no-networks-description"))
	v.noNetworks = noNetworks
	noAdapter := widgets.EmptyState(font, px, "ld-wifi-off-symbolic", i18n.T("dropdown-network-no-adapter-title"), i18n.T("dropdown-network-no-adapter-description"))
	v.noAdapter = noAdapter
	browse.AppendAligned(v.noNetworks, true, widget.AlignCenter)
	browse.AppendAligned(v.noAdapter, true, widget.AlignCenter)
	// Two pages rather than a form hiding the lists in place: the editor
	// is somewhere you go, and coming back is one button. Each page is its
	// own height and the card tweens between them.
	v.vpnForm = newNetVPNForm(v)
	v.body = pageStack()
	v.body.SetTransition(widget.StackSlideLeftRight, gtkStackDuration)
	v.body.Add(netPageBrowse, browse)
	v.body.Add(netPageEdit, v.vpnForm)
	content.Append(v.body, true)
	v.Append(header, false)
	v.Append(content, true)

	v.apply(v.read(context.Background()))
	v.scanIfEmpty()
	v.follow()
	return v
}

// wifi is the wireless device, nil without one.
func (v *networkView) wifi() wifiControl { return v.wifiCtl }

// read snapshots NetworkManager off the loop.
func (v *networkView) read(ctx context.Context) netState {
	var s netState
	if v.ctx.Network != nil {
		if snap, err := v.ctx.Network.Read(ctx); err == nil {
			s.snap = snap
		}
	}
	if w := v.wifi(); w != nil {
		s.aps, _ = w.AccessPoints(ctx)
		s.known = w.KnownSSIDs()
	}
	if v.agent != nil {
		s.request, s.asking = v.agent.Request()
	}
	if v.vpn != nil {
		s.vpns = v.vpn.Entries()
	}
	return s
}

// apply pushes one read into every part.
func (v *networkView) apply(s netState) {
	v.cur = s
	wifiAvail := v.wifi() != nil
	if wifiAvail {
		v.headerIcon.SetThemeName("ld-wifi-symbolic")
	} else {
		v.headerIcon.SetThemeName("ld-ethernet-port-symbolic")
	}
	v.syncing = true
	v.wifiSwitch.SetOn(s.snap.WifiEnabled)
	v.syncing = false
	v.wifiSwitch.SetVisible(wifiAvail)
	v.scanBtn.SetVisible(wifiAvail && s.snap.WifiEnabled)
	v.scanBtn.SetEnabled(v.state != netScanning)
	if v.state == netScanning {
		v.scanBtn.AddClass("scanning")
	} else {
		v.scanBtn.RemoveClass("scanning")
	}
	if s.snap.Wifi == network.StatusConnected {
		v.progress = netProgress{}
	}
	v.active.apply(s.snap, v.progress)
	v.secret.apply(s.request, s.asking)
	v.vpns.apply(s.vpns)
	if !s.snap.WifiEnabled && wifiAvail {
		v.cache = nil
	}
	v.rebuildList()
}

// rebuildList is rebuild_network_list.
func (v *networkView) rebuildList() {
	wifiAvail := v.wifi() != nil
	connected := ""
	if v.cur.snap.Wifi == network.StatusConnected {
		connected = v.cur.snap.WifiSSID
	}
	live := sortedUniqueAccessPoints(v.cur.aps, connected, v.cur.known)
	// NM can drop the whole list for a moment mid-scan; folding that in
	// would flash every row stale until the results land.
	if len(live) > 0 || len(v.cache) == 0 || v.state != netScanning {
		v.cache = mergeWithCache(live, v.cache, connected, v.cur.known)
	}
	v.list.Clear()
	for _, ap := range v.cache {
		v.list.Append(v.row(ap))
	}
	v.availSection.SetVisible(wifiAvail)
	v.password.SetVisible(wifiAvail && v.state == netPasswordEntry)
	v.listCard.SetVisible(wifiAvail && len(v.cache) > 0)
	v.noNetworks.SetVisible(wifiAvail && len(v.cache) == 0)
	v.noAdapter.SetVisible(!wifiAvail)
}

// row is NetworkItem on lys-ui's Item: the signal icon as the media,
// the name over the security, and on a saved network the forget
// button, shown while hovered, as the action. Activating the row - any
// part of it but the forget button, which keeps its own click - selects
// the network (network_item.rs:181-190).
func (v *networkView) row(ap apSnapshot) *widget.Item {
	ssid := ap.ssid
	row := widget.NewItem(v.font, ap.ssid, rowSecurityLabel(ap))
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.AddClass("network-ap")
	if ap.stale {
		row.AddClass("stale")
	}
	row.SetOnActivate(func() { v.selectNetwork(ssid) })
	signal := widget.NewThemeIcon(signalStrengthIcon(ap.strength), itemIconPx)
	signal.AddClass("network-item-signal")
	row.SetMedia(signal, false)

	if ap.known {
		forgetBtn := v.ghostText(i18n.T("dropdown-network-forget"), "network-item-forget", func() { v.forget(ssid) })
		// The forget button shows while the row is hovered; the empty
		// page keeps its width so hovering never shifts the row.
		trailing := widget.NewStack()
		trailing.AddClass("network-item-trailing")
		trailing.SetTransition(widget.StackCrossfade, hoverTransition)
		trailing.Add("idle", widget.NewBox(widget.Row, 0, 0))
		trailing.Add("actions", forgetBtn)
		trailing.Show("idle")
		row.SetOnHoverWithin(func(on bool) {
			if on {
				trailing.Show("actions")
			} else {
				trailing.Show("idle")
			}
		})
		row.AddAction(trailing)
	}
	return row
}

// find is the cached entry for ssid.
func (v *networkView) find(ssid string) (apSnapshot, bool) {
	for _, ap := range v.cache {
		if ap.ssid == ssid {
			return ap, true
		}
	}
	return apSnapshot{}, false
}

// selectNetwork is select_network: a stale row scans for its network
// first; a secured unknown one asks for its password; anything else
// connects.
func (v *networkView) selectNetwork(ssid string) {
	ap, ok := v.find(ssid)
	if !ok {
		return
	}
	v.pendingSSID = ""
	if ap.stale {
		v.pendingSSID = ssid
		v.progress = netProgress{ssid: ssid, step: i18n.T("dropdown-network-step-searching")}
		v.active.apply(v.cur.snap, v.progress)
		if v.state != netScanning {
			v.startScan()
		}
		return
	}
	v.selection = &ap
	if ap.security.RequiresPassword() && !ap.known {
		v.state = netPasswordEntry
		v.password.show(ap, "")
		v.rebuildList()
		return
	}
	v.connect("")
}

// connect is connect_to_selected: the watch runs off the loop and
// reports each step and the outcome back.
func (v *networkView) connect(password string) {
	w, sel := v.wifi(), v.selection
	if w == nil || sel == nil {
		return
	}
	v.state = netConnecting
	v.progress = netProgress{ssid: sel.ssid}
	v.active.apply(v.cur.snap, v.progress)
	v.rebuildList()
	path := sel.path
	go func() {
		err := w.ConnectAndWait(context.Background(), path, password, func(state network.DeviceState) {
			if step := connectionStep(state); step != "" {
				v.ctx.Invoke(func() {
					v.progress.step = step
					v.active.apply(v.cur.snap, v.progress)
				})
			}
		})
		v.ctx.Invoke(func() { v.connectDone(err) })
	}()
}

// connectDone is the connection watcher's verdict.
func (v *networkView) connectDone(err error) {
	var ce *network.ConnectError
	switch {
	case err == nil:
		v.state, v.selection = netNormal, nil
		v.progress = netProgress{}
	case errors.As(err, &ce) && ce.Failure == network.FailureAuth && v.selection != nil:
		// Wrong password: ask again, saying so.
		v.state = netPasswordEntry
		v.progress = netProgress{}
		v.password.show(*v.selection, i18n.T("dropdown-network-error-wrong-password"))
	case errors.As(err, &ce) && ce.Failure == network.FailureSuperseded:
		v.state, v.selection = netNormal, nil
		v.progress = netProgress{}
	case errors.As(err, &ce):
		v.fail(connectFailureMessage(ce.Failure))
	default:
		log.Printf("network: connect: %v", err)
		v.fail(err.Error())
	}
	v.active.apply(v.cur.snap, v.progress)
	v.rebuildList()
	v.refresh()
}

// fail is handle_connection_failure.
func (v *networkView) fail(message string) {
	v.state, v.selection = netNormal, nil
	v.progress = netProgress{err: message}
}

// dropdownOpened is NetworkDropdownMsg::Opened: every open of the
// cached instance scans an empty list.
func (v *networkView) dropdownOpened() { v.scanIfEmpty() }

// scanIfEmpty is scan_if_empty: an empty list is never the first thing
// seen.
func (v *networkView) scanIfEmpty() {
	if v.wifi() != nil && v.cur.snap.WifiEnabled && len(v.cache) == 0 && v.state == netNormal {
		v.startScan()
	}
}

// startScan is start_scan: the scan runs off the loop until NM bumps
// LastScan or SCAN_TIMEOUT passes.
func (v *networkView) startScan() {
	w := v.wifi()
	if w == nil || v.state == netScanning {
		return
	}
	v.state = netScanning
	v.apply(v.cur)
	go func() {
		w.ScanAndWait(v.life, netScanTimeout)
		v.reads.requestThen(v.scanDone)
	}()
}

// scanDone is ScanComplete, after the read that followed the scan was
// applied: a pending stale click connects if its network came back,
// else reports it not found.
func (v *networkView) scanDone() {
	if v.state == netScanning {
		v.state = netNormal
	}
	v.apply(v.cur)
	if v.pendingSSID == "" {
		return
	}
	ssid := v.pendingSSID
	v.pendingSSID = ""
	if ap, ok := v.find(ssid); ok && !ap.stale {
		v.progress = netProgress{}
		v.selectNetwork(ssid)
		return
	}
	v.fail(i18n.T("dropdown-network-error-not-found"))
	v.active.apply(v.cur.snap, v.progress)
}

// forget is forget_network.
func (v *networkView) forget(ssid string) {
	w := v.wifi()
	if w == nil {
		return
	}
	go func() {
		w.Forget(context.Background(), ssid)
		v.refresh()
	}()
}

// refresh re-reads off the loop and applies, through the view's reader.
func (v *networkView) refresh() { v.reads.request() }

// wifiToggled is toggle_wifi.
func (v *networkView) wifiToggled(on bool) {
	w := v.wifi()
	if v.syncing || w == nil {
		return
	}
	if !on {
		v.cache, v.pendingSSID, v.selection = nil, "", nil
		v.state = netNormal
	}
	go func() {
		if err := w.SetEnabled(context.Background(), on); err != nil {
			log.Printf("network: wifi toggle: %v", err)
		}
		v.refresh()
	}()
}

// follow re-reads on NetworkManager, saved-profile, and agent changes
// until the dropdown closes.
func (v *networkView) follow() {
	if v.ctx.Network != nil {
		followInto(v.life, "network", v.ctx.Network.Subscribe, v.reads)
	}
	for _, feed := range v.changes {
		ch := feed(v.life)
		go func() {
			for range ch {
				v.reads.request()
			}
		}()
	}
}

// dropdownClosed implements dropdownCloser.
func (v *networkView) dropdownClosed() { v.once.Do(v.cancel) }

// netActive is ActiveConnections: a section of the wired and the wifi
// rows, whose hover swaps the wifi status for the disconnect/forget (or
// dismiss) actions.
type netActive struct {
	*widget.Box
	v                  *networkView
	label              *widget.Label
	wired              *widget.Item
	wifi               *widget.Item
	wifiIcon           *widget.Icon
	wifiStatus         *widget.Label
	badge              *widget.Badge
	trailing           *widget.Stack
	hovered            bool
	connected, wifiErr bool
	ssid               string
}

func newNetActive(v *networkView) *netActive {
	a := &netActive{v: v}
	a.label = sectionLabel(v.font, "")
	card, group := itemCard()
	card.AddClass("network-connections-group")
	a.Box = dropdownSection(a.label, card)

	a.wired = widget.NewItem(v.font, i18n.T("dropdown-network-ethernet"), "")
	a.wired.SetSmall(true)
	a.wired.SetEllipsize(true)
	a.wired.AddClass("network-active", "ethernet")
	a.wired.SetMedia(widget.NewThemeIcon("ld-ethernet-port-symbolic", itemIconPx), false)
	a.wired.AddAction(v.statusLabel())
	group.Append(a.wired)

	a.wifi = widget.NewItem(v.font, "", "")
	a.wifi.SetSmall(true)
	a.wifi.SetEllipsize(true)
	a.wifi.AddClass("network-active", "wifi")
	a.wifiIcon = widget.NewThemeIcon(signalStrengthIcon(0), itemIconPx)
	a.wifi.SetMedia(a.wifiIcon, false)

	a.trailing = widget.NewStack()
	a.trailing.SetTransition(widget.StackCrossfade, hoverTransition)
	status := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
	a.wifiStatus = v.statusLabel()
	status.AppendAligned(a.wifiStatus, false, widget.AlignCenter)
	a.badge = widget.NewBadge(v.font, "")
	a.badge.SetVariant(widget.BadgeSecondary)
	a.badge.AddClass("network-connection-status")
	status.AppendAligned(a.badge, false, widget.AlignCenter)
	a.trailing.Add("status", status)
	actions := widget.NewBox(widget.Row, widget.Current().Spacing/2, 0)
	actions.Append(v.ghostText(i18n.T("dropdown-network-disconnect"), "network-action-disconnect", a.disconnect), false)
	actions.Append(v.ghostText(i18n.T("dropdown-network-forget"), "network-action-forget", a.forget), false)
	a.trailing.Add("actions", actions)
	a.trailing.Add("error-actions", v.ghostText(i18n.T("dropdown-network-dismiss"), "network-action-dismiss", a.dismiss))
	a.wifi.AddAction(a.trailing)
	a.wifi.SetOnHoverWithin(func(on bool) {
		a.hovered = on
		a.syncTrailing()
	})
	group.Append(a.wifi)
	return a
}

func (v *networkView) statusLabel() *widget.Label {
	l := widget.NewLabel(v.font, v.px, i18n.T("dropdown-network-connected"), 0)
	l.AddClass("network-connection-status", css.Caption, css.DimLabel)
	return l
}

// ghostText is a small ghost text button: a row's or a form's quiet
// action.
func (v *networkView) ghostText(text, class string, onClick func()) *widget.Button {
	return dropdownButton(v.buttonLabel(text), onClick, class, css.Ghost, css.Small)
}

// outlineText is a form's dismissing action, lys-ui's outline button.
func (v *networkView) outlineText(text, class string, onClick func()) *widget.Button {
	return dropdownButton(v.buttonLabel(text), onClick, class, css.Outline)
}

// primaryText is a PrimaryButton: the accent-filled action of a form.
func (v *networkView) primaryText(text, class string, onClick func()) *widget.Button {
	return dropdownButton(v.buttonLabel(text), onClick, class, css.SuggestedAction)
}

func (v *networkView) buttonLabel(text string) *widget.Label {
	return widget.NewLabel(v.font, v.px, text, 0)
}

// errorLabel is a form's error line: wrapped, in lys-ui's error color,
// hidden until there is something to say.
func (v *networkView) errorLabel() *widget.Label {
	l := widget.NewLabel(v.font, v.px, "", 0)
	l.AddClass(css.Error)
	l.SetWrap(true)
	l.SetVisible(false)
	return l
}

// apply is the cards' #[watch]es over the snapshot and the progress.
func (a *netActive) apply(s network.Snapshot, p netProgress) {
	connecting := p.ssid != "" || s.Wifi == network.StatusConnecting
	a.connected = s.Wifi == network.StatusConnected
	a.wifiErr = p.err != "" && s.Wifi != network.StatusConnected
	a.ssid = s.WifiSSID
	a.SetVisible(s.Wifi == network.StatusConnected || s.Wired == network.StatusConnected || connecting || p.err != "")
	if s.Wired == network.StatusConnected && s.Wifi == network.StatusConnected {
		a.label.SetText(i18n.T("dropdown-network-active-connections"))
	} else {
		a.label.SetText(i18n.T("dropdown-network-active-connection"))
	}

	a.wired.SetVisible(s.Wired == network.StatusConnected)
	speed := ""
	if s.WiredSpeed > 0 {
		speed = formatWiredSpeed(s.WiredSpeed)
		if s.WiredIP4 != "" {
			speed = s.WiredIP4 + " - " + speed
		}
	}
	a.wired.SetDescription(speed)

	a.wifi.SetVisible(s.Wifi == network.StatusConnected || connecting || p.err != "")
	if a.wifiErr {
		// The row swaps wifi for error, it never wears both
		// (active_connections/methods.rs:100-110).
		a.wifiIcon.SetThemeName("ld-wifi-off-symbolic")
		a.wifi.RemoveClass("wifi")
		a.wifi.AddClass("error")
	} else {
		a.wifiIcon.SetThemeName(signalStrengthIcon(s.WifiStrength))
		a.wifi.RemoveClass("error")
		a.wifi.AddClass("wifi")
	}
	switch {
	case s.WifiSSID != "":
		a.wifi.SetTitle(s.WifiSSID)
	case p.ssid != "":
		a.wifi.SetTitle(p.ssid)
	default:
		a.wifi.SetTitle(i18n.T("dropdown-network-wifi"))
	}
	detail := ""
	switch {
	case p.err != "":
		detail = p.err
	case p.step != "":
		detail = p.step
	default:
		band := frequencyBand(s.WifiFrequency)
		switch {
		case s.WifiIP4 != "" && band != "":
			detail = s.WifiIP4 + " - " + band
		case s.WifiIP4 != "":
			detail = s.WifiIP4
		default:
			detail = band
		}
	}
	a.wifi.SetDescription(detail)
	a.wifi.SetTooltip(p.err)

	a.wifiStatus.SetVisible(s.Wifi == network.StatusConnected && !connecting && p.err == "")
	a.badge.RemoveClass("error", "warning")
	switch {
	case p.err != "":
		a.badge.SetText(i18n.T("dropdown-network-error"))
		a.badge.AddClass("error")
	case connecting:
		a.badge.SetText(i18n.T("dropdown-network-connecting"))
		a.badge.AddClass("warning")
	}
	a.badge.SetVisible(connecting || p.err != "")
	a.syncTrailing()
}

// syncTrailing picks the hover stack's page.
func (a *netActive) syncTrailing() {
	switch {
	case a.hovered && a.connected:
		a.trailing.Show("actions")
	case a.hovered && a.wifiErr:
		a.trailing.Show("error-actions")
	default:
		a.trailing.Show("status")
	}
}

func (a *netActive) disconnect() {
	w := a.v.wifi()
	if w == nil {
		return
	}
	go func() {
		if err := w.Disconnect(context.Background()); err != nil {
			log.Printf("network: wifi disconnect: %v", err)
		}
	}()
}

// forget is forget_wifi: the saved profiles go, then the link.
func (a *netActive) forget() {
	w, ssid := a.v.wifi(), a.ssid
	if w == nil || ssid == "" {
		return
	}
	go func() {
		w.Forget(context.Background(), ssid)
		if err := w.Disconnect(context.Background()); err != nil {
			log.Printf("network: wifi disconnect after forget: %v", err)
		}
	}()
}

// dismiss is DismissError.
func (a *netActive) dismiss() {
	a.v.progress.err = ""
	a.apply(a.v.cur.snap, a.v.progress)
}

// netPasswordForm is PasswordForm: the network as the form card's
// subject, the masked password box, the error from a refused attempt,
// Cancel and Connect.
type netPasswordForm struct {
	*formCard
	v        *networkView
	secret   *secretEntry
	errLabel *widget.Label
}

func newNetPasswordForm(v *networkView) *netPasswordForm {
	f := &netPasswordForm{v: v}
	f.formCard = newFormCard(v.font, v.px, signalStrengthIcon(0), f.cancel, "network-password-card")
	f.secret = newSecretEntry(v.font, v.px)
	f.secret.entry.AddClass("network-password-input")
	f.secret.entry.SetPlaceholder(i18n.T("dropdown-network-password-placeholder"))
	f.secret.entry.OnActivate = func(string) { f.connect() }
	f.body.Append(f.secret.entry, false)
	f.errLabel = v.errorLabel()
	f.body.Append(f.errLabel, false)
	f.body.Append(formActions(
		v.outlineText(i18n.T("dropdown-network-cancel"), "network-password-cancel", f.cancel),
		v.primaryText(i18n.T("dropdown-network-connect"), "network-password-connect", f.connect),
	), false)
	f.SetVisible(false)
	return f
}

// show is PasswordFormInput::Show: the box takes the keyboard at once.
func (f *netPasswordForm) show(ap apSnapshot, errMessage string) {
	f.icon.SetThemeName(signalStrengthIcon(ap.strength))
	f.head.SetTitle(ap.ssid)
	f.head.SetDescription(securityLabel(ap.security))
	f.secret.reset()
	f.errLabel.SetText(errMessage)
	f.errLabel.SetVisible(errMessage != "")
	f.SetVisible(true)
	f.v.focus(f.secret.entry)
}

func (f *netPasswordForm) connect() {
	password := f.secret.entry.Text()
	f.secret.reset()
	f.v.connect(password)
}

func (f *netPasswordForm) cancel() {
	f.secret.reset()
	f.v.state, f.v.selection = netNormal, nil
	f.v.rebuildList()
}

// netSecretForm is SecretForm: NetworkManager's pending credential
// prompt (a VPN password, a 2FA code), answered through the agent: the
// connection as the form card's subject, the challenge, and a field per
// asked secret.
type netSecretForm struct {
	*formCard
	v       *networkView
	message *widget.Label
	fields  *widget.Box
	entries []netSecretField
	showing string
}

type netSecretField struct {
	key   string
	entry *widget.Entry
}

func newNetSecretForm(v *networkView) *netSecretForm {
	f := &netSecretForm{v: v}
	f.formCard = newFormCard(v.font, v.px, "ld-lock-symbolic", f.cancel, "network-password-card", "network-secret-card")
	f.head.SetDescription(i18n.T("dropdown-network-secret-title"))
	f.message = widget.NewLabel(v.font, v.px, "", 0)
	f.message.AddClass("network-secret-message", css.DimLabel)
	f.message.SetWrap(true)
	// The challenge must not drive the popover's width
	// (secret_form/mod.rs:118-128).
	f.message.SetMaxWidthChars(32)
	f.body.Append(f.message, false)
	f.fields = widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	f.body.Append(f.fields, false)
	f.body.Append(formActions(
		v.outlineText(i18n.T("dropdown-network-cancel"), "network-password-cancel", f.cancel),
		v.primaryText(i18n.T("dropdown-network-secret-submit"), "network-password-connect", f.submit),
	), false)
	f.SetVisible(false)
	return f
}

// secretFieldLabel is label_for: the dropdown's own wording for the
// common keys, else the service's label.
func secretFieldLabel(field secrets.Field) string {
	switch field.Key {
	case "password", "passwd", "psk", "leap-password", "secret":
		return i18n.T("dropdown-network-secret-password")
	case "user", "username", "user-name":
		return i18n.T("dropdown-network-secret-username")
	case "pin":
		return i18n.T("dropdown-network-secret-pin")
	case "usergroup":
		return i18n.T("dropdown-network-secret-group")
	case "domain":
		return i18n.T("dropdown-network-secret-domain")
	case "wep-key0", "wep-key1", "wep-key2", "wep-key3":
		return i18n.T("dropdown-network-secret-wep-key")
	case "private-key":
		return i18n.T("dropdown-network-secret-private-key")
	case "private-key-password":
		return i18n.T("dropdown-network-secret-private-key-password")
	}
	return field.Label
}

// apply mirrors the agent's pending prompt: a new request rebuilds the
// fields and takes the keyboard; none hides the form.
func (f *netSecretForm) apply(req secrets.Request, asking bool) {
	if !asking {
		f.showing = ""
		f.entries = nil
		f.fields.Clear()
		f.SetVisible(false)
		return
	}
	key := req.UUID + "\x00" + req.Setting + "\x00" + req.Message
	if key == f.showing {
		return
	}
	f.showing = key
	f.head.SetTitle(req.Name)
	f.message.SetText(req.Message)
	f.message.SetVisible(req.Message != "")
	f.fields.Clear()
	f.entries = nil
	for _, field := range req.Fields {
		var entry *widget.Entry
		if field.Secret {
			entry = newSecretEntry(f.v.font, f.v.px).entry
		} else {
			entry = widget.NewEntry(f.v.font, f.v.px, 0)
		}
		entry.AddClass("network-password-input")
		f.fields.Append(widget.NewField(f.v.font, secretFieldLabel(field), entry), false)
		entry.OnActivate = func(string) { f.submit() }
		f.entries = append(f.entries, netSecretField{key: field.Key, entry: entry})
	}
	f.SetVisible(true)
	if len(f.entries) > 0 {
		f.v.focus(f.entries[0].entry)
	}
}

func (f *netSecretForm) submit() {
	agent := f.agent()
	if agent == nil {
		return
	}
	values := make(map[string]string, len(f.entries))
	for _, e := range f.entries {
		values[e.key] = e.entry.Text()
	}
	agent.Submit(values)
	f.apply(secrets.Request{}, false)
}

func (f *netSecretForm) cancel() {
	if agent := f.agent(); agent != nil {
		agent.Cancel()
	}
	f.apply(secrets.Request{}, false)
}

func (f *netSecretForm) agent() secretAgent { return f.v.agent }
