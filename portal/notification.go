package portal

import (
	"context"
	"errors"
	"fmt"
	"html"
	"image"
	"image/draw"
	"io"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-ui/render"
	xdraw "golang.org/x/image/draw"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/imagedecode"
	"github.com/stubbedev/lys-shell/service/notifications"
)

// NotificationIface turns portal notifications into
// org.freedesktop.Notifications calls the shell's daemon shows, and
// forwards the daemon's ActionInvoked back as the portal's. A press
// reaches the app as the action it named, with its target, then the
// platform-data holding the activation-token the daemon announced for
// the press (its ActivationToken), so the app can raise its window.
const NotificationIface = "org.freedesktop.impl.portal.Notification"

// maxIconBytes bounds an icon read from a file descriptor or bytes.
const maxIconBytes = 4 << 20

// maxIconSize is the largest icon edge handed to the daemon; bigger
// images are scaled down.
const maxIconSize = 256

// svgIconSize is the edge an SVG icon is rasterized at.
const svgIconSize = 128

// portalKey names one app notification.
type portalKey struct{ appID, id string }

// portalAction is an app action a daemon action key stands for.
type portalAction struct {
	name   string
	target *dbus.Variant
}

// shown is one daemon notification this portal created: whose it is
// and what its action keys stand for.
type shown struct {
	key     portalKey
	actions map[string]portalAction
}

// notifier is the interface's state: the daemon notifications it
// created, both ways, and the ones it shows to ask the user something,
// each waiting for its answer.
type notifier struct {
	conn     *dbus.Conn
	mu       sync.Mutex
	byDaemon map[uint32]shown
	byKey    map[portalKey]uint32
	asks     map[uint32]chan string
	// tokens are the activation tokens the daemon announced, by its
	// notification id, for the ActionInvoked that follows.
	tokens map[uint32]string
	// listening is whether the daemon's signals reach handle; without
	// them no ask could be answered.
	listening atomic.Bool
}

func newNotifier(conn *dbus.Conn) *notifier {
	return &notifier{conn: conn, byDaemon: map[uint32]shown{}, byKey: map[portalKey]uint32{}, asks: map[uint32]chan string{}, tokens: map[uint32]string{}}
}

func (n *notifier) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    NotificationIface,
		Methods: notificationObject{n},
		Properties: dbusx.Getters{
			"version": func() any { return uint32(2) },
			// The daemon treats no category or button purpose specially.
			"SupportedOptions": func() any {
				return Vardict{"category": dbus.MakeVariant([]string{}), "button-purpose": dbus.MakeVariant([]string{})}
			},
		},
	}
}

func (n *notifier) daemon() dbus.BusObject {
	return n.conn.Object(notifications.Interface, notifications.ObjPath)
}

// forget drops a daemon notification from both maps.
func (n *notifier) forget(daemonID uint32) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, ok := n.byDaemon[daemonID]
	if !ok {
		return
	}
	delete(n.byDaemon, daemonID)
	delete(n.tokens, daemonID)
	if n.byKey[s.key] == daemonID {
		delete(n.byKey, s.key)
	}
}

// forward re-emits the daemon's ActionInvoked for the notifications
// this portal created, and forgets them when the daemon closes them;
// the returned func stops it.
func (n *notifier) forward() (stop func(), err error) {
	match := []dbus.MatchOption{dbus.WithMatchSender(notifications.Interface), dbus.WithMatchInterface(notifications.Interface)}
	if err := n.conn.AddMatchSignal(match...); err != nil {
		return nil, err
	}
	signals := make(chan *dbus.Signal, 16)
	n.conn.Signal(signals)
	n.listening.Store(true)
	// godbus closes the channel itself when the connection goes, so the
	// loop ends on quit or that, and stop never closes it.
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			var s *dbus.Signal
			select {
			case <-quit:
				return
			case s = <-signals:
			}
			if s == nil {
				return
			}
			n.handle(s)
		}
	}()
	return func() {
		n.listening.Store(false)
		_ = n.conn.RemoveMatchSignal(match...)
		n.conn.RemoveSignal(signals)
		close(quit)
		<-done
	}, nil
}

