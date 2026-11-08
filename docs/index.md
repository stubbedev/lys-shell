---
layout: home

hero:
  name: lys-shell
  text: A Wayland desktop shell in pure Go
  tagline: Bar, notifications, OSD, launcher, wallpaper and a desktop portal, on any compositor with wlr-layer-shell.
  image:
    src: /lys-shell.svg
    alt: lys-shell
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: Config reference
      link: /config/
    - theme: alt
      text: GitHub
      link: https://github.com/stubbedev/lys-shell

features:
  - title: Bar
    details: Per-monitor layouts with groups and CSS classes. Clock and world clock, battery, power profiles, brightness, volume and microphone, network and VPN, bluetooth, media, cava visualizer, CPU, RAM, storage, network traffic, weather, mail, notifications, system tray, screen recorder, screenshot, idle inhibit, night light, keyboard layout and keybind mode, window title, a dashboard, and workspaces for lys-wm, Hyprland, Niri, Mango and Sway.
  - title: Surfaces
    details: Notifications with popups and history, an OSD and custom toasts, a power menu, a rofi-compatible launcher, locking through lys-login, region and window screenshots, a colour picker, and a wallpaper engine with transitions and cycling.
  - title: Desktop portal
    details: An xdg-desktop-portal backend covering FileChooser, ScreenCast and Screenshot with its own share picker, Print, AppChooser, Settings, Notification, Wallpaper, GlobalShortcuts, Clipboard, Inhibit and more, so GTK, Qt, Electron and Flatpak apps get native dialogs.
  - title: Networking
    details: NetworkManager Wi-Fi and wired control, and VPNs signed in from the network dropdown, including GlobalProtect SAML through openconnect.
  - title: Configuration
    details: YAML with imports and live reload, a JSON schema for editor completion, the lys-settings GUI, and the lys-shell config CLI. Every size takes a scale or exact pixels. Themes come from built-in palettes or from Matugen, Pywal and Wallust.
  - title: Scriptable
    details: Custom bar modules backed by any command, with icon and colour maps keyed on their output; a JSON-RPC socket and lys-shell widget update to push updates to a widget; lys-shell toast for one-off messages.
---
