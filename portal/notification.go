package portal

import (
	"context"
	"errors"
	"fmt"
	"html"
	"image"
	"image/draw"
	"io"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/stubbedev/lys-desktop/imagedecode"
	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	xdraw "golang.org/x/image/draw"

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
	sounds    *soundCache
}

func newNotifier(conn *dbus.Conn) *notifier {
	return &notifier{conn: conn, byDaemon: map[uint32]shown{}, byKey: map[portalKey]uint32{}, asks: map[uint32]chan string{}, tokens: map[uint32]string{}, sounds: newSoundCache()}
}

func (n *notifier) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    NotificationIface,
		Methods: notificationObject{n},
		Properties: dbusx.Getters{
			"version":          func() any { return uint32(2) },
			"SupportedOptions": func() any { return n.supportedOptions() },
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
	watch, err := dbusx.WatchSignals(context.Background(), n.conn, dbusx.FromOwner(notifications.Interface),
		func(err error) { warnf("notification: %v", err) },
		dbusx.On(notifications.ActivationToken, notifications.ObjPath, n.activationToken),
		dbusx.On(notifications.ActionInvoked, notifications.ObjPath, n.actionInvoked),
		dbusx.On(notifications.NotificationReplied, notifications.ObjPath, n.replied),
		dbusx.On(notifications.NotificationClosed, notifications.ObjPath, n.closed))
	if err != nil {
		return nil, err
	}
	n.listening.Store(true)
	return func() {
		n.listening.Store(false)
		if err := watch.Stop(); err != nil && !errors.Is(err, dbusx.ErrDisconnected) {
			warnf("notification: %v", err)
		}
	}, nil
}

func (n *notifier) activationToken(grant notifications.TokenGrant, _ dbus.Sequence) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.byDaemon[grant.ID]; ok && grant.Token != "" {
		n.tokens[grant.ID] = grant.Token
	}
	return nil
}

func (n *notifier) actionInvoked(invoked notifications.ActionInvocation, _ dbus.Sequence) error {
	id, key := invoked.ID, invoked.Key
	n.mu.Lock()
	token := n.tokens[id]
	delete(n.tokens, id)
	n.mu.Unlock()
	if n.answer(id, key) {
		return nil
	}
	n.mu.Lock()
	sh, ok := n.byDaemon[id]
	action, known := sh.actions[key]
	n.mu.Unlock()
	if !ok || !known {
		return nil
	}
	parameter := []dbus.Variant{}
	if action.target != nil {
		parameter = append(parameter, *action.target)
	}
	if token != "" {
		parameter = append(parameter, dbus.MakeVariant(Vardict{"activation-token": dbus.MakeVariant(token)}))
	}
	_ = n.conn.Emit(ObjectPath, NotificationIface+".ActionInvoked", sh.key.appID, sh.key.id, action.name, parameter)
	return nil
}

func (n *notifier) replied(reply notifications.InlineReply, _ dbus.Sequence) error {
	n.mu.Lock()
	sh, ok := n.byDaemon[reply.ID]
	action, known := sh.actions[notifications.InlineReplyActionID]
	n.mu.Unlock()
	if ok && known {
		_ = n.conn.Emit(ObjectPath, NotificationIface+".ActionInvoked", sh.key.appID, sh.key.id, action.name, []dbus.Variant{dbus.MakeVariant(reply.Text)})
	}
	return nil
}

func (n *notifier) closed(closure notifications.Closure, _ dbus.Sequence) error {
	n.answer(closure.ID, "")
	n.forget(closure.ID)
	return nil
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
	buttons := notificationActions(notification)
	hints := o.n.notificationHints(appID, notification, buttons)
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
	if slices.Contains(stringList(notification, "display-hint"), "persistent") || ringing(notification) {
		expire = 0
	}
	var daemonID uint32
	err := o.n.daemon().CallWithContext(context.Background(), notifications.Interface+".Notify", 0,
		appID, replaces, notificationIcon(notification), stringOr(notification, "title", appID),
		notificationBody(notification), buttons.labels, hints, expire,
	).Store(&daemonID)
	if err != nil {
		return dbusx.Failed(fmt.Sprintf("show the notification: %v", err))
	}
	o.n.mu.Lock()
	if replaces != 0 && replaces != daemonID {
		delete(o.n.byDaemon, replaces)
	}
	o.n.byDaemon[daemonID] = shown{key: key, actions: buttons.actions}
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

// notificationButtons is a portal notification's actions as the daemon
// takes them: what each key stands for, the [key, label, ...] list, the
// purposes of the keys that have one, and the reply field's placeholder.
type notificationButtons struct {
	actions     map[string]portalAction
	labels      []string
	purposes    map[string]string
	placeholder string
}

// notificationActions maps the default action to the daemon's body-click
// key "default", an im.reply-with-text button to its inline-reply action
// (the label becoming the field's placeholder), and each other labelled
// button to a key of its own, its purpose kept. A button without a label
// (a purpose-only one, such as system.custom-alert) is not shown.
func notificationActions(notification Vardict) notificationButtons {
	actions := map[string]portalAction{}
	labels := []string{}
	purposes := map[string]string{}
	placeholder := ""
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
		purpose, _ := optString(b, "purpose")
		if purpose == replyPurpose {
			key, placeholder = notifications.InlineReplyActionID, label
		} else if purpose != "" {
			purposes[key] = purpose
		}
		actions[key] = portalAction{name: action, target: optVariant(b, "target")}
		labels = append(labels, key, label)
	}
	return notificationButtons{actions: actions, labels: labels, purposes: purposes, placeholder: placeholder}
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
func (n *notifier) notificationHints(appID string, notification Vardict, buttons notificationButtons) map[string]dbus.Variant {
	hints := map[string]dbus.Variant{}
	if buttons.placeholder != "" {
		hints["x-kde-reply-placeholder-text"] = dbus.MakeVariant(buttons.placeholder)
	}
	if len(buttons.purposes) > 0 {
		hints[notifications.ActionPurposesHint] = dbus.MakeVariant(buttons.purposes)
	}
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
	if slices.Contains(display, "persistent") || ringing(notification) {
		hints["resident"] = dbus.MakeVariant(true)
	}
	if ringing(notification) {
		hints["urgency"] = dbus.MakeVariant(byte(2))
	}
	n.soundHints(appID, notification, hints)
	if img, err := iconImage(notification); err != nil {
		warnf("notification: icon for %s: %v", appID, err)
	} else if img != nil {
		if data, err := imageDataOf(img); err != nil {
			warnf("notification: icon for %s: %v", appID, err)
		} else {
			hints["image-data"] = dbus.MakeVariant(data)
		}
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
		f, err := dbusx.File(fd, "icon")
		if err != nil {
			return nil, err
		}
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

func imageDataOf(img image.Image) (imageData, error) {
	b := img.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), img, b.Min, draw.Src)
	width, errW := intconv.Exact[int32](b.Dx())
	height, errH := intconv.Exact[int32](b.Dy())
	stride, errS := intconv.Exact[int32](nrgba.Stride)
	if err := errors.Join(errW, errH, errS); err != nil {
		return imageData{}, fmt.Errorf("a %dx%d icon: %w", b.Dx(), b.Dy(), err)
	}
	return imageData{
		Width: width, Height: height, Rowstride: stride,
		HasAlpha: true, BitsPerSample: 8, Channels: 4, Data: nrgba.Pix,
	}, nil
}
