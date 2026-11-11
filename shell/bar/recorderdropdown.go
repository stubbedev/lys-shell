package bar

import (
	"context"
	"log"
	"slices"

	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/recorder/devicechoice"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/sysinfo"
)

// Recorder config paths the dropdown writes.
const (
	recorderPathMic       = "modules.recorder.microphone"
	recorderPathMicDevice = "modules.recorder.microphone-device"
	recorderPathSystem    = "modules.recorder.system-audio"
	recorderPathWebcam    = "modules.recorder.webcam-enabled"
	recorderPathCamDevice = "modules.recorder.webcam-device"
	recorderPathWebcamX   = "modules.recorder.webcam-x"
	recorderPathWebcamY   = "modules.recorder.webcam-y"
	recorderPathReplay    = "modules.recorder.replay.enable"
)

// previewGeometry is preview_geometry: the 16:9 screen frame across the
// width the preview is given and the webcam frame at its configured
// size.
func previewGeometry(width int32, sizePct uint8) (pw, ph, cw, ch int32) {
	pw = width
	ph = pw * 9 / 16
	cw = max(pw*int32(sizePct)/100, 24)
	ch = max(cw*9/16, 14)
	return pw, ph, cw, ch
}

// pctFromPx is pct_from_px.
func pctFromPx(px, margin, travel int32) uint8 {
	if travel <= 0 {
		return 0
	}
	return intconv.Clamp[uint8](min(max(px-margin, 0), travel) * 100 / travel)
}

// webcamPreview is the draggable position preview: the outer frame is
// the screen, the inner one the webcam picture-in-picture, inset by the
// recording's margin. Both are classed boxes the stylesheet
// paints (recorder-position-preview, recorder-position-cam). The frame
// spans the width its row gives it at 16:9, so it never outgrows the
// card. A drag tracks the pointer from where it grabbed the frame, and
// the release reports the position as percentages.
type webcamPreview struct {
	widget.Base
	sizePct uint8
	// x and y are the frame's position as percentages of its travel.
	x, y           uint8
	pw, ph, cw, ch int32
	// camX and camY are the frame's offset inside the preview.
	camX, camY int32
	frame, cam *widget.Box

	pointer  widget.Point
	grabX    int32
	grabY    int32
	dragging bool
	onMove   func(x, y uint8)
}

func newWebcamPreview(sizePct, x, y uint8) *webcamPreview {
	p := &webcamPreview{sizePct: sizePct}
	p.frame = widget.NewBox(widget.Row, 0, 0)
	p.frame.AddClass("recorder-position-preview")
	p.cam = widget.NewBox(widget.Row, 0, 0)
	p.cam.AddClass("recorder-position-cam")
	p.place(x, y)
	return p
}

func (p *webcamPreview) travel() (margin, w, h int32) {
	margin = recorder.WebcamMargin(p.pw, p.ph)
	return margin, max(p.pw-p.cw-2*margin, 0), max(p.ph-p.ch-2*margin, 0)
}

// place is reposition_cam.
func (p *webcamPreview) place(x, y uint8) {
	p.x, p.y = min(x, 100), min(y, 100)
	p.reposition()
	p.Invalidate()
}

// reposition puts the frame at its percentages inside the current
// geometry.
func (p *webcamPreview) reposition() {
	margin, tw, th := p.travel()
	p.camX = margin + tw*int32(p.x)/100
	p.camY = margin + th*int32(p.y)/100
}

// percent is the frame's position as the config stores it.
func (p *webcamPreview) percent() (uint8, uint8) {
	margin, tw, th := p.travel()
	return pctFromPx(p.camX, margin, tw), pctFromPx(p.camY, margin, th)
}