func (n *notifier) handle(s *dbus.Signal) {
	switch s.Name {
	case notifications.Interface + ".ActivationToken":
		if len(s.Body) != 2 {
			return
		}
		id, _ := s.Body[0].(uint32)
		token, _ := s.Body[1].(string)
		n.mu.Lock()
		if _, ok := n.byDaemon[id]; ok && token != "" {
			n.tokens[id] = token
		}
		n.mu.Unlock()
	case notifications.Interface + ".ActionInvoked":
		if len(s.Body) != 2 {
			return
		}
		id, _ := s.Body[0].(uint32)
		key, _ := s.Body[1].(string)
		n.mu.Lock()
		token := n.tokens[id]
		delete(n.tokens, id)
		n.mu.Unlock()
		if n.answer(id, key) {
			return
		}
		n.mu.Lock()
		sh, ok := n.byDaemon[id]
		action, known := sh.actions[key]
		n.mu.Unlock()
		if !ok || !known {
			return
		}
		parameter := []dbus.Variant{}
		if action.target != nil {
			parameter = append(parameter, *action.target)
		}
		if token != "" {
			parameter = append(parameter, dbus.MakeVariant(Vardict{"activation-token": dbus.MakeVariant(token)}))
		}
		_ = n.conn.Emit(ObjectPath, NotificationIface+".ActionInvoked", sh.key.appID, sh.key.id, action.name, parameter)
	case notifications.Interface + ".NotificationClosed":
		if len(s.Body) != 2 {
			return
		}
		if id, ok := s.Body[0].(uint32); ok {
			n.answer(id, "")
			n.forget(id)
		}
	}
}

// answer hands key to the ask waiting on daemon notification id;
// false when no ask is.
func (n *notifier) answer(id uint32, key string) bool {
	n.mu.Lock()
	ch, ok := n.asks[id]
	delete(n.asks, id)
	n.mu.Unlock()
	if ok {
		ch <- key
	}
	return ok
}

// ask shows a persistent notification for appID offering actions
// ([key, label, ...]) and waits for the user: the key of the action
// they pick, "" when the notification closes unanswered. When ctx ends
// first the notification is withdrawn and ctx's error returned.
func (n *notifier) ask(ctx context.Context, appID, title, body string, actions []string) (string, error) {
	if !n.listening.Load() {
		return "", errors.New("not subscribed to the notification daemon's signals")
	}
	answer := make(chan string, 1)
	notifyCtx, cancel := context.WithTimeout(ctx, shellCallTimeout)
	defer cancel()
	hints := map[string]dbus.Variant{"desktop-entry": dbus.MakeVariant(appID)}
	var id uint32
	// The lock spans the call so the answer cannot arrive before the
	// ask is registered under its id.
	n.mu.Lock()
	err := n.daemon().CallWithContext(notifyCtx, notifications.Interface+".Notify", 0,
		appID, uint32(0), "", title, body, actions, hints, int32(0)).Store(&id)
	if err == nil {
		n.asks[id] = answer
	}
	n.mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("show the notification: %w", err)
	}
	select {
	case key := <-answer:
		return key, nil
	case <-ctx.Done():
	}
	n.mu.Lock()
	delete(n.asks, id)
	n.mu.Unlock()
	closeCtx, cancelClose := context.WithTimeout(context.Background(), shellCallTimeout)
	defer cancelClose()
	if err := n.daemon().CallWithContext(closeCtx, notifications.Interface+".CloseNotification", 0, id).Err; err != nil {
		warnf("notification: withdrawing %d: %v", id, err)
	}
	return "", ctx.Err()
}

// notificationObject carries the interface's D-Bus methods.
type notificationObject struct{ n *notifier }

