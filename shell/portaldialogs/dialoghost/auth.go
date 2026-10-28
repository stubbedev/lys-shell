package dialoghost

import (
	"log"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// authDialog is the authentication dialog for one request: mapped once
// its turn comes, it stays up across the conversation's steps, showing
// the latest, until Dismiss.
type authDialog struct {
	h      *Dialogs
	handle string
	req    portaldialogs.AuthRequest
	// reply answers the Authenticate call waiting on the dialog; nil
	// while none waits (ShowAuthentication, or once answered), when the
	// user's actions go out as signals and the entry takes no answer.
	reply chan<- portaldialogs.AuthReply

	message  *widget.Label
	details  *widget.Box
	identity *widget.Dropdown
	notes    *widget.Box
	entry    *widget.Entry
	submit   *widget.Button
}

// Authenticate shows the authentication dialog for the caller's
// request, or updates the one showing, and waits for the user to
// answer the prompt, cancel or pick another identity.
func (h *Dialogs) Authenticate(c portaldialogs.Caller, r portaldialogs.AuthRequest) portaldialogs.AuthReply {
	ch := make(chan portaldialogs.AuthReply, 1)
	h.d.Invoke(func() { h.authStep(c, r, ch) })
	return <-ch
}

// ShowAuthentication shows the authentication dialog for the caller's
// request, or updates the one showing, with its notes and no prompt.
func (h *Dialogs) ShowAuthentication(c portaldialogs.Caller, r portaldialogs.AuthRequest) {
	h.d.Invoke(func() { h.authStep(c, r, nil) })
}

// authStep brings the dialog for c's handle to r: a new one queues for
// its turn; one already open takes the step in place, mapped or not.
func (h *Dialogs) authStep(c portaldialogs.Caller, r portaldialogs.AuthRequest, reply chan<- portaldialogs.AuthReply) {
	a, open := h.auths[c.Handle]
	if open && a.reply != nil {
		log.Printf("portal dialogs: authentication %s: a new step replaced the one waiting; it is cancelled", c.Handle)
		a.reply <- portaldialogs.AuthCancel{}
		a.reply = nil
	}
	if !open {
		a = &authDialog{h: h, handle: c.Handle, req: r, reply: reply}
		h.auths[c.Handle] = a
		h.queue.Enqueue(c.Handle, func() {
			h.answer = func(bool, string) { a.end() }
			h.app, h.appIcon = h.d.Describe(c.AppID)
			h.escape = func() { a.act(portaldialogs.AuthCancel{}) }
			tree := a.build()
			a.step(a.req, a.reply)
			h.grab = true
			if a.reply != nil {
				h.focus = a.entry
			}
			h.show(tree)
		}, a.end)
		return
	}
	if a.entry == nil {
		a.req, a.reply = r, reply
		return
	}
	a.step(r, reply)
	if reply != nil && h.win != nil {
		if err := h.win.SetFocus(a.entry); err != nil {
			log.Printf("portal dialogs: cannot focus the authentication entry: %v", err)
		}
	}
}

// build makes the dialog's widgets, filled by step.
func (a *authDialog) build() widget.Widget {
	h := a.h
	a.message = h.label("", "portal-dialog-auth-message")
	a.details = widget.NewBox(widget.Column, 0, 0)
	a.details.AddClass("portal-dialog-auth-details")
	a.identity = widget.NewDropdown(h.d.Font, widgets.TextPx(styling.Body, 1), nil, 0)
	a.identity.AddClass("portal-dialog-auth-identity")
	a.identity.OnSelect = func(i int) {
		if uid := a.req.Identities[i].UID; uid != a.req.Selected {
			a.act(portaldialogs.AuthSwitch{UID: uid})
		}
	}
	a.notes = widget.NewBox(widget.Column, 0, 0)
	a.notes.AddClass("portal-dialog-auth-notes")
	a.entry = h.passwordEntry("")
	a.entry.OnActivate = func(string) { a.answer() }
	a.submit = h.accept("Authenticate", a.answer)
	cancel := widgets.DialogButton(h.d.Font, "Cancel", "portal-dialog-cancel", css.Outline, func() { a.act(portaldialogs.AuthCancel{}) })
	rows := []widget.Widget{a.message, a.details, a.identity, a.notes, a.entry}
	return h.surface("Authentication Required", "", rows, cancel, a.submit)
}

// step shows r, prompting for an answer while reply waits for one.
func (a *authDialog) step(r portaldialogs.AuthRequest, reply chan<- portaldialogs.AuthReply) {
	a.req, a.reply = r, reply
	h := a.h
	a.message.SetText(r.Message)
	a.details.Clear()
	for _, d := range r.Details {
		a.details.Append(h.label(d.Label+": "+d.Value, "portal-dialog-auth-detail", css.DimLabel), false)
	}
	names := make([]string, len(r.Identities))
	selected := 0
	for i, id := range r.Identities {
		names[i] = id.Name
		if id.UID == r.Selected {
			selected = i
		}
	}
	a.identity.SetItems(names, selected)
	a.notes.Clear()
	for _, n := range r.Notes {
		class := "portal-dialog-auth-info"
		if n.Error {
			class = "portal-dialog-error"
		}
		a.notes.Append(h.label(n.Text, class), false)
	}
	prompting := reply != nil
	a.entry.SetText("")
	a.entry.SetPlaceholder(r.Prompt)
	a.entry.SetEcho(widget.EchoPassword)
	if r.Echo {
		a.entry.SetEcho(widget.EchoNormal)
	}
	a.entry.SetReadOnly(!prompting)
	a.entry.SetVisible(prompting)
	a.submit.SetVisible(prompting)
}

// answer sends the entry's text to the waiting call and clears the
// entry, which takes nothing more until the next step.
func (a *authDialog) answer() {
	if a.reply == nil {
		return
	}
	reply := portaldialogs.AuthAnswer{Response: portaldialogs.NewResponse(a.entry.Text())}
	a.entry.SetText("")
	a.respond(reply)
}

// act is the user cancelling or picking another identity: the reply to
// the waiting call, else a signal.
func (a *authDialog) act(action portaldialogs.AuthAction) {
	if a.reply == nil {
		a.h.d.Signal(a.handle, action)
		return
	}
	a.respond(action)
}

// respond answers the waiting call and holds the prompt until the next
// step.
func (a *authDialog) respond(reply portaldialogs.AuthReply) {
	a.reply <- reply
	a.reply = nil
	a.entry.SetReadOnly(true)
	a.submit.SetVisible(false)
}

// end closes the request: a call still waiting is cancelled, and the
// handle's next step opens a new dialog.
func (a *authDialog) end() {
	if a.reply != nil {
		a.reply <- portaldialogs.AuthCancel{}
		a.reply = nil
	}
	if a.entry != nil {
		a.entry.SetText("")
	}
	delete(a.h.auths, a.handle)
}
