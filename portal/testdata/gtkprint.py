import sys

import gi

gi.require_version("Gtk", sys.argv[1])
gi.require_version("Gdk", sys.argv[1])
from gi.repository import Gdk, GLib, Gtk  # noqa: E402

if Gdk.Display.get_default() is None:
    sys.exit("gtkprint: no display")

op = Gtk.PrintOperation()
op.set_n_pages(int(sys.argv[2]))
op.set_job_name("Doc")


def draw(op, context, page):
    print(page, flush=True)
    cr = context.get_cairo_context()
    cr.rectangle(10, 10, 10 + page, 10)
    cr.fill()


op.connect("draw-page", draw)
result = op.run(Gtk.PrintOperationAction.PRINT_DIALOG, None)
if result != Gtk.PrintOperationResult.APPLY:
    sys.exit(f"gtkprint: the dialog answered {result.value_nick}")
print("sent", flush=True)
GLib.MainLoop().run()