// AddNotification shows a notification, or updates the one the app
// showed under the same id (replacing it outright with show-as-new).
// A daemon that refuses it fails the call.
func (o notificationObject) AddNotification(appID, id string, notification Vardict) *dbus.Error {
	key := portalKey{appID, id}
	hints := notificationHints(appID, notification)
	actions, labels := notificationActions(notification)
	o.n.mu.Lock()
	replaces := o.n.byKey[key]
	o.n.mu.Unlock()
	if replaces != 0 && slices.Contains(stringList(notification, "display-hint"), "show-as-new") {
		if err := o.n.daemon().Call(notifications.Interface+".CloseNotification", 0, replaces).Err; err != nil {
			warnf("notification: closing %d for show-as-new: %v", replaces, err)
		}
		o.n.forget(replaces)
		replaces = 0
	}
	expire := int32(-1)
	if slices.Contains(stringList(notification, "display-hint"), "persistent") {
		expire = 0
	}
	var daemonID uint32
	err := o.n.daemon().CallWithContext(context.Background(), notifications.Interface+".Notify", 0,
		appID, replaces, notificationIcon(notification), stringOr(notification, "title", appID),
		notificationBody(notification), labels, hints, expire,
	).Store(&daemonID)
	if err != nil {
		return dbusx.Failed(fmt.Sprintf("show the notification: %v", err))
	}
	o.n.mu.Lock()
	if replaces != 0 && replaces != daemonID {
		delete(o.n.byDaemon, replaces)
	}
	o.n.byDaemon[daemonID] = shown{key: key, actions: actions}
	o.n.byKey[key] = daemonID
	o.n.mu.Unlock()
	return nil
}

// RemoveNotification closes a notification this portal showed.
func (o notificationObject) RemoveNotification(appID, id string) *dbus.Error {
	o.n.mu.Lock()
	daemonID, found := o.n.byKey[portalKey{appID, id}]
	o.n.mu.Unlock()
	if !found {
		return nil
	}
	o.n.forget(daemonID)
	if err := o.n.daemon().Call(notifications.Interface+".CloseNotification", 0, daemonID).Err; err != nil {
		warnf("notification: CloseNotification failed: %v", err)
	}
	return nil
}

// notificationActions maps the default action to the daemon's body-click
// key "default" and each labelled button to a key of its own; it returns
// what each key stands for and the freedesktop [key, label, ...] list.
// A button without a label (a purpose-only one, such as
// system.custom-alert) is not shown.
func notificationActions(notification Vardict) (map[string]portalAction, []string) {
	actions := map[string]portalAction{}
	labels := []string{}
	if def, ok := optString(notification, "default-action"); ok && def != "" {
		actions[notifications.DefaultActionID] = portalAction{name: def, target: optVariant(notification, "default-action-target")}
		labels = append(labels, notifications.DefaultActionID, "")
	}
	buttons, _ := notification["buttons"].Value().([]map[string]dbus.Variant)
	for i, b := range buttons {
		action, okA := optString(b, "action")
		label, okL := optString(b, "label")
		if !okA || !okL || action == "" || label == "" {
			continue
		}
		key := "button-" + strconv.Itoa(i)
		actions[key] = portalAction{name: action, target: optVariant(b, "target")}
		labels = append(labels, key, label)
	}
	return actions, labels
}

// optVariant is a v option, nil when absent.
func optVariant(options Vardict, key string) *dbus.Variant {
	v, ok := options[key]
	if !ok {
		return nil
	}
	if inner, ok := v.Value().(dbus.Variant); ok {
		return &inner
	}
	return &v
}

// notificationBody is markup-body (the frontend already cut it to the
// subset the daemon renders) or the plain body escaped, since the daemon
// reads every body as markup.
func notificationBody(notification Vardict) string {
	if markup, ok := optString(notification, "markup-body"); ok {
		return markup
	}
	return html.EscapeString(stringOr(notification, "body", ""))
}