// Arrange fits the geometry to the arranged width, then lays the frame
// over the whole preview and the cam at its offset; the boxes paint
// from their cascades.
func (p *webcamPreview) Arrange(rect render.Rect) {
	p.Base.Arrange(rect)
	p.pw, p.ph, p.cw, p.ch = previewGeometry(intconv.Clamp[int32](rect.W), p.sizePct)
	if !p.dragging {
		p.reposition()
	}
	p.layOut()
	widget.SetParents(p, p.frame, p.cam)
}

// layOut positions both boxes against the current bounds.
func (p *webcamPreview) layOut() {
	b := p.Bounds()
	if b.Empty() {
		return
	}
	p.frame.Arrange(render.Rect{X: b.X, Y: b.Y, W: int(p.pw), H: int(p.ph)})
	p.cam.Arrange(render.Rect{X: b.X + int(p.camX), Y: b.Y + int(p.camY), W: int(p.cw), H: int(p.ch)})
}

// Measure asks for no width of its own, taking the width it is given,
// and the 16:9 height for that width.
func (p *webcamPreview) Measure(axis widget.Axis, forSize int) widget.Sizes {
	if axis == widget.Row || forSize == widget.Unknown {
		return widget.Sizes{}
	}
	_, ph, _, _ := previewGeometry(intconv.Clamp[int32](forSize), p.sizePct)
	return widget.FixedSizes(int(ph))
}

func (p *webcamPreview) Paint(cv *render.Canvas) {
	widget.PaintChild(cv, p.frame)
	widget.PaintChild(cv, p.cam)
}

// Children exposes the boxes to the tree walks.
func (p *webcamPreview) Children() []widget.Widget { return []widget.Widget{p.frame, p.cam} }

func (p *webcamPreview) HitTest(pt widget.Point) widget.Widget { return p.HitLeaf(p, pt) }

// HoverMove tracks the pointer, so a press knows where it grabbed.
func (p *webcamPreview) HoverMove(pt widget.Point) { p.pointer = pt }

// SetPressed is drag begin and end.
func (p *webcamPreview) SetPressed(on bool) {
	b := p.Bounds()
	switch {
	case on && !p.dragging:
		p.dragging = true
		p.grabX = intconv.Clamp[int32](p.pointer.X-b.X) - p.camX
		p.grabY = intconv.Clamp[int32](p.pointer.Y-b.Y) - p.camY
	case !on && p.dragging:
		p.dragging = false
		p.x, p.y = p.percent()
		if p.onMove != nil {
			p.onMove(p.x, p.y)
		}
	}
	p.Invalidate()
}

// DragMove is drag update: the frame follows the pointer, clamped to
// the inset travel.
func (p *webcamPreview) DragMove(pt widget.Point) {
	p.pointer = pt
	if !p.dragging {
		return
	}
	b := p.Bounds()
	margin, tw, th := p.travel()
	p.camX = min(max(intconv.Clamp[int32](pt.X-b.X)-p.grabX, margin), margin+tw)
	p.camY = min(max(intconv.Clamp[int32](pt.Y-b.Y)-p.grabY, margin), margin+th)
	p.layOut()
	p.Invalidate()
}

// CursorName is the grab and grabbing cursors.
func (p *webcamPreview) CursorName() string {
	if p.dragging {
		return "grabbing"
	}
	return "grab"
}

// recorderView is the recorder dropdown: the live status in the
// header, the record and pause buttons, the audio card, and the webcam
// card (hidden without a camera).
type recorderView struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Box
	status     *widget.Box
	statusDot  *widget.Box
	time       *widget.Label
	record     *widget.Button
	recordFace *widget.ButtonContent
	pause      *widget.Button
	pauseIcon  *widget.Icon
	// micSlot holds the microphone picker, swapped when the device
	// list changes.
	micSlot    *widget.Box
	micPicker  *widget.Dropdown
	mics, cams []devicechoice.DeviceChoice
	webcam     *widget.Box
	preview    *webcamPreview

	*openFollow
	micReads     *refresher[[]devicechoice.DeviceChoice]
	camReads     *refresher[[]devicechoice.DeviceChoice]
	camSlot      *widget.Box
	replayMemory *widget.Label
	replaySave   *widget.Button
}

func recorderDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &recorderView{ctx: ctx, font: font, px: px}
	v.openFollow = newOpenFollow(ctx, v.follow)
	v.camReads = newRefresher(ctx, v.life, func(context.Context) []devicechoice.DeviceChoice { return recorderCameras() }, v.syncCams)
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "recorder-dropdown")
	cfg := ctx.Config.Recorder

	th := widget.Current()
	v.status = widget.NewBox(widget.Row, th.Spacing, 0)
	v.status.AddClass("recorder-status")
	v.statusDot = statusDot("recorder-status-dot")
	v.time = widget.NewLabel(font, px, "", 0)
	v.time.AddClass("recorder-status-time", css.Heading, css.Numeric)
	v.status.AppendAligned(v.statusDot, false, widget.AlignCenter)
	v.status.AppendAligned(v.time, false, widget.AlignCenter)
	v.Append(dropdownHeader(font, "ld-video-symbolic", i18n.T("dropdown-recorder-title"), v.status), false)

	// The content: everything below the header; its class lets the
	// stylesheet's `.recorder-dropdown-content` rules reach the rows,
	// cards, and section headers.
	content := dropdownContent("recorder-dropdown-content")
	v.Append(content, true)

	content.Append(v.controls(), false)

	audioCard, audio := itemCard()
	audioCard.AddClass("recorder-card")
	audio.Append(v.switchRow(i18n.T("dropdown-recorder-microphone"), cfg.Microphone, recorderPathMic))
	v.mics = microphoneSources(nil)
	micRow := v.row(i18n.T("dropdown-recorder-microphone-device"))
	v.micPicker = v.picker(v.mics, cfg.MicrophoneDevice, recorderPathMicDevice, func() []devicechoice.DeviceChoice { return v.mics })
	v.micSlot = widget.NewBox(widget.Row, 0, 0)
	v.micSlot.Append(v.micPicker, false)
	micRow.AddAction(v.micSlot)
	audio.Append(micRow)
	audio.Append(v.switchRow(i18n.T("dropdown-recorder-system-audio"), cfg.SystemAudio, recorderPathSystem))
	content.Append(dropdownGroup(v.sectionHeader("ld-mic-symbolic", i18n.T("dropdown-recorder-section-audio")), audioCard), false)

	v.cams = devicechoice.NoCameras()
	webcamCard, webcam := itemCard()
	webcamCard.AddClass("recorder-card")
	webcam.Append(v.switchRow(i18n.T("dropdown-recorder-webcam"), cfg.WebcamEnabled, recorderPathWebcam))
	camRow := v.row(i18n.T("dropdown-recorder-webcam-device"))
	v.camSlot = widget.NewBox(widget.Row, 0, 0)
	v.camSlot.Append(v.picker(v.cams, cfg.WebcamDevice, recorderPathCamDevice, func() []devicechoice.DeviceChoice { return v.cams }), false)
	camRow.AddAction(v.camSlot)
	webcam.Append(camRow)
	position := v.row(i18n.T("dropdown-recorder-position"))
	position.AddClass("recorder-position")
	webcam.Append(position)
	v.preview = newWebcamPreview(uint8(cfg.WebcamSize), uint8(cfg.WebcamX), uint8(cfg.WebcamY))
	v.preview.onMove = v.webcamMoved
	webcam.Append(v.positionPicker())
	v.webcam = dropdownGroup(v.sectionHeader("ld-camera-symbolic", i18n.T("dropdown-recorder-section-webcam")), webcamCard)
	v.webcam.SetVisible(len(v.cams) > 1)
	content.Append(v.webcam, false)

	replayCard, replay := itemCard()
	replayCard.AddClass("recorder-card")
	replay.Append(v.switchRow(i18n.T("dropdown-recorder-replay"), cfg.Replay.Enable, recorderPathReplay))
	memory := v.row(i18n.T("dropdown-recorder-replay-memory"))
	v.replayMemory = widget.NewLabel(font, px, "", 0)
	v.replayMemory.AddClass("recorder-replay-memory", css.Numeric)
	memory.AddAction(v.replayMemory)
	replay.Append(memory)
	content.Append(dropdownGroup(v.sectionHeader("ld-history-symbolic", i18n.T("dropdown-recorder-section-replay")), replayCard), false)
	v.replaySave = dropdownButton(widget.NewButtonContent(font, px, "ld-history-symbolic", i18n.T("dropdown-recorder-replay-save")), v.saveReplay, "recorder-replay-save-button")
	content.Append(v.replaySave, false)

	v.startFollowing()
	return v
}

