package notifications

import "github.com/stubbedev/lys-lib/dbusx"

// ActionInvocation is what ActionInvoked carries: the notification and
// the key of the action the user picked.
type ActionInvocation struct {
	ID  uint32 `dbus:"id"`
	Key string `dbus:"action_key"`
}

// TokenGrant is what ActivationToken carries: the notification and the
// token minted from the press on it.
type TokenGrant struct {
	ID    uint32 `dbus:"id"`
	Token string `dbus:"activation_token"`
}

// InlineReply is what NotificationReplied carries: the notification and
// the text the user typed into it.
type InlineReply struct {
	ID   uint32 `dbus:"id"`
	Text string `dbus:"text"`
}

// Closure is what NotificationClosed carries: the notification and why
// it closed.
type Closure struct {
	ID     uint32 `dbus:"id"`
	Reason uint32 `dbus:"reason"`
}

// The signals a notification server sends from ObjPath.
var (
	ActionInvoked       = dbusx.NewSignal[ActionInvocation](Interface, "ActionInvoked")
	ActivationToken     = dbusx.NewSignal[TokenGrant](Interface, "ActivationToken")
	NotificationReplied = dbusx.NewSignal[InlineReply](Interface, "NotificationReplied")
	NotificationClosed  = dbusx.NewSignal[Closure](Interface, "NotificationClosed")
)
