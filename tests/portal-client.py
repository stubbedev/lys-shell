"""A portal client for the VM checks: asks the xdg-desktop-portal
frontend for a screenshot or a screencast, as an application would,
and prints the response as JSON. Exits 0 on success, 1 on a cancel or
failure, so the test script can assert the outcome.

    portal-client screenshot
    portal-client screencast
"""

import json
import secrets
import sys

from jeepney import DBusAddress, MatchRule, new_method_call
from jeepney.io.blocking import open_dbus_connection

PORTAL = "org.freedesktop.portal.Desktop"
PATH = "/org/freedesktop/portal/desktop"

conn = open_dbus_connection(bus="SESSION")
sender = conn.unique_name[1:].replace(".", "_")


def token():
    return "lys" + secrets.token_hex(4)


def request(interface, method, signature, *args, options):
    """Calls method with a fresh handle_token and waits for the Response
    signal on its request object: (code, results)."""
    handle = token()
    path = f"/org/freedesktop/portal/desktop/request/{sender}/{handle}"
    rule = MatchRule(
        type="signal",
        interface="org.freedesktop.portal.Request",
        member="Response",
        path=path,
    )
    conn.send_and_get_reply(
        new_method_call(
            DBusAddress("/org/freedesktop/DBus", bus_name="org.freedesktop.DBus",
                        interface="org.freedesktop.DBus"),
            "AddMatch", "s", (rule.serialise(),),
        )
    )
    options = dict(options)
    options["handle_token"] = ("s", handle)
    call = new_method_call(
        DBusAddress(PATH, bus_name=PORTAL, interface=interface),
        method,
        signature + "a{sv}",
        (*args, options),
    )
    with conn.filter(rule) as signals:
        reply = conn.send_and_get_reply(call, timeout=60)
        if reply.header.message_type.name == "error":
            raise SystemExit(f"{method}: {reply.body}")
        signal = conn.recv_until_filtered(signals, timeout=240)
    code, results = signal.body
    return code, results


def plain(value):
    if isinstance(value, tuple) and len(value) == 2 and isinstance(value[0], str):
        return plain(value[1])
    if isinstance(value, dict):
        return {k: plain(v) for k, v in value.items()}
    if isinstance(value, list):
        return [plain(v) for v in value]
    return value


def screenshot():
    return request(
        "org.freedesktop.portal.Screenshot", "Screenshot", "s", "",
        options={"interactive": ("b", False)},
    )


def screencast():
    cast = "org.freedesktop.portal.ScreenCast"
    code, results = request(
        cast, "CreateSession", "",
        options={"session_handle_token": ("s", token())},
    )
    if code != 0:
        return code, results
    session = results["session_handle"][1]
    code, results = request(
        cast, "SelectSources", "o", session,
        options={"types": ("u", 3), "multiple": ("b", False)},
    )
    if code != 0:
        return code, results
    return request(cast, "Start", "os", session, "", options={})


code, results = {"screenshot": screenshot, "screencast": screencast}[sys.argv[1]]()
print(json.dumps({"code": code, "results": plain(results)}))
sys.exit(0 if code == 0 else 1)
