// Package portalreq is what the desktop-portal backend tells a shell
// dialog about the request it serves.
package portalreq

// Caller identifies the portal request behind a shell dialog: Handle is
// the request's object path, the key the services' Dismiss names;
// AppID is the app asking ("" for a host app); ParentWindow is the
// app's window identifier ("wayland:<xdg-foreign handle>", or "");
// Modal is whether the dialog should hold the keyboard until answered
// (the portal's modal option, true unless the app said otherwise).
// Over D-Bus it is the struct (sssb).
type Caller struct {
	Handle, AppID, ParentWindow string
	Modal                       bool
}