// notificationHints carries the portal's options as freedesktop hints:
// the app for grouping, priority as urgency, the category, the transient
// and persistent display hints, a silent sound, and a file or bytes icon
// as image-data.
func notificationHints(appID string, notification Vardict) map[string]dbus.Variant {
	hints := map[string]dbus.Variant{}
	if appID != "" {
		hints["desktop-entry"] = dbus.MakeVariant(appID)
	}
	if priority, ok := optString(notification, "priority"); ok {
		hints["urgency"] = dbus.MakeVariant(priorityUrgency(priority))
	}
	if category, ok := optString(notification, "category"); ok && category != "" {
		hints["category"] = dbus.MakeVariant(category)
	}
	display := stringList(notification, "display-hint")
	if slices.Contains(display, "transient") {
		hints["transient"] = dbus.MakeVariant(true)
	}
	if slices.Contains(display, "persistent") {
		hints["resident"] = dbus.MakeVariant(true)
	}
	if sound, ok := notification["sound"].Value().(string); ok && sound == "silent" {
		hints["suppress-sound"] = dbus.MakeVariant(true)
	}
	if img, err := iconImage(notification); err != nil {
		warnf("notification: icon for %s: %v", appID, err)
	} else if img != nil {
		hints["image-data"] = dbus.MakeVariant(imageDataOf(img))
	}
	return hints
}

// priorityUrgency maps a portal priority onto the freedesktop urgency
// byte: low 0, normal and high 1, urgent 2 (critical).
func priorityUrgency(priority string) byte {
	switch priority {
	case "low":
		return 0
	case "urgent":
		return 2
	}
	return 1
}

// notificationIcon is a bare icon name, or the first name of a
// serialized themed icon ('themed', <['name', ...]>).
func notificationIcon(notification Vardict) string {
	if name, ok := notification["icon"].Value().(string); ok {
		return name
	}
	if kind, inner, ok := serializedIcon(notification["icon"]); ok && kind == "themed" {
		if names, ok := inner.Value().([]string); ok && len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

// iconImage decodes a ('file-descriptor', <h>) or ('bytes', <ay>) icon;
// nil without one.
func iconImage(notification Vardict) (image.Image, error) {
	kind, inner, ok := serializedIcon(notification["icon"])
	if !ok {
		return nil, nil
	}
	var data []byte
	switch kind {
	case "file-descriptor":
		fd, ok := inner.Value().(dbus.UnixFD)
		if !ok {
			return nil, errors.New("a file-descriptor icon without a descriptor")
		}
		f := os.NewFile(uintptr(fd), "icon")
		defer func() { _ = f.Close() }()
		b, err := io.ReadAll(io.LimitReader(f, maxIconBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read the icon: %w", err)
		}
		data = b
	case "bytes":
		b, ok := inner.Value().([]byte)
		if !ok {
			return nil, errors.New("a bytes icon without bytes")
		}
		data = b
	default:
		return nil, nil
	}
	if len(data) > maxIconBytes {
		return nil, fmt.Errorf("icon larger than %d bytes", maxIconBytes)
	}
	return decodeIcon(data)
}

// decodeIcon decodes PNG, JPEG or SVG bytes, scaled to fit maxIconSize.
func decodeIcon(data []byte) (image.Image, error) {
	if img, err := imagedecode.Decode(data); err == nil {
		return fitIcon(img), nil
	}
	icon, err := render.LoadSVG(data, svgIconSize, svgIconSize)
	if err != nil {
		return nil, fmt.Errorf("neither a raster image nor an SVG: %w", err)
	}
	w, h := icon.Size()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, icon.At(x, y))
		}
	}
	return img, nil
}

func fitIcon(img image.Image) image.Image {
	b := img.Bounds()
	if b.Dx() <= maxIconSize && b.Dy() <= maxIconSize {
		return img
	}
	scale := float64(maxIconSize) / float64(max(b.Dx(), b.Dy()))
	dst := image.NewNRGBA(image.Rect(0, 0, max(int(float64(b.Dx())*scale), 1), max(int(float64(b.Dy())*scale), 1)))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// imageData is the freedesktop (iiibiiay) image: straight RGBA, 8 bits.
type imageData struct {
	Width, Height, Rowstride int32
	HasAlpha                 bool
	BitsPerSample, Channels  int32
	Data                     []byte
}

func imageDataOf(img image.Image) imageData {
	b := img.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), img, b.Min, draw.Src)
	return imageData{
		Width: int32(b.Dx()), Height: int32(b.Dy()), Rowstride: int32(nrgba.Stride),
		HasAlpha: true, BitsPerSample: 8, Channels: 4, Data: nrgba.Pix,
	}
}