// positionPicker is the preview's row in the webcam card: lys-ui's
// small item element, so the theme's item rule insets it exactly as it
// insets the setting rows.
func (v *recorderView) positionPicker() *widget.Box {
	row := widget.NewBox(widget.Column, 0, 0)
	row.SetElement("item")
	row.AddClass(css.Small, "recorder-position-picker")
	row.Append(v.preview, false)
	return row
}

// controls is the record/stop toggle and the pause button.
func (v *recorderView) controls() widget.Widget {
	row := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
	row.AddClass("recorder-controls")
	v.recordFace = widget.NewButtonContent(v.font, v.px, "ld-circle-dot-symbolic", "")
	v.record = dropdownButton(v.recordFace, func() {
		if v.ctx.Recorder != nil {
			v.ctx.Recorder.Toggle()
		}
	}, "recorder-record-button")
	row.Append(v.record, true)
	v.pauseIcon = widget.NewThemeIcon("ld-pause-symbolic", int(v.px))
	v.pause = dropdownButton(v.pauseIcon, func() {
		if r := v.ctx.Recorder; r != nil {
			r.SetPaused(!r.Snapshot().Paused)
		}
	}, "recorder-pause-button", css.Secondary, css.Icon)
	row.Append(v.pause, false)
	return row
}

// saveReplay saves the instant replay off the loop: the service reports
// the file or the failure through the OSD and a notification.
func (v *recorderView) saveReplay() {
	r := v.ctx.Recorder
	if r == nil {
		return
	}
	v.ctx.Go(func() {
		if _, err := r.SaveReplay(); err != nil {
			log.Printf("recorder: %v", err)
		}
	})
}

// sectionHeader is the recorder's icon-led section label.
func (v *recorderView) sectionHeader(icon, title string) *widget.Box {
	row := iconSectionLabel(v.font, icon, title)
	row.AddClass("recorder-section-header")
	return row
}

// row is a setting row on lys-ui's Item: the title, the control added
// as its action.
func (v *recorderView) row(title string) *widget.Item {
	r := widget.NewItem(v.font, title, "")
	r.SetSmall(true)
	r.AddClass("recorder-row")
	return r
}

// switchRow is a labeled switch writing a boolean config key.
func (v *recorderView) switchRow(title string, on bool, path string) *widget.Item {
	r := v.row(title)
	sw := widget.NewSwitch(on)
	sw.OnChanged = func(on bool) { v.ctx.setConfig(path, on) }
	r.AddAction(sw)
	return r
}

// picker is a device selector writing the chosen id; choices reads the
// list current at selection time.
func (v *recorderView) picker(list []devicechoice.DeviceChoice, saved, path string, choices func() []devicechoice.DeviceChoice) *widget.Dropdown {
	d := widget.NewDropdown(v.font, v.px, devicechoice.ChoiceLabels(list), devicechoice.ChoiceIndex(list, saved))
	d.OnSelect = func(i int) {
		if c := choices(); i >= 0 && i < len(c) {
			v.ctx.setConfig(path, c[i].ID)
		}
	}
	return d
}

