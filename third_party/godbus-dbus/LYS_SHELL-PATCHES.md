# godbus/dbus v5.2.2, patched for lys-shell

A copy of github.com/godbus/dbus/v5 v5.2.2 (also unfixed on master
6fea989, 2026-09-24), wired in through go.mod's `replace`. Drop it once
upstream decodes fd arrays.

- `decoder.go`, case `'a'`: an `ah` decoded with the message's fds made a
  `[]UnixFDIndex` and appended `UnixFD` values to it. The reflect panic was
  a string, which `Decode`'s recover does not turn into an error, so every
  message carrying an fd array (xdg-desktop-portal's Email
  `attachment_fds`) arrived with an empty body. The slice is now
  `[]UnixFD`, and an index past the fds is an InvalidMessageError.
  Pinned by `lys_shell_fd_array_test.go`.
- `encoder.go`, case `variantType`, and `lys_shell_encode_as.go`: a Variant's
  value is encoded by the Variant's signature. `decode` returns a struct as
  `[]any`, which the Go-type encoder wrote as an array of variants under the
  struct signature: a malformed message, which dbus-daemon answers by
  disconnecting the sender. Echoing a decoded GIcon (DynamicLauncher's
  PrepareInstall) killed the portal's bus connection. Pinned by
  `TestDecodedStructsReencode`.
- `lys_shell_file_reply.go` (with `sig.go`, `encoder.go`, `dbus.go`,
  `export.go`): `*os.File` marshals as `h`, and the files an exported
  method replies are closed once the reply is sent. A method returning a
  bare UnixFD had no safe moment to close its copy, since the reply goes
  out after it returns. The portal's clipboard pipes and PipeWire remote
  ride on this. Pinned by `TestAFileReplyIsSentThenClosed`.
- `lys_shell_ordered.go` (with `conn.go`'s read loop): `DispatchInOrder`
  runs an interface's named methods one at a time in arrival order on one
  goroutine, through a queue bounded at 256 that holds back the read loop
  when full; every other call still runs on a goroutine of its own.
  Upstream dispatches each call on its own goroutine, so calls a sender
  does not wait on race: xdg-desktop-portal forwards RemoteDesktop input
  that way, and a click could land before the motion that placed it.
  Pinned by `TestOrderedCallsRunInArrivalOrder`.