// webcamMoved is WebcamMoved.
func (v *recorderView) webcamMoved(x, y uint8) {
	v.ctx.setConfig(recorderPathWebcamX, int64(x))
	v.ctx.setConfig(recorderPathWebcamY, int64(y))
}

func (v *recorderView) snapshot() recorder.Change {
	if v.ctx.Recorder == nil {
		return recorder.Change{}
	}
	return v.ctx.Recorder.Snapshot()
}

// applyState is StateChanged plus the view's #[watch]es. The record
// button's `primary`/`danger` classes and the status dot's `paused`
// class carry the colors; the stylesheet paints them.
func (v *recorderView) applyState(c recorder.Change) {
	v.status.SetVisible(c.Active)
	v.time.SetText(recorder.FormatElapsed(c.ElapsedSecs))
	if c.Paused {
		v.statusDot.AddClass("paused")
	} else {
		v.statusDot.RemoveClass("paused")
	}
	v.record.RemoveClass(css.DestructiveAction, css.SuggestedAction)
	if c.Active {
		v.record.AddClass(css.DestructiveAction)
		v.recordFace.SetIconName("ld-square-symbolic")
		v.recordFace.SetLabel(i18n.T("dropdown-recorder-stop"))
	} else {
		v.record.AddClass(css.SuggestedAction)
		v.recordFace.SetIconName("ld-circle-dot-symbolic")
		v.recordFace.SetLabel(i18n.T("dropdown-recorder-record"))
	}
	v.pause.SetEnabled(c.Active)
	armed := c.Replay == recorder.ReplayArmed
	v.replaySave.SetEnabled(armed)
	memory := "-"
	if armed {
		memory = sysinfo.AutoBytes(c.ReplayBytes)
	}
	v.replayMemory.SetText(memory)
	if c.Paused {
		v.pauseIcon.SetThemeName("ld-play-symbolic")
		v.pause.SetTooltip(i18n.T("dropdown-recorder-resume"))
	} else {
		v.pauseIcon.SetThemeName("ld-pause-symbolic")
		v.pause.SetTooltip(i18n.T("dropdown-recorder-pause"))
	}
}

// syncMics is MicrophonesUpdated: a changed source list rebuilds the
// picker, keeping the saved selection when it is still there.
func (v *recorderView) syncMics(mics []devicechoice.DeviceChoice) {
	if slices.Equal(mics, v.mics) {
		return
	}
	v.mics = mics
	saved := v.ctx.Config.Recorder.MicrophoneDevice
	next := v.picker(mics, saved, recorderPathMicDevice, func() []devicechoice.DeviceChoice { return v.mics })
	v.micSlot.Clear()
	v.micSlot.Append(next, false)
	v.micPicker = next
}

func (v *recorderView) syncCams(cams []devicechoice.DeviceChoice) {
	if slices.Equal(cams, v.cams) {
		return
	}
	v.cams = cams
	v.camSlot.Clear()
	v.camSlot.Append(v.picker(cams, v.ctx.Config.Recorder.WebcamDevice, recorderPathCamDevice, func() []devicechoice.DeviceChoice { return v.cams }), false)
	v.webcam.SetVisible(len(cams) > 1)
}

func (v *recorderView) follow(life context.Context) {
	v.applyState(v.snapshot())
	v.camReads.first()
	if r := v.ctx.Recorder; r != nil {
		changes, stop := r.Changes()
		v.ctx.Go(func() {
			defer stop()
			for {
				select {
				case <-life.Done():
					return
				case c, ok := <-changes:
					if !ok {
						return
					}
					v.ctx.Invoke(func() { v.applyState(c) })
				}
			}
		})
	}
	if src := v.ctx.Pulse; src != nil {
		if v.micReads == nil {
			v.micReads = newRefresher(v.ctx, v.life, func(context.Context) []devicechoice.DeviceChoice { return microphoneSources(src) }, v.syncMics)
		}
		followInto(life, "recorder microphones", src.Subscribe, v.micReads)
	}
}
