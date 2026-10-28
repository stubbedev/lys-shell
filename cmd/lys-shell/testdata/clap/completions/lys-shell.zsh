#compdef lys-shell

autoload -U is-at-least

_lys-shell() {
    typeset -A opt_args
    typeset -a _arguments_options
    local ret=1

    if is-at-least 5.2; then
        _arguments_options=(-s -S -C)
    else
        _arguments_options=(-s -C)
    fi

    local context curcontext="$curcontext" state line
    _arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'-V[Print version]' \
'--version[Print version]' \
":: :_lys-shell_commands" \
"*::: :->lys-shell" \
&& ret=0
    case $state in
    (lys-shell)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-command-$line[1]:"
        case $line[1] in
            (audio)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__audio_commands" \
"*::: :->audio" \
&& ret=0

    case $state in
    (audio)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-audio-command-$line[1]:"
        case $line[1] in
            (output-volume)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::level -- Volume level (0-100) or relative adjustment (+5, -10):_default' \
&& ret=0
;;
(output-mute)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(input-volume)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::level -- Volume level (0-100) or relative adjustment (+5, -10):_default' \
&& ret=0
;;
(input-mute)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(sinks)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(sources)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__audio__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-audio-help-command-$line[1]:"
        case $line[1] in
            (output-volume)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(output-mute)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(input-volume)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(input-mute)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sinks)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sources)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(brightness)
_arguments "${_arguments_options[@]}" : \
'-o+[Output connector name (e.g. DP-1); the focused output when omitted]:NAME:_default' \
'--output=[Output connector name (e.g. DP-1); the focused output when omitted]:NAME:_default' \
'-h[Print help]' \
'--help[Print help]' \
':change -- Percent\: N sets, +N raises, -N lowers; never below modules.brightness.min-brightness:_default' \
&& ret=0
;;
(config)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__config_commands" \
"*::: :->config" \
&& ret=0

    case $state in
    (config)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-config-command-$line[1]:"
        case $line[1] in
            (get)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':path -- The configuration path to retrieve (e.g., "modules.battery.enabled"):_default' \
&& ret=0
;;
(set)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':path -- The configuration path to set (e.g., "modules.battery.enabled"):_default' \
':value -- The value to set, as YAML (e.g. true, 5, \[a, b\]); anything else is a string:_default' \
&& ret=0
;;
(reset)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':path -- The configuration path to reset (e.g., "bar.button_gap"):_default' \
&& ret=0
;;
(schema)
_arguments "${_arguments_options[@]}" : \
'--stdout[Print to stdout instead of writing to config directory]' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(default)
_arguments "${_arguments_options[@]}" : \
'--stdout[Print to stdout instead of writing config.yaml.example]' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(docs)
_arguments "${_arguments_options[@]}" : \
'--out=[Output directory for the generated pages]:OUT:_files' \
'--only=[Regenerate only the named module]:ONLY:_default' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__config__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-config-help-command-$line[1]:"
        case $line[1] in
            (get)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(set)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(reset)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(schema)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(default)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(docs)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(icons)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__icons_commands" \
"*::: :->icons" \
&& ret=0

    case $state in
    (icons)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-icons-command-$line[1]:"
        case $line[1] in
            (setup)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(install)
_arguments "${_arguments_options[@]}" : \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
':source -- Source name (run '\''lys-shell icons sources'\'' to see available sources):_default' \
'*::slugs -- Icon slugs to install (e.g., home settings bell):_default' \
&& ret=0
;;
(import)
_arguments "${_arguments_options[@]}" : \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
':path -- Path to SVG file or directory:_files' \
'::name -- Icon name (required for single file, ignored for directory):_default' \
&& ret=0
;;
(remove)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'*::names -- Icon names to remove (e.g., tb-home-symbolic si-firefox-symbolic):_default' \
&& ret=0
;;
(sources)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(list)
_arguments "${_arguments_options[@]}" : \
'-s+[Filter by source prefix (e.g., tb, si, md)]:SOURCE:_default' \
'--source=[Filter by source prefix (e.g., tb, si, md)]:SOURCE:_default' \
'-i[Interactive fuzzy search (requires fzf)]' \
'--interactive[Interactive fuzzy search (requires fzf)]' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(open)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(export)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':destination -- Destination directory for exported icons:_files' \
&& ret=0
;;
(sync)
_arguments "${_arguments_options[@]}" : \
'--dry-run[Preview what would be installed without making changes]' \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__icons__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-icons-help-command-$line[1]:"
        case $line[1] in
            (setup)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(install)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(import)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(remove)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sources)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(open)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(export)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sync)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(media)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__media_commands" \
"*::: :->media" \
&& ret=0

    case $state in
    (media)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-media-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(play-pause)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::player -- Player identifier (number or partial name match):_default' \
&& ret=0
;;
(next)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::player -- Player identifier (number or partial name match):_default' \
&& ret=0
;;
(previous)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::player -- Player identifier (number or partial name match):_default' \
&& ret=0
;;
(shuffle)
_arguments "${_arguments_options[@]}" : \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
'::state -- Shuffle state:((on\:"Enable shuffle"
off\:"Disable shuffle"
toggle\:"Toggle shuffle state"))' \
'::player -- Player identifier (number or partial name match):_default' \
&& ret=0
;;
(loop)
_arguments "${_arguments_options[@]}" : \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
':mode -- Loop mode:((none\:"No looping"
track\:"Loop current track"
playlist\:"Loop entire playlist"))' \
'::player -- Player identifier (number or partial name match):_default' \
&& ret=0
;;
(active)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::player -- Player to set as active (number or partial name match):_default' \
&& ret=0
;;
(info)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::player -- Player identifier (number or partial name match):_default' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__media__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-media-help-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(play-pause)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(next)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(previous)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(shuffle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(loop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(active)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(info)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(notify)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__notify_commands" \
"*::: :->notify" \
&& ret=0

    case $state in
    (notify)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-notify-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(dismiss)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':id -- Notification ID to dismiss:_default' \
&& ret=0
;;
(dismiss-all)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(dnd)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__notify__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-notify-help-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dismiss)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dismiss-all)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dnd)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(panel)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__panel_commands" \
"*::: :->panel" \
&& ret=0

    case $state in
    (panel)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-panel-command-$line[1]:"
        case $line[1] in
            (start)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(restart)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(settings)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(inspect)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(hide)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::monitor -- Monitor connector name (e.g., "DP-1"). Omit to hide all:_default' \
&& ret=0
;;
(show)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::monitor -- Monitor connector name (e.g., "DP-1"). Omit to show all:_default' \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::monitor -- Monitor connector name (e.g., "DP-1"). Omit to toggle all:_default' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__panel__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-panel-help-command-$line[1]:"
        case $line[1] in
            (start)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(restart)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(settings)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(inspect)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(hide)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(show)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(power)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__power_commands" \
"*::: :->power" \
&& ret=0

    case $state in
    (power)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-power-command-$line[1]:"
        case $line[1] in
            (status)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(set)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':profile -- Profile name (power-saver, balanced, performance):_default' \
&& ret=0
;;
(cycle)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(list)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__power__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-power-help-command-$line[1]:"
        case $line[1] in
            (status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(set)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(cycle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(systray)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__systray_commands" \
"*::: :->systray" \
&& ret=0

    case $state in
    (systray)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-systray-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(activate)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':id -- Tray item ID to activate:_default' \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__systray__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-systray-help-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(activate)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(wallpaper)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__wallpaper_commands" \
"*::: :->wallpaper" \
&& ret=0

    case $state in
    (wallpaper)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-wallpaper-command-$line[1]:"
        case $line[1] in
            (set)
_arguments "${_arguments_options[@]}" : \
'-f+[Image fit mode]:FIT:((fill\:"Scale to cover entire display"
fit\:"Scale to fit within display"
center\:"Display at original size, centered"
tile\:"Tile the image"
stretch\:"Stretch to fill"))' \
'--fit=[Image fit mode]:FIT:((fill\:"Scale to cover entire display"
fit\:"Scale to fit within display"
center\:"Display at original size, centered"
tile\:"Tile the image"
stretch\:"Stretch to fill"))' \
'--monitor=[Target monitor (e.g., DP-1, HDMI-A-1). If omitted, applies to all monitors]:MONITOR:_default' \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
':path -- Path to wallpaper image:_files' \
&& ret=0
;;
(cycle)
_arguments "${_arguments_options[@]}" : \
'-i+[Interval in seconds between changes]:INTERVAL:_default' \
'--interval=[Interval in seconds between changes]:INTERVAL:_default' \
'-m+[Cycling mode]:MODE:((sequential\:"Cycle in alphabetical order"
shuffle\:"Cycle in random order"))' \
'--mode=[Cycling mode]:MODE:((sequential\:"Cycle in alphabetical order"
shuffle\:"Cycle in random order"))' \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
':directory -- Directory containing wallpaper images:_files' \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(next)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(previous)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(info)
_arguments "${_arguments_options[@]}" : \
'--monitor=[Target monitor (e.g., DP-1, HDMI-A-1). If omitted, shows global state]:MONITOR:_default' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(theming-monitor)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':monitor -- Monitor connector name (e.g., DP-1). Use empty string for default:_default' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__wallpaper__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-wallpaper-help-command-$line[1]:"
        case $line[1] in
            (set)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(cycle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(next)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(previous)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(info)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(theming-monitor)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(idle)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__idle_commands" \
"*::: :->idle" \
&& ret=0

    case $state in
    (idle)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-idle-command-$line[1]:"
        case $line[1] in
            (on)
_arguments "${_arguments_options[@]}" : \
'-i[Force indefinite mode (ignore default duration)]' \
'--indefinite[Force indefinite mode (ignore default duration)]' \
'-h[Print help]' \
'--help[Print help]' \
'::minutes -- Duration in minutes (omit to use default duration):_default' \
&& ret=0
;;
(off)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(duration)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':value -- +N to add, -N to subtract, N to set absolute:_default' \
&& ret=0
;;
(remaining)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':value -- +N to add, -N to subtract, N to set absolute:_default' \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
'-i[Use indefinite mode when enabling]' \
'--indefinite[Use indefinite mode when enabling]' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__idle__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-idle-help-command-$line[1]:"
        case $line[1] in
            (on)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(off)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(duration)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(remaining)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(launcher)
_arguments "${_arguments_options[@]}" : \
'*::args -- Raw rofi-style args (single-dash long flags, e.g. `-show drun`):_default' \
&& ret=0
;;
(lock)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(logout)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(vpn)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__vpn_commands" \
"*::: :->vpn" \
&& ret=0

    case $state in
    (vpn)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-vpn-command-$line[1]:"
        case $line[1] in
            (sso-callback)
_arguments "${_arguments_options[@]}" : \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
':uri -- The `globalprotectcallback\:` URI the browser was sent to:_default' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__vpn__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-vpn-help-command-$line[1]:"
        case $line[1] in
            (sso-callback)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(recorder)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__recorder_commands" \
"*::: :->recorder" \
&& ret=0

    case $state in
    (recorder)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-recorder-command-$line[1]:"
        case $line[1] in
            (start)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(pause)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(resume)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__recorder__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-recorder-help-command-$line[1]:"
        case $line[1] in
            (start)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(pause)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(resume)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(screenshot)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__screenshot_commands" \
"*::: :->screenshot" \
&& ret=0

    case $state in
    (screenshot)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-screenshot-command-$line[1]:"
        case $line[1] in
            (region)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(output)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
'::name -- Output connector name (e.g. DP-1):_default' \
&& ret=0
;;
(window)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__screenshot__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-screenshot-help-command-$line[1]:"
        case $line[1] in
            (region)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(output)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(window)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(widget)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__widget_commands" \
"*::: :->widget" \
&& ret=0

    case $state in
    (widget)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-widget-command-$line[1]:"
        case $line[1] in
            (update)
_arguments "${_arguments_options[@]}" : \
'-h[Print help (see more with '\''--help'\'')]' \
'--help[Print help (see more with '\''--help'\'')]' \
':id -- Config id of the target widget (e.g. a custom module'\''s `id`):_default' \
':output -- Output payload (plain text, or a JSON object):_default' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__widget__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-widget-help-command-$line[1]:"
        case $line[1] in
            (update)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(toast)
_arguments "${_arguments_options[@]}" : \
'--icon=[Icon name shown beside the text]:ICON:_default' \
'--percentage=[Progress percentage (0-100); shows a progress bar when set]:PERCENTAGE:_default' \
'--duration=[Auto-dismiss duration in milliseconds (toast config default when unset)]:DURATION:_default' \
'--preset=[Preset id from \`\[\[toasts.presets\]\]\` to base this toast on]:PRESET:_default' \
'--class=[Extra CSS class applied to the toast for custom styling]:CLASS:_default' \
'-h[Print help]' \
'--help[Print help]' \
'::label -- Toast text. Optional when `--preset` supplies one:_default' \
&& ret=0
;;
(portal)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__portal_commands" \
"*::: :->portal" \
&& ret=0

    case $state in
    (portal)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-portal-command-$line[1]:"
        case $line[1] in
            (run)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(share-picker)
_arguments "${_arguments_options[@]}" : \
'--allow-token[Pre-check the "allow restore token" box]' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(show)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
":: :_lys-shell__portal__show_commands" \
"*::: :->show" \
&& ret=0

    case $state in
    (show)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-portal-show-command-$line[1]:"
        case $line[1] in
            (file-chooser)
_arguments "${_arguments_options[@]}" : \
'--save[Show the save dialog instead of the open dialog]' \
'--multiple[Allow selecting multiple files (open only)]' \
'--directory[Select a directory instead of files (open only)]' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(screenshot)
_arguments "${_arguments_options[@]}" : \
'--mode=[Capture mode\: \`region\`, \`output\`, \`screen\`, or \`window\`]:MODE:_default' \
'--target=[Output connector name (used by \`output\` mode)]:TARGET:_default' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(color)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(print)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(screen-cast)
_arguments "${_arguments_options[@]}" : \
'--allow-token[Pre-check the "allow restore token" box]' \
'--multiple[Allow selecting multiple sources]' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(access)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(account)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(app-chooser)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(dynamic-launcher)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(authenticate)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(wallpaper)
_arguments "${_arguments_options[@]}" : \
'--uri=[\`file\://\` image URI to preview (defaults to a placeholder)]:URI:_default' \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__portal__show__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-portal-show-help-command-$line[1]:"
        case $line[1] in
            (file-chooser)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(screenshot)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(color)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(print)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(screen-cast)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(access)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(account)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(app-chooser)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dynamic-launcher)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(authenticate)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(wallpaper)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__portal__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-portal-help-command-$line[1]:"
        case $line[1] in
            (run)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(share-picker)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(show)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__portal__help__show_commands" \
"*::: :->show" \
&& ret=0

    case $state in
    (show)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-portal-help-show-command-$line[1]:"
        case $line[1] in
            (file-chooser)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(screenshot)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(color)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(print)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(screen-cast)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(access)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(account)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(app-chooser)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dynamic-launcher)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(authenticate)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(wallpaper)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(shell)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
&& ret=0
;;
(completions)
_arguments "${_arguments_options[@]}" : \
'-h[Print help]' \
'--help[Print help]' \
':shell -- Shell to generate completions for:(bash elvish fish powershell zsh)' \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help_commands" \
"*::: :->help" \
&& ret=0

    case $state in
    (help)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-command-$line[1]:"
        case $line[1] in
            (audio)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__audio_commands" \
"*::: :->audio" \
&& ret=0

    case $state in
    (audio)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-audio-command-$line[1]:"
        case $line[1] in
            (output-volume)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(output-mute)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(input-volume)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(input-mute)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sinks)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sources)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(brightness)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(config)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__config_commands" \
"*::: :->config" \
&& ret=0

    case $state in
    (config)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-config-command-$line[1]:"
        case $line[1] in
            (get)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(set)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(reset)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(schema)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(default)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(docs)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(icons)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__icons_commands" \
"*::: :->icons" \
&& ret=0

    case $state in
    (icons)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-icons-command-$line[1]:"
        case $line[1] in
            (setup)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(install)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(import)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(remove)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sources)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(open)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(export)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(sync)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(media)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__media_commands" \
"*::: :->media" \
&& ret=0

    case $state in
    (media)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-media-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(play-pause)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(next)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(previous)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(shuffle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(loop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(active)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(info)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(notify)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__notify_commands" \
"*::: :->notify" \
&& ret=0

    case $state in
    (notify)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-notify-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dismiss)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dismiss-all)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dnd)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(panel)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__panel_commands" \
"*::: :->panel" \
&& ret=0

    case $state in
    (panel)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-panel-command-$line[1]:"
        case $line[1] in
            (start)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(restart)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(settings)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(inspect)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(hide)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(show)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(power)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__power_commands" \
"*::: :->power" \
&& ret=0

    case $state in
    (power)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-power-command-$line[1]:"
        case $line[1] in
            (status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(set)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(cycle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(systray)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__systray_commands" \
"*::: :->systray" \
&& ret=0

    case $state in
    (systray)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-systray-command-$line[1]:"
        case $line[1] in
            (list)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(activate)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(wallpaper)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__wallpaper_commands" \
"*::: :->wallpaper" \
&& ret=0

    case $state in
    (wallpaper)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-wallpaper-command-$line[1]:"
        case $line[1] in
            (set)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(cycle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(next)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(previous)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(info)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(theming-monitor)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(idle)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__idle_commands" \
"*::: :->idle" \
&& ret=0

    case $state in
    (idle)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-idle-command-$line[1]:"
        case $line[1] in
            (on)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(off)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(duration)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(remaining)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(launcher)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(lock)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(logout)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(vpn)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__vpn_commands" \
"*::: :->vpn" \
&& ret=0

    case $state in
    (vpn)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-vpn-command-$line[1]:"
        case $line[1] in
            (sso-callback)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(recorder)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__recorder_commands" \
"*::: :->recorder" \
&& ret=0

    case $state in
    (recorder)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-recorder-command-$line[1]:"
        case $line[1] in
            (start)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(stop)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(toggle)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(pause)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(resume)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(status)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(screenshot)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__screenshot_commands" \
"*::: :->screenshot" \
&& ret=0

    case $state in
    (screenshot)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-screenshot-command-$line[1]:"
        case $line[1] in
            (region)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(output)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(window)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(widget)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__widget_commands" \
"*::: :->widget" \
&& ret=0

    case $state in
    (widget)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-widget-command-$line[1]:"
        case $line[1] in
            (update)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
(toast)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(portal)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__portal_commands" \
"*::: :->portal" \
&& ret=0

    case $state in
    (portal)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-portal-command-$line[1]:"
        case $line[1] in
            (run)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(share-picker)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(show)
_arguments "${_arguments_options[@]}" : \
":: :_lys-shell__help__portal__show_commands" \
"*::: :->show" \
&& ret=0

    case $state in
    (show)
        words=($line[1] "${words[@]}")
        (( CURRENT += 1 ))
        curcontext="${curcontext%:*:*}:lys-shell-help-portal-show-command-$line[1]:"
        case $line[1] in
            (file-chooser)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(screenshot)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(color)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(print)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(screen-cast)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(access)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(account)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(app-chooser)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(dynamic-launcher)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(authenticate)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(wallpaper)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
;;
(shell)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(completions)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
(help)
_arguments "${_arguments_options[@]}" : \
&& ret=0
;;
        esac
    ;;
esac
;;
        esac
    ;;
esac
}

(( $+functions[_lys-shell_commands] )) ||
_lys-shell_commands() {
    local commands; commands=(
'audio:Audio control commands' \
'brightness:Set an output'\''s brightness through its backlight or DDC/CI' \
'config:Configuration management commands' \
'icons:Icon management commands' \
'media:Media player control commands' \
'notify:Notification control commands' \
'panel:Panel management commands' \
'power:Power profile commands' \
'systray:System tray commands' \
'wallpaper:Wallpaper control commands' \
'idle:Idle inhibit control commands' \
'launcher:Application launcher / dmenu with rofi-compatible flags' \
'lock:Lock the session via lys-shell'\''s lock screen' \
'logout:Log out of the session through lys-wm or logind' \
'vpn:VPN commands' \
'recorder:Screen recorder control commands' \
'screenshot:Screenshot capture commands' \
'widget:Widget control commands' \
'toast:Show a custom on-screen toast' \
'portal:xdg-desktop-portal backend, screencast picker stub, and dialog previewer' \
'shell:Run the desktop shell in the foreground' \
'completions:Generate shell completions' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell commands' commands "$@"
}
(( $+functions[_lys-shell__audio_commands] )) ||
_lys-shell__audio_commands() {
    local commands; commands=(
'output-volume:Get or set output volume level' \
'output-mute:Toggle output mute state' \
'input-volume:Get or set input volume level' \
'input-mute:Toggle input mute state' \
'sinks:List available audio sinks (outputs)' \
'sources:List available audio sources (inputs)' \
'status:Show current audio status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell audio commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help_commands] )) ||
_lys-shell__audio__help_commands() {
    local commands; commands=(
'output-volume:Get or set output volume level' \
'output-mute:Toggle output mute state' \
'input-volume:Get or set input volume level' \
'input-mute:Toggle input mute state' \
'sinks:List available audio sinks (outputs)' \
'sources:List available audio sources (inputs)' \
'status:Show current audio status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell audio help commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__help_commands] )) ||
_lys-shell__audio__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help help commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__input-mute_commands] )) ||
_lys-shell__audio__help__input-mute_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help input-mute commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__input-volume_commands] )) ||
_lys-shell__audio__help__input-volume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help input-volume commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__output-mute_commands] )) ||
_lys-shell__audio__help__output-mute_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help output-mute commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__output-volume_commands] )) ||
_lys-shell__audio__help__output-volume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help output-volume commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__sinks_commands] )) ||
_lys-shell__audio__help__sinks_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help sinks commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__sources_commands] )) ||
_lys-shell__audio__help__sources_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help sources commands' commands "$@"
}
(( $+functions[_lys-shell__audio__help__status_commands] )) ||
_lys-shell__audio__help__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio help status commands' commands "$@"
}
(( $+functions[_lys-shell__audio__input-mute_commands] )) ||
_lys-shell__audio__input-mute_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio input-mute commands' commands "$@"
}
(( $+functions[_lys-shell__audio__input-volume_commands] )) ||
_lys-shell__audio__input-volume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio input-volume commands' commands "$@"
}
(( $+functions[_lys-shell__audio__output-mute_commands] )) ||
_lys-shell__audio__output-mute_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio output-mute commands' commands "$@"
}
(( $+functions[_lys-shell__audio__output-volume_commands] )) ||
_lys-shell__audio__output-volume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio output-volume commands' commands "$@"
}
(( $+functions[_lys-shell__audio__sinks_commands] )) ||
_lys-shell__audio__sinks_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio sinks commands' commands "$@"
}
(( $+functions[_lys-shell__audio__sources_commands] )) ||
_lys-shell__audio__sources_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio sources commands' commands "$@"
}
(( $+functions[_lys-shell__audio__status_commands] )) ||
_lys-shell__audio__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell audio status commands' commands "$@"
}
(( $+functions[_lys-shell__brightness_commands] )) ||
_lys-shell__brightness_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell brightness commands' commands "$@"
}
(( $+functions[_lys-shell__completions_commands] )) ||
_lys-shell__completions_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell completions commands' commands "$@"
}
(( $+functions[_lys-shell__config_commands] )) ||
_lys-shell__config_commands() {
    local commands; commands=(
'get:Get the value of a configuration path' \
'set:Set the value of a configuration path' \
'reset:Reset a configuration path to its default value' \
'schema:Output JSON Schema for the configuration (for editor intellisense)' \
'default:Output the default configuration as YAML' \
'docs:Generate markdown reference pages for every registered schema' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell config commands' commands "$@"
}
(( $+functions[_lys-shell__config__default_commands] )) ||
_lys-shell__config__default_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config default commands' commands "$@"
}
(( $+functions[_lys-shell__config__docs_commands] )) ||
_lys-shell__config__docs_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config docs commands' commands "$@"
}
(( $+functions[_lys-shell__config__get_commands] )) ||
_lys-shell__config__get_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config get commands' commands "$@"
}
(( $+functions[_lys-shell__config__help_commands] )) ||
_lys-shell__config__help_commands() {
    local commands; commands=(
'get:Get the value of a configuration path' \
'set:Set the value of a configuration path' \
'reset:Reset a configuration path to its default value' \
'schema:Output JSON Schema for the configuration (for editor intellisense)' \
'default:Output the default configuration as YAML' \
'docs:Generate markdown reference pages for every registered schema' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell config help commands' commands "$@"
}
(( $+functions[_lys-shell__config__help__default_commands] )) ||
_lys-shell__config__help__default_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config help default commands' commands "$@"
}
(( $+functions[_lys-shell__config__help__docs_commands] )) ||
_lys-shell__config__help__docs_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config help docs commands' commands "$@"
}
(( $+functions[_lys-shell__config__help__get_commands] )) ||
_lys-shell__config__help__get_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config help get commands' commands "$@"
}
(( $+functions[_lys-shell__config__help__help_commands] )) ||
_lys-shell__config__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config help help commands' commands "$@"
}
(( $+functions[_lys-shell__config__help__reset_commands] )) ||
_lys-shell__config__help__reset_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config help reset commands' commands "$@"
}
(( $+functions[_lys-shell__config__help__schema_commands] )) ||
_lys-shell__config__help__schema_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config help schema commands' commands "$@"
}
(( $+functions[_lys-shell__config__help__set_commands] )) ||
_lys-shell__config__help__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config help set commands' commands "$@"
}
(( $+functions[_lys-shell__config__reset_commands] )) ||
_lys-shell__config__reset_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config reset commands' commands "$@"
}
(( $+functions[_lys-shell__config__schema_commands] )) ||
_lys-shell__config__schema_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config schema commands' commands "$@"
}
(( $+functions[_lys-shell__config__set_commands] )) ||
_lys-shell__config__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell config set commands' commands "$@"
}
(( $+functions[_lys-shell__help_commands] )) ||
_lys-shell__help_commands() {
    local commands; commands=(
'audio:Audio control commands' \
'brightness:Set an output'\''s brightness through its backlight or DDC/CI' \
'config:Configuration management commands' \
'icons:Icon management commands' \
'media:Media player control commands' \
'notify:Notification control commands' \
'panel:Panel management commands' \
'power:Power profile commands' \
'systray:System tray commands' \
'wallpaper:Wallpaper control commands' \
'idle:Idle inhibit control commands' \
'launcher:Application launcher / dmenu with rofi-compatible flags' \
'lock:Lock the session via lys-shell'\''s lock screen' \
'logout:Log out of the session through lys-wm or logind' \
'vpn:VPN commands' \
'recorder:Screen recorder control commands' \
'screenshot:Screenshot capture commands' \
'widget:Widget control commands' \
'toast:Show a custom on-screen toast' \
'portal:xdg-desktop-portal backend, screencast picker stub, and dialog previewer' \
'shell:Run the desktop shell in the foreground' \
'completions:Generate shell completions' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell help commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio_commands] )) ||
_lys-shell__help__audio_commands() {
    local commands; commands=(
'output-volume:Get or set output volume level' \
'output-mute:Toggle output mute state' \
'input-volume:Get or set input volume level' \
'input-mute:Toggle input mute state' \
'sinks:List available audio sinks (outputs)' \
'sources:List available audio sources (inputs)' \
'status:Show current audio status' \
    )
    _describe -t commands 'lys-shell help audio commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio__input-mute_commands] )) ||
_lys-shell__help__audio__input-mute_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help audio input-mute commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio__input-volume_commands] )) ||
_lys-shell__help__audio__input-volume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help audio input-volume commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio__output-mute_commands] )) ||
_lys-shell__help__audio__output-mute_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help audio output-mute commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio__output-volume_commands] )) ||
_lys-shell__help__audio__output-volume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help audio output-volume commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio__sinks_commands] )) ||
_lys-shell__help__audio__sinks_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help audio sinks commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio__sources_commands] )) ||
_lys-shell__help__audio__sources_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help audio sources commands' commands "$@"
}
(( $+functions[_lys-shell__help__audio__status_commands] )) ||
_lys-shell__help__audio__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help audio status commands' commands "$@"
}
(( $+functions[_lys-shell__help__brightness_commands] )) ||
_lys-shell__help__brightness_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help brightness commands' commands "$@"
}
(( $+functions[_lys-shell__help__completions_commands] )) ||
_lys-shell__help__completions_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help completions commands' commands "$@"
}
(( $+functions[_lys-shell__help__config_commands] )) ||
_lys-shell__help__config_commands() {
    local commands; commands=(
'get:Get the value of a configuration path' \
'set:Set the value of a configuration path' \
'reset:Reset a configuration path to its default value' \
'schema:Output JSON Schema for the configuration (for editor intellisense)' \
'default:Output the default configuration as YAML' \
'docs:Generate markdown reference pages for every registered schema' \
    )
    _describe -t commands 'lys-shell help config commands' commands "$@"
}
(( $+functions[_lys-shell__help__config__default_commands] )) ||
_lys-shell__help__config__default_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help config default commands' commands "$@"
}
(( $+functions[_lys-shell__help__config__docs_commands] )) ||
_lys-shell__help__config__docs_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help config docs commands' commands "$@"
}
(( $+functions[_lys-shell__help__config__get_commands] )) ||
_lys-shell__help__config__get_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help config get commands' commands "$@"
}
(( $+functions[_lys-shell__help__config__reset_commands] )) ||
_lys-shell__help__config__reset_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help config reset commands' commands "$@"
}
(( $+functions[_lys-shell__help__config__schema_commands] )) ||
_lys-shell__help__config__schema_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help config schema commands' commands "$@"
}
(( $+functions[_lys-shell__help__config__set_commands] )) ||
_lys-shell__help__config__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help config set commands' commands "$@"
}
(( $+functions[_lys-shell__help__help_commands] )) ||
_lys-shell__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help help commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons_commands] )) ||
_lys-shell__help__icons_commands() {
    local commands; commands=(
'setup:Install bundled icons required by lys-shell components' \
'install:Install icons from a CDN source' \
'import:Import local SVG file(s) as icons' \
'remove:Remove installed icons' \
'sources:List available icon sources' \
'list:List installed icons' \
'open:Open the icons directory in file manager' \
'export:Export all installed icons to a directory' \
'sync:Install any icons referenced in config but not yet on disk' \
    )
    _describe -t commands 'lys-shell help icons commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__export_commands] )) ||
_lys-shell__help__icons__export_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons export commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__import_commands] )) ||
_lys-shell__help__icons__import_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons import commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__install_commands] )) ||
_lys-shell__help__icons__install_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons install commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__list_commands] )) ||
_lys-shell__help__icons__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons list commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__open_commands] )) ||
_lys-shell__help__icons__open_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons open commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__remove_commands] )) ||
_lys-shell__help__icons__remove_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons remove commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__setup_commands] )) ||
_lys-shell__help__icons__setup_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons setup commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__sources_commands] )) ||
_lys-shell__help__icons__sources_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons sources commands' commands "$@"
}
(( $+functions[_lys-shell__help__icons__sync_commands] )) ||
_lys-shell__help__icons__sync_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help icons sync commands' commands "$@"
}
(( $+functions[_lys-shell__help__idle_commands] )) ||
_lys-shell__help__idle_commands() {
    local commands; commands=(
'on:Enable idle inhibition' \
'off:Disable idle inhibition' \
'duration:Adjust timer duration (upper limit)' \
'remaining:Adjust remaining time on active timer' \
'status:Show current idle inhibit status' \
'toggle:Toggle idle inhibition on/off' \
    )
    _describe -t commands 'lys-shell help idle commands' commands "$@"
}
(( $+functions[_lys-shell__help__idle__duration_commands] )) ||
_lys-shell__help__idle__duration_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help idle duration commands' commands "$@"
}
(( $+functions[_lys-shell__help__idle__off_commands] )) ||
_lys-shell__help__idle__off_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help idle off commands' commands "$@"
}
(( $+functions[_lys-shell__help__idle__on_commands] )) ||
_lys-shell__help__idle__on_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help idle on commands' commands "$@"
}
(( $+functions[_lys-shell__help__idle__remaining_commands] )) ||
_lys-shell__help__idle__remaining_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help idle remaining commands' commands "$@"
}
(( $+functions[_lys-shell__help__idle__status_commands] )) ||
_lys-shell__help__idle__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help idle status commands' commands "$@"
}
(( $+functions[_lys-shell__help__idle__toggle_commands] )) ||
_lys-shell__help__idle__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help idle toggle commands' commands "$@"
}
(( $+functions[_lys-shell__help__launcher_commands] )) ||
_lys-shell__help__launcher_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help launcher commands' commands "$@"
}
(( $+functions[_lys-shell__help__lock_commands] )) ||
_lys-shell__help__lock_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help lock commands' commands "$@"
}
(( $+functions[_lys-shell__help__logout_commands] )) ||
_lys-shell__help__logout_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help logout commands' commands "$@"
}
(( $+functions[_lys-shell__help__media_commands] )) ||
_lys-shell__help__media_commands() {
    local commands; commands=(
'list:List all available media players' \
'play-pause:Toggle play/pause for a media player' \
'next:Skip to next track' \
'previous:Go to previous track' \
'shuffle:Toggle or set shuffle mode' \
'loop:Set loop/repeat mode' \
'active:Get or set the active media player' \
'info:Display detailed information about a media player' \
    )
    _describe -t commands 'lys-shell help media commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__active_commands] )) ||
_lys-shell__help__media__active_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media active commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__info_commands] )) ||
_lys-shell__help__media__info_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media info commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__list_commands] )) ||
_lys-shell__help__media__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media list commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__loop_commands] )) ||
_lys-shell__help__media__loop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media loop commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__next_commands] )) ||
_lys-shell__help__media__next_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media next commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__play-pause_commands] )) ||
_lys-shell__help__media__play-pause_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media play-pause commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__previous_commands] )) ||
_lys-shell__help__media__previous_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media previous commands' commands "$@"
}
(( $+functions[_lys-shell__help__media__shuffle_commands] )) ||
_lys-shell__help__media__shuffle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help media shuffle commands' commands "$@"
}
(( $+functions[_lys-shell__help__notify_commands] )) ||
_lys-shell__help__notify_commands() {
    local commands; commands=(
'list:List all notifications' \
'dismiss:Dismiss a notification by ID' \
'dismiss-all:Dismiss all notifications' \
'dnd:Toggle Do Not Disturb mode' \
'status:Show notification status' \
    )
    _describe -t commands 'lys-shell help notify commands' commands "$@"
}
(( $+functions[_lys-shell__help__notify__dismiss_commands] )) ||
_lys-shell__help__notify__dismiss_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help notify dismiss commands' commands "$@"
}
(( $+functions[_lys-shell__help__notify__dismiss-all_commands] )) ||
_lys-shell__help__notify__dismiss-all_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help notify dismiss-all commands' commands "$@"
}
(( $+functions[_lys-shell__help__notify__dnd_commands] )) ||
_lys-shell__help__notify__dnd_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help notify dnd commands' commands "$@"
}
(( $+functions[_lys-shell__help__notify__list_commands] )) ||
_lys-shell__help__notify__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help notify list commands' commands "$@"
}
(( $+functions[_lys-shell__help__notify__status_commands] )) ||
_lys-shell__help__notify__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help notify status commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel_commands] )) ||
_lys-shell__help__panel_commands() {
    local commands; commands=(
'start:Start the panel daemon' \
'stop:Stop the panel daemon' \
'restart:Restart the panel daemon' \
'status:Check panel status' \
'settings:Open panel settings' \
'inspect:Open the widget inspector for debugging' \
'hide:Hide the bar on a monitor' \
'show:Show the bar on a monitor' \
'toggle:Toggle bar visibility on a monitor' \
    )
    _describe -t commands 'lys-shell help panel commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__hide_commands] )) ||
_lys-shell__help__panel__hide_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel hide commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__inspect_commands] )) ||
_lys-shell__help__panel__inspect_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel inspect commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__restart_commands] )) ||
_lys-shell__help__panel__restart_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel restart commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__settings_commands] )) ||
_lys-shell__help__panel__settings_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel settings commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__show_commands] )) ||
_lys-shell__help__panel__show_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel show commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__start_commands] )) ||
_lys-shell__help__panel__start_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel start commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__status_commands] )) ||
_lys-shell__help__panel__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel status commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__stop_commands] )) ||
_lys-shell__help__panel__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel stop commands' commands "$@"
}
(( $+functions[_lys-shell__help__panel__toggle_commands] )) ||
_lys-shell__help__panel__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help panel toggle commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal_commands] )) ||
_lys-shell__help__portal_commands() {
    local commands; commands=(
'run:Run the xdg-desktop-portal backend (the default when no subcommand is given, so the installed D-Bus/systemd units can keep execing \`lys-shell portal\`)' \
'share-picker:xdg-desktop-portal-hyprland screencast picker stub (invoked by the portal, not by hand)' \
'show:Preview a portal dialog UI without an application request (developer tool; talks to the running shell over D-Bus, same as the real backend)' \
    )
    _describe -t commands 'lys-shell help portal commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__run_commands] )) ||
_lys-shell__help__portal__run_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal run commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__share-picker_commands] )) ||
_lys-shell__help__portal__share-picker_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal share-picker commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show_commands] )) ||
_lys-shell__help__portal__show_commands() {
    local commands; commands=(
'file-chooser:File open/save dialog (\`org.freedesktop.impl.portal.FileChooser\`)' \
'screenshot:Screenshot capture (\`org.freedesktop.impl.portal.Screenshot\`)' \
'color:Interactive color picker (\`Screenshot.PickColor\`)' \
'print:Printer selection dialog (\`org.freedesktop.impl.portal.Print\`); only the printer/settings prepare step is shown, no document is spooled' \
'screen-cast:Screencast source picker (\`org.freedesktop.impl.portal.ScreenCast\`)' \
'access:Generic grant/deny access prompt (\`org.freedesktop.impl.portal.Access\`)' \
'account:Account info sharing consent (\`org.freedesktop.impl.portal.Account\`)' \
'app-chooser:Application chooser (\`org.freedesktop.impl.portal.AppChooser\`)' \
'dynamic-launcher:Dynamic launcher install confirmation (\`org.freedesktop.impl.portal.DynamicLauncher\`)' \
'authenticate:Administrator authentication dialog (\`dev.stubbe.lys.PortalDialogs1\` \`Authenticate\`, lys-polkit'\''s)' \
'wallpaper:Wallpaper preview confirmation (\`org.freedesktop.impl.portal.Wallpaper\` \`show-preview\`)' \
    )
    _describe -t commands 'lys-shell help portal show commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__access_commands] )) ||
_lys-shell__help__portal__show__access_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show access commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__account_commands] )) ||
_lys-shell__help__portal__show__account_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show account commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__app-chooser_commands] )) ||
_lys-shell__help__portal__show__app-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show app-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__authenticate_commands] )) ||
_lys-shell__help__portal__show__authenticate_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show authenticate commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__color_commands] )) ||
_lys-shell__help__portal__show__color_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show color commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__dynamic-launcher_commands] )) ||
_lys-shell__help__portal__show__dynamic-launcher_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show dynamic-launcher commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__file-chooser_commands] )) ||
_lys-shell__help__portal__show__file-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show file-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__print_commands] )) ||
_lys-shell__help__portal__show__print_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show print commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__screen-cast_commands] )) ||
_lys-shell__help__portal__show__screen-cast_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show screen-cast commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__screenshot_commands] )) ||
_lys-shell__help__portal__show__screenshot_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show screenshot commands' commands "$@"
}
(( $+functions[_lys-shell__help__portal__show__wallpaper_commands] )) ||
_lys-shell__help__portal__show__wallpaper_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help portal show wallpaper commands' commands "$@"
}
(( $+functions[_lys-shell__help__power_commands] )) ||
_lys-shell__help__power_commands() {
    local commands; commands=(
'status:Show current power profile' \
'set:Set power profile' \
'cycle:Cycle to next power profile' \
'list:List available power profiles' \
    )
    _describe -t commands 'lys-shell help power commands' commands "$@"
}
(( $+functions[_lys-shell__help__power__cycle_commands] )) ||
_lys-shell__help__power__cycle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help power cycle commands' commands "$@"
}
(( $+functions[_lys-shell__help__power__list_commands] )) ||
_lys-shell__help__power__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help power list commands' commands "$@"
}
(( $+functions[_lys-shell__help__power__set_commands] )) ||
_lys-shell__help__power__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help power set commands' commands "$@"
}
(( $+functions[_lys-shell__help__power__status_commands] )) ||
_lys-shell__help__power__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help power status commands' commands "$@"
}
(( $+functions[_lys-shell__help__recorder_commands] )) ||
_lys-shell__help__recorder_commands() {
    local commands; commands=(
'start:Start recording' \
'stop:Stop recording' \
'toggle:Toggle recording on/off' \
'pause:Pause the active recording' \
'resume:Resume a paused recording' \
'status:Show current recorder status' \
    )
    _describe -t commands 'lys-shell help recorder commands' commands "$@"
}
(( $+functions[_lys-shell__help__recorder__pause_commands] )) ||
_lys-shell__help__recorder__pause_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help recorder pause commands' commands "$@"
}
(( $+functions[_lys-shell__help__recorder__resume_commands] )) ||
_lys-shell__help__recorder__resume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help recorder resume commands' commands "$@"
}
(( $+functions[_lys-shell__help__recorder__start_commands] )) ||
_lys-shell__help__recorder__start_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help recorder start commands' commands "$@"
}
(( $+functions[_lys-shell__help__recorder__status_commands] )) ||
_lys-shell__help__recorder__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help recorder status commands' commands "$@"
}
(( $+functions[_lys-shell__help__recorder__stop_commands] )) ||
_lys-shell__help__recorder__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help recorder stop commands' commands "$@"
}
(( $+functions[_lys-shell__help__recorder__toggle_commands] )) ||
_lys-shell__help__recorder__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help recorder toggle commands' commands "$@"
}
(( $+functions[_lys-shell__help__screenshot_commands] )) ||
_lys-shell__help__screenshot_commands() {
    local commands; commands=(
'region:Capture a dragged region or a clicked window' \
'output:Capture a whole output (the focused output when NAME is omitted)' \
'window:Capture the active window' \
    )
    _describe -t commands 'lys-shell help screenshot commands' commands "$@"
}
(( $+functions[_lys-shell__help__screenshot__output_commands] )) ||
_lys-shell__help__screenshot__output_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help screenshot output commands' commands "$@"
}
(( $+functions[_lys-shell__help__screenshot__region_commands] )) ||
_lys-shell__help__screenshot__region_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help screenshot region commands' commands "$@"
}
(( $+functions[_lys-shell__help__screenshot__window_commands] )) ||
_lys-shell__help__screenshot__window_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help screenshot window commands' commands "$@"
}
(( $+functions[_lys-shell__help__shell_commands] )) ||
_lys-shell__help__shell_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help shell commands' commands "$@"
}
(( $+functions[_lys-shell__help__systray_commands] )) ||
_lys-shell__help__systray_commands() {
    local commands; commands=(
'list:List all system tray items' \
'activate:Activate a tray item by ID' \
'status:Show system tray status' \
    )
    _describe -t commands 'lys-shell help systray commands' commands "$@"
}
(( $+functions[_lys-shell__help__systray__activate_commands] )) ||
_lys-shell__help__systray__activate_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help systray activate commands' commands "$@"
}
(( $+functions[_lys-shell__help__systray__list_commands] )) ||
_lys-shell__help__systray__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help systray list commands' commands "$@"
}
(( $+functions[_lys-shell__help__systray__status_commands] )) ||
_lys-shell__help__systray__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help systray status commands' commands "$@"
}
(( $+functions[_lys-shell__help__toast_commands] )) ||
_lys-shell__help__toast_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help toast commands' commands "$@"
}
(( $+functions[_lys-shell__help__vpn_commands] )) ||
_lys-shell__help__vpn_commands() {
    local commands; commands=(
'sso-callback:Hand a browser sign-in callback to the running shell' \
    )
    _describe -t commands 'lys-shell help vpn commands' commands "$@"
}
(( $+functions[_lys-shell__help__vpn__sso-callback_commands] )) ||
_lys-shell__help__vpn__sso-callback_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help vpn sso-callback commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper_commands] )) ||
_lys-shell__help__wallpaper_commands() {
    local commands; commands=(
'set:Set wallpaper from an image file' \
'cycle:Start cycling wallpapers from a directory' \
'stop:Stop wallpaper cycling' \
'next:Skip to next wallpaper' \
'previous:Go back to previous wallpaper' \
'info:Display current wallpaper information' \
'theming-monitor:Set which monitor to use for color extraction' \
    )
    _describe -t commands 'lys-shell help wallpaper commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper__cycle_commands] )) ||
_lys-shell__help__wallpaper__cycle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help wallpaper cycle commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper__info_commands] )) ||
_lys-shell__help__wallpaper__info_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help wallpaper info commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper__next_commands] )) ||
_lys-shell__help__wallpaper__next_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help wallpaper next commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper__previous_commands] )) ||
_lys-shell__help__wallpaper__previous_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help wallpaper previous commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper__set_commands] )) ||
_lys-shell__help__wallpaper__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help wallpaper set commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper__stop_commands] )) ||
_lys-shell__help__wallpaper__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help wallpaper stop commands' commands "$@"
}
(( $+functions[_lys-shell__help__wallpaper__theming-monitor_commands] )) ||
_lys-shell__help__wallpaper__theming-monitor_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help wallpaper theming-monitor commands' commands "$@"
}
(( $+functions[_lys-shell__help__widget_commands] )) ||
_lys-shell__help__widget_commands() {
    local commands; commands=(
'update:Push an output update to a bar widget by its config id' \
    )
    _describe -t commands 'lys-shell help widget commands' commands "$@"
}
(( $+functions[_lys-shell__help__widget__update_commands] )) ||
_lys-shell__help__widget__update_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell help widget update commands' commands "$@"
}
(( $+functions[_lys-shell__icons_commands] )) ||
_lys-shell__icons_commands() {
    local commands; commands=(
'setup:Install bundled icons required by lys-shell components' \
'install:Install icons from a CDN source' \
'import:Import local SVG file(s) as icons' \
'remove:Remove installed icons' \
'sources:List available icon sources' \
'list:List installed icons' \
'open:Open the icons directory in file manager' \
'export:Export all installed icons to a directory' \
'sync:Install any icons referenced in config but not yet on disk' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell icons commands' commands "$@"
}
(( $+functions[_lys-shell__icons__export_commands] )) ||
_lys-shell__icons__export_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons export commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help_commands] )) ||
_lys-shell__icons__help_commands() {
    local commands; commands=(
'setup:Install bundled icons required by lys-shell components' \
'install:Install icons from a CDN source' \
'import:Import local SVG file(s) as icons' \
'remove:Remove installed icons' \
'sources:List available icon sources' \
'list:List installed icons' \
'open:Open the icons directory in file manager' \
'export:Export all installed icons to a directory' \
'sync:Install any icons referenced in config but not yet on disk' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell icons help commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__export_commands] )) ||
_lys-shell__icons__help__export_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help export commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__help_commands] )) ||
_lys-shell__icons__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help help commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__import_commands] )) ||
_lys-shell__icons__help__import_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help import commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__install_commands] )) ||
_lys-shell__icons__help__install_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help install commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__list_commands] )) ||
_lys-shell__icons__help__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help list commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__open_commands] )) ||
_lys-shell__icons__help__open_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help open commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__remove_commands] )) ||
_lys-shell__icons__help__remove_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help remove commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__setup_commands] )) ||
_lys-shell__icons__help__setup_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help setup commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__sources_commands] )) ||
_lys-shell__icons__help__sources_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help sources commands' commands "$@"
}
(( $+functions[_lys-shell__icons__help__sync_commands] )) ||
_lys-shell__icons__help__sync_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons help sync commands' commands "$@"
}
(( $+functions[_lys-shell__icons__import_commands] )) ||
_lys-shell__icons__import_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons import commands' commands "$@"
}
(( $+functions[_lys-shell__icons__install_commands] )) ||
_lys-shell__icons__install_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons install commands' commands "$@"
}
(( $+functions[_lys-shell__icons__list_commands] )) ||
_lys-shell__icons__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons list commands' commands "$@"
}
(( $+functions[_lys-shell__icons__open_commands] )) ||
_lys-shell__icons__open_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons open commands' commands "$@"
}
(( $+functions[_lys-shell__icons__remove_commands] )) ||
_lys-shell__icons__remove_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons remove commands' commands "$@"
}
(( $+functions[_lys-shell__icons__setup_commands] )) ||
_lys-shell__icons__setup_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons setup commands' commands "$@"
}
(( $+functions[_lys-shell__icons__sources_commands] )) ||
_lys-shell__icons__sources_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons sources commands' commands "$@"
}
(( $+functions[_lys-shell__icons__sync_commands] )) ||
_lys-shell__icons__sync_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell icons sync commands' commands "$@"
}
(( $+functions[_lys-shell__idle_commands] )) ||
_lys-shell__idle_commands() {
    local commands; commands=(
'on:Enable idle inhibition' \
'off:Disable idle inhibition' \
'duration:Adjust timer duration (upper limit)' \
'remaining:Adjust remaining time on active timer' \
'status:Show current idle inhibit status' \
'toggle:Toggle idle inhibition on/off' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell idle commands' commands "$@"
}
(( $+functions[_lys-shell__idle__duration_commands] )) ||
_lys-shell__idle__duration_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle duration commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help_commands] )) ||
_lys-shell__idle__help_commands() {
    local commands; commands=(
'on:Enable idle inhibition' \
'off:Disable idle inhibition' \
'duration:Adjust timer duration (upper limit)' \
'remaining:Adjust remaining time on active timer' \
'status:Show current idle inhibit status' \
'toggle:Toggle idle inhibition on/off' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell idle help commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help__duration_commands] )) ||
_lys-shell__idle__help__duration_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle help duration commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help__help_commands] )) ||
_lys-shell__idle__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle help help commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help__off_commands] )) ||
_lys-shell__idle__help__off_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle help off commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help__on_commands] )) ||
_lys-shell__idle__help__on_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle help on commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help__remaining_commands] )) ||
_lys-shell__idle__help__remaining_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle help remaining commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help__status_commands] )) ||
_lys-shell__idle__help__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle help status commands' commands "$@"
}
(( $+functions[_lys-shell__idle__help__toggle_commands] )) ||
_lys-shell__idle__help__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle help toggle commands' commands "$@"
}
(( $+functions[_lys-shell__idle__off_commands] )) ||
_lys-shell__idle__off_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle off commands' commands "$@"
}
(( $+functions[_lys-shell__idle__on_commands] )) ||
_lys-shell__idle__on_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle on commands' commands "$@"
}
(( $+functions[_lys-shell__idle__remaining_commands] )) ||
_lys-shell__idle__remaining_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle remaining commands' commands "$@"
}
(( $+functions[_lys-shell__idle__status_commands] )) ||
_lys-shell__idle__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle status commands' commands "$@"
}
(( $+functions[_lys-shell__idle__toggle_commands] )) ||
_lys-shell__idle__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell idle toggle commands' commands "$@"
}
(( $+functions[_lys-shell__launcher_commands] )) ||
_lys-shell__launcher_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell launcher commands' commands "$@"
}
(( $+functions[_lys-shell__lock_commands] )) ||
_lys-shell__lock_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell lock commands' commands "$@"
}
(( $+functions[_lys-shell__logout_commands] )) ||
_lys-shell__logout_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell logout commands' commands "$@"
}
(( $+functions[_lys-shell__media_commands] )) ||
_lys-shell__media_commands() {
    local commands; commands=(
'list:List all available media players' \
'play-pause:Toggle play/pause for a media player' \
'next:Skip to next track' \
'previous:Go to previous track' \
'shuffle:Toggle or set shuffle mode' \
'loop:Set loop/repeat mode' \
'active:Get or set the active media player' \
'info:Display detailed information about a media player' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell media commands' commands "$@"
}
(( $+functions[_lys-shell__media__active_commands] )) ||
_lys-shell__media__active_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media active commands' commands "$@"
}
(( $+functions[_lys-shell__media__help_commands] )) ||
_lys-shell__media__help_commands() {
    local commands; commands=(
'list:List all available media players' \
'play-pause:Toggle play/pause for a media player' \
'next:Skip to next track' \
'previous:Go to previous track' \
'shuffle:Toggle or set shuffle mode' \
'loop:Set loop/repeat mode' \
'active:Get or set the active media player' \
'info:Display detailed information about a media player' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell media help commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__active_commands] )) ||
_lys-shell__media__help__active_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help active commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__help_commands] )) ||
_lys-shell__media__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help help commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__info_commands] )) ||
_lys-shell__media__help__info_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help info commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__list_commands] )) ||
_lys-shell__media__help__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help list commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__loop_commands] )) ||
_lys-shell__media__help__loop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help loop commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__next_commands] )) ||
_lys-shell__media__help__next_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help next commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__play-pause_commands] )) ||
_lys-shell__media__help__play-pause_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help play-pause commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__previous_commands] )) ||
_lys-shell__media__help__previous_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help previous commands' commands "$@"
}
(( $+functions[_lys-shell__media__help__shuffle_commands] )) ||
_lys-shell__media__help__shuffle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media help shuffle commands' commands "$@"
}
(( $+functions[_lys-shell__media__info_commands] )) ||
_lys-shell__media__info_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media info commands' commands "$@"
}
(( $+functions[_lys-shell__media__list_commands] )) ||
_lys-shell__media__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media list commands' commands "$@"
}
(( $+functions[_lys-shell__media__loop_commands] )) ||
_lys-shell__media__loop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media loop commands' commands "$@"
}
(( $+functions[_lys-shell__media__next_commands] )) ||
_lys-shell__media__next_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media next commands' commands "$@"
}
(( $+functions[_lys-shell__media__play-pause_commands] )) ||
_lys-shell__media__play-pause_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media play-pause commands' commands "$@"
}
(( $+functions[_lys-shell__media__previous_commands] )) ||
_lys-shell__media__previous_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media previous commands' commands "$@"
}
(( $+functions[_lys-shell__media__shuffle_commands] )) ||
_lys-shell__media__shuffle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell media shuffle commands' commands "$@"
}
(( $+functions[_lys-shell__notify_commands] )) ||
_lys-shell__notify_commands() {
    local commands; commands=(
'list:List all notifications' \
'dismiss:Dismiss a notification by ID' \
'dismiss-all:Dismiss all notifications' \
'dnd:Toggle Do Not Disturb mode' \
'status:Show notification status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell notify commands' commands "$@"
}
(( $+functions[_lys-shell__notify__dismiss_commands] )) ||
_lys-shell__notify__dismiss_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify dismiss commands' commands "$@"
}
(( $+functions[_lys-shell__notify__dismiss-all_commands] )) ||
_lys-shell__notify__dismiss-all_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify dismiss-all commands' commands "$@"
}
(( $+functions[_lys-shell__notify__dnd_commands] )) ||
_lys-shell__notify__dnd_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify dnd commands' commands "$@"
}
(( $+functions[_lys-shell__notify__help_commands] )) ||
_lys-shell__notify__help_commands() {
    local commands; commands=(
'list:List all notifications' \
'dismiss:Dismiss a notification by ID' \
'dismiss-all:Dismiss all notifications' \
'dnd:Toggle Do Not Disturb mode' \
'status:Show notification status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell notify help commands' commands "$@"
}
(( $+functions[_lys-shell__notify__help__dismiss_commands] )) ||
_lys-shell__notify__help__dismiss_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify help dismiss commands' commands "$@"
}
(( $+functions[_lys-shell__notify__help__dismiss-all_commands] )) ||
_lys-shell__notify__help__dismiss-all_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify help dismiss-all commands' commands "$@"
}
(( $+functions[_lys-shell__notify__help__dnd_commands] )) ||
_lys-shell__notify__help__dnd_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify help dnd commands' commands "$@"
}
(( $+functions[_lys-shell__notify__help__help_commands] )) ||
_lys-shell__notify__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify help help commands' commands "$@"
}
(( $+functions[_lys-shell__notify__help__list_commands] )) ||
_lys-shell__notify__help__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify help list commands' commands "$@"
}
(( $+functions[_lys-shell__notify__help__status_commands] )) ||
_lys-shell__notify__help__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify help status commands' commands "$@"
}
(( $+functions[_lys-shell__notify__list_commands] )) ||
_lys-shell__notify__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify list commands' commands "$@"
}
(( $+functions[_lys-shell__notify__status_commands] )) ||
_lys-shell__notify__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell notify status commands' commands "$@"
}
(( $+functions[_lys-shell__panel_commands] )) ||
_lys-shell__panel_commands() {
    local commands; commands=(
'start:Start the panel daemon' \
'stop:Stop the panel daemon' \
'restart:Restart the panel daemon' \
'status:Check panel status' \
'settings:Open panel settings' \
'inspect:Open the widget inspector for debugging' \
'hide:Hide the bar on a monitor' \
'show:Show the bar on a monitor' \
'toggle:Toggle bar visibility on a monitor' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell panel commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help_commands] )) ||
_lys-shell__panel__help_commands() {
    local commands; commands=(
'start:Start the panel daemon' \
'stop:Stop the panel daemon' \
'restart:Restart the panel daemon' \
'status:Check panel status' \
'settings:Open panel settings' \
'inspect:Open the widget inspector for debugging' \
'hide:Hide the bar on a monitor' \
'show:Show the bar on a monitor' \
'toggle:Toggle bar visibility on a monitor' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell panel help commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__help_commands] )) ||
_lys-shell__panel__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help help commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__hide_commands] )) ||
_lys-shell__panel__help__hide_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help hide commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__inspect_commands] )) ||
_lys-shell__panel__help__inspect_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help inspect commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__restart_commands] )) ||
_lys-shell__panel__help__restart_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help restart commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__settings_commands] )) ||
_lys-shell__panel__help__settings_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help settings commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__show_commands] )) ||
_lys-shell__panel__help__show_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help show commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__start_commands] )) ||
_lys-shell__panel__help__start_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help start commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__status_commands] )) ||
_lys-shell__panel__help__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help status commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__stop_commands] )) ||
_lys-shell__panel__help__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help stop commands' commands "$@"
}
(( $+functions[_lys-shell__panel__help__toggle_commands] )) ||
_lys-shell__panel__help__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel help toggle commands' commands "$@"
}
(( $+functions[_lys-shell__panel__hide_commands] )) ||
_lys-shell__panel__hide_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel hide commands' commands "$@"
}
(( $+functions[_lys-shell__panel__inspect_commands] )) ||
_lys-shell__panel__inspect_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel inspect commands' commands "$@"
}
(( $+functions[_lys-shell__panel__restart_commands] )) ||
_lys-shell__panel__restart_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel restart commands' commands "$@"
}
(( $+functions[_lys-shell__panel__settings_commands] )) ||
_lys-shell__panel__settings_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel settings commands' commands "$@"
}
(( $+functions[_lys-shell__panel__show_commands] )) ||
_lys-shell__panel__show_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel show commands' commands "$@"
}
(( $+functions[_lys-shell__panel__start_commands] )) ||
_lys-shell__panel__start_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel start commands' commands "$@"
}
(( $+functions[_lys-shell__panel__status_commands] )) ||
_lys-shell__panel__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel status commands' commands "$@"
}
(( $+functions[_lys-shell__panel__stop_commands] )) ||
_lys-shell__panel__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel stop commands' commands "$@"
}
(( $+functions[_lys-shell__panel__toggle_commands] )) ||
_lys-shell__panel__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell panel toggle commands' commands "$@"
}
(( $+functions[_lys-shell__portal_commands] )) ||
_lys-shell__portal_commands() {
    local commands; commands=(
'run:Run the xdg-desktop-portal backend (the default when no subcommand is given, so the installed D-Bus/systemd units can keep execing \`lys-shell portal\`)' \
'share-picker:xdg-desktop-portal-hyprland screencast picker stub (invoked by the portal, not by hand)' \
'show:Preview a portal dialog UI without an application request (developer tool; talks to the running shell over D-Bus, same as the real backend)' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell portal commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help_commands] )) ||
_lys-shell__portal__help_commands() {
    local commands; commands=(
'run:Run the xdg-desktop-portal backend (the default when no subcommand is given, so the installed D-Bus/systemd units can keep execing \`lys-shell portal\`)' \
'share-picker:xdg-desktop-portal-hyprland screencast picker stub (invoked by the portal, not by hand)' \
'show:Preview a portal dialog UI without an application request (developer tool; talks to the running shell over D-Bus, same as the real backend)' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell portal help commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__help_commands] )) ||
_lys-shell__portal__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help help commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__run_commands] )) ||
_lys-shell__portal__help__run_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help run commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__share-picker_commands] )) ||
_lys-shell__portal__help__share-picker_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help share-picker commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show_commands] )) ||
_lys-shell__portal__help__show_commands() {
    local commands; commands=(
'file-chooser:File open/save dialog (\`org.freedesktop.impl.portal.FileChooser\`)' \
'screenshot:Screenshot capture (\`org.freedesktop.impl.portal.Screenshot\`)' \
'color:Interactive color picker (\`Screenshot.PickColor\`)' \
'print:Printer selection dialog (\`org.freedesktop.impl.portal.Print\`); only the printer/settings prepare step is shown, no document is spooled' \
'screen-cast:Screencast source picker (\`org.freedesktop.impl.portal.ScreenCast\`)' \
'access:Generic grant/deny access prompt (\`org.freedesktop.impl.portal.Access\`)' \
'account:Account info sharing consent (\`org.freedesktop.impl.portal.Account\`)' \
'app-chooser:Application chooser (\`org.freedesktop.impl.portal.AppChooser\`)' \
'dynamic-launcher:Dynamic launcher install confirmation (\`org.freedesktop.impl.portal.DynamicLauncher\`)' \
'authenticate:Administrator authentication dialog (\`dev.stubbe.lys.PortalDialogs1\` \`Authenticate\`, lys-polkit'\''s)' \
'wallpaper:Wallpaper preview confirmation (\`org.freedesktop.impl.portal.Wallpaper\` \`show-preview\`)' \
    )
    _describe -t commands 'lys-shell portal help show commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__access_commands] )) ||
_lys-shell__portal__help__show__access_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show access commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__account_commands] )) ||
_lys-shell__portal__help__show__account_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show account commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__app-chooser_commands] )) ||
_lys-shell__portal__help__show__app-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show app-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__authenticate_commands] )) ||
_lys-shell__portal__help__show__authenticate_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show authenticate commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__color_commands] )) ||
_lys-shell__portal__help__show__color_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show color commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__dynamic-launcher_commands] )) ||
_lys-shell__portal__help__show__dynamic-launcher_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show dynamic-launcher commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__file-chooser_commands] )) ||
_lys-shell__portal__help__show__file-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show file-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__print_commands] )) ||
_lys-shell__portal__help__show__print_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show print commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__screen-cast_commands] )) ||
_lys-shell__portal__help__show__screen-cast_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show screen-cast commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__screenshot_commands] )) ||
_lys-shell__portal__help__show__screenshot_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show screenshot commands' commands "$@"
}
(( $+functions[_lys-shell__portal__help__show__wallpaper_commands] )) ||
_lys-shell__portal__help__show__wallpaper_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal help show wallpaper commands' commands "$@"
}
(( $+functions[_lys-shell__portal__run_commands] )) ||
_lys-shell__portal__run_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal run commands' commands "$@"
}
(( $+functions[_lys-shell__portal__share-picker_commands] )) ||
_lys-shell__portal__share-picker_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal share-picker commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show_commands] )) ||
_lys-shell__portal__show_commands() {
    local commands; commands=(
'file-chooser:File open/save dialog (\`org.freedesktop.impl.portal.FileChooser\`)' \
'screenshot:Screenshot capture (\`org.freedesktop.impl.portal.Screenshot\`)' \
'color:Interactive color picker (\`Screenshot.PickColor\`)' \
'print:Printer selection dialog (\`org.freedesktop.impl.portal.Print\`); only the printer/settings prepare step is shown, no document is spooled' \
'screen-cast:Screencast source picker (\`org.freedesktop.impl.portal.ScreenCast\`)' \
'access:Generic grant/deny access prompt (\`org.freedesktop.impl.portal.Access\`)' \
'account:Account info sharing consent (\`org.freedesktop.impl.portal.Account\`)' \
'app-chooser:Application chooser (\`org.freedesktop.impl.portal.AppChooser\`)' \
'dynamic-launcher:Dynamic launcher install confirmation (\`org.freedesktop.impl.portal.DynamicLauncher\`)' \
'authenticate:Administrator authentication dialog (\`dev.stubbe.lys.PortalDialogs1\` \`Authenticate\`, lys-polkit'\''s)' \
'wallpaper:Wallpaper preview confirmation (\`org.freedesktop.impl.portal.Wallpaper\` \`show-preview\`)' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell portal show commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__access_commands] )) ||
_lys-shell__portal__show__access_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show access commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__account_commands] )) ||
_lys-shell__portal__show__account_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show account commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__app-chooser_commands] )) ||
_lys-shell__portal__show__app-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show app-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__authenticate_commands] )) ||
_lys-shell__portal__show__authenticate_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show authenticate commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__color_commands] )) ||
_lys-shell__portal__show__color_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show color commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__dynamic-launcher_commands] )) ||
_lys-shell__portal__show__dynamic-launcher_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show dynamic-launcher commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__file-chooser_commands] )) ||
_lys-shell__portal__show__file-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show file-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help_commands] )) ||
_lys-shell__portal__show__help_commands() {
    local commands; commands=(
'file-chooser:File open/save dialog (\`org.freedesktop.impl.portal.FileChooser\`)' \
'screenshot:Screenshot capture (\`org.freedesktop.impl.portal.Screenshot\`)' \
'color:Interactive color picker (\`Screenshot.PickColor\`)' \
'print:Printer selection dialog (\`org.freedesktop.impl.portal.Print\`); only the printer/settings prepare step is shown, no document is spooled' \
'screen-cast:Screencast source picker (\`org.freedesktop.impl.portal.ScreenCast\`)' \
'access:Generic grant/deny access prompt (\`org.freedesktop.impl.portal.Access\`)' \
'account:Account info sharing consent (\`org.freedesktop.impl.portal.Account\`)' \
'app-chooser:Application chooser (\`org.freedesktop.impl.portal.AppChooser\`)' \
'dynamic-launcher:Dynamic launcher install confirmation (\`org.freedesktop.impl.portal.DynamicLauncher\`)' \
'authenticate:Administrator authentication dialog (\`dev.stubbe.lys.PortalDialogs1\` \`Authenticate\`, lys-polkit'\''s)' \
'wallpaper:Wallpaper preview confirmation (\`org.freedesktop.impl.portal.Wallpaper\` \`show-preview\`)' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell portal show help commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__access_commands] )) ||
_lys-shell__portal__show__help__access_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help access commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__account_commands] )) ||
_lys-shell__portal__show__help__account_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help account commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__app-chooser_commands] )) ||
_lys-shell__portal__show__help__app-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help app-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__authenticate_commands] )) ||
_lys-shell__portal__show__help__authenticate_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help authenticate commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__color_commands] )) ||
_lys-shell__portal__show__help__color_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help color commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__dynamic-launcher_commands] )) ||
_lys-shell__portal__show__help__dynamic-launcher_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help dynamic-launcher commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__file-chooser_commands] )) ||
_lys-shell__portal__show__help__file-chooser_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help file-chooser commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__help_commands] )) ||
_lys-shell__portal__show__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help help commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__print_commands] )) ||
_lys-shell__portal__show__help__print_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help print commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__screen-cast_commands] )) ||
_lys-shell__portal__show__help__screen-cast_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help screen-cast commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__screenshot_commands] )) ||
_lys-shell__portal__show__help__screenshot_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help screenshot commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__help__wallpaper_commands] )) ||
_lys-shell__portal__show__help__wallpaper_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show help wallpaper commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__print_commands] )) ||
_lys-shell__portal__show__print_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show print commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__screen-cast_commands] )) ||
_lys-shell__portal__show__screen-cast_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show screen-cast commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__screenshot_commands] )) ||
_lys-shell__portal__show__screenshot_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show screenshot commands' commands "$@"
}
(( $+functions[_lys-shell__portal__show__wallpaper_commands] )) ||
_lys-shell__portal__show__wallpaper_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell portal show wallpaper commands' commands "$@"
}
(( $+functions[_lys-shell__power_commands] )) ||
_lys-shell__power_commands() {
    local commands; commands=(
'status:Show current power profile' \
'set:Set power profile' \
'cycle:Cycle to next power profile' \
'list:List available power profiles' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell power commands' commands "$@"
}
(( $+functions[_lys-shell__power__cycle_commands] )) ||
_lys-shell__power__cycle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power cycle commands' commands "$@"
}
(( $+functions[_lys-shell__power__help_commands] )) ||
_lys-shell__power__help_commands() {
    local commands; commands=(
'status:Show current power profile' \
'set:Set power profile' \
'cycle:Cycle to next power profile' \
'list:List available power profiles' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell power help commands' commands "$@"
}
(( $+functions[_lys-shell__power__help__cycle_commands] )) ||
_lys-shell__power__help__cycle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power help cycle commands' commands "$@"
}
(( $+functions[_lys-shell__power__help__help_commands] )) ||
_lys-shell__power__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power help help commands' commands "$@"
}
(( $+functions[_lys-shell__power__help__list_commands] )) ||
_lys-shell__power__help__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power help list commands' commands "$@"
}
(( $+functions[_lys-shell__power__help__set_commands] )) ||
_lys-shell__power__help__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power help set commands' commands "$@"
}
(( $+functions[_lys-shell__power__help__status_commands] )) ||
_lys-shell__power__help__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power help status commands' commands "$@"
}
(( $+functions[_lys-shell__power__list_commands] )) ||
_lys-shell__power__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power list commands' commands "$@"
}
(( $+functions[_lys-shell__power__set_commands] )) ||
_lys-shell__power__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power set commands' commands "$@"
}
(( $+functions[_lys-shell__power__status_commands] )) ||
_lys-shell__power__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell power status commands' commands "$@"
}
(( $+functions[_lys-shell__recorder_commands] )) ||
_lys-shell__recorder_commands() {
    local commands; commands=(
'start:Start recording' \
'stop:Stop recording' \
'toggle:Toggle recording on/off' \
'pause:Pause the active recording' \
'resume:Resume a paused recording' \
'status:Show current recorder status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell recorder commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help_commands] )) ||
_lys-shell__recorder__help_commands() {
    local commands; commands=(
'start:Start recording' \
'stop:Stop recording' \
'toggle:Toggle recording on/off' \
'pause:Pause the active recording' \
'resume:Resume a paused recording' \
'status:Show current recorder status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell recorder help commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help__help_commands] )) ||
_lys-shell__recorder__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder help help commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help__pause_commands] )) ||
_lys-shell__recorder__help__pause_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder help pause commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help__resume_commands] )) ||
_lys-shell__recorder__help__resume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder help resume commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help__start_commands] )) ||
_lys-shell__recorder__help__start_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder help start commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help__status_commands] )) ||
_lys-shell__recorder__help__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder help status commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help__stop_commands] )) ||
_lys-shell__recorder__help__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder help stop commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__help__toggle_commands] )) ||
_lys-shell__recorder__help__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder help toggle commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__pause_commands] )) ||
_lys-shell__recorder__pause_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder pause commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__resume_commands] )) ||
_lys-shell__recorder__resume_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder resume commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__start_commands] )) ||
_lys-shell__recorder__start_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder start commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__status_commands] )) ||
_lys-shell__recorder__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder status commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__stop_commands] )) ||
_lys-shell__recorder__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder stop commands' commands "$@"
}
(( $+functions[_lys-shell__recorder__toggle_commands] )) ||
_lys-shell__recorder__toggle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell recorder toggle commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot_commands] )) ||
_lys-shell__screenshot_commands() {
    local commands; commands=(
'region:Capture a dragged region or a clicked window' \
'output:Capture a whole output (the focused output when NAME is omitted)' \
'window:Capture the active window' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell screenshot commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__help_commands] )) ||
_lys-shell__screenshot__help_commands() {
    local commands; commands=(
'region:Capture a dragged region or a clicked window' \
'output:Capture a whole output (the focused output when NAME is omitted)' \
'window:Capture the active window' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell screenshot help commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__help__help_commands] )) ||
_lys-shell__screenshot__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell screenshot help help commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__help__output_commands] )) ||
_lys-shell__screenshot__help__output_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell screenshot help output commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__help__region_commands] )) ||
_lys-shell__screenshot__help__region_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell screenshot help region commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__help__window_commands] )) ||
_lys-shell__screenshot__help__window_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell screenshot help window commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__output_commands] )) ||
_lys-shell__screenshot__output_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell screenshot output commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__region_commands] )) ||
_lys-shell__screenshot__region_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell screenshot region commands' commands "$@"
}
(( $+functions[_lys-shell__screenshot__window_commands] )) ||
_lys-shell__screenshot__window_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell screenshot window commands' commands "$@"
}
(( $+functions[_lys-shell__shell_commands] )) ||
_lys-shell__shell_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell shell commands' commands "$@"
}
(( $+functions[_lys-shell__systray_commands] )) ||
_lys-shell__systray_commands() {
    local commands; commands=(
'list:List all system tray items' \
'activate:Activate a tray item by ID' \
'status:Show system tray status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell systray commands' commands "$@"
}
(( $+functions[_lys-shell__systray__activate_commands] )) ||
_lys-shell__systray__activate_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell systray activate commands' commands "$@"
}
(( $+functions[_lys-shell__systray__help_commands] )) ||
_lys-shell__systray__help_commands() {
    local commands; commands=(
'list:List all system tray items' \
'activate:Activate a tray item by ID' \
'status:Show system tray status' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell systray help commands' commands "$@"
}
(( $+functions[_lys-shell__systray__help__activate_commands] )) ||
_lys-shell__systray__help__activate_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell systray help activate commands' commands "$@"
}
(( $+functions[_lys-shell__systray__help__help_commands] )) ||
_lys-shell__systray__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell systray help help commands' commands "$@"
}
(( $+functions[_lys-shell__systray__help__list_commands] )) ||
_lys-shell__systray__help__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell systray help list commands' commands "$@"
}
(( $+functions[_lys-shell__systray__help__status_commands] )) ||
_lys-shell__systray__help__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell systray help status commands' commands "$@"
}
(( $+functions[_lys-shell__systray__list_commands] )) ||
_lys-shell__systray__list_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell systray list commands' commands "$@"
}
(( $+functions[_lys-shell__systray__status_commands] )) ||
_lys-shell__systray__status_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell systray status commands' commands "$@"
}
(( $+functions[_lys-shell__toast_commands] )) ||
_lys-shell__toast_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell toast commands' commands "$@"
}
(( $+functions[_lys-shell__vpn_commands] )) ||
_lys-shell__vpn_commands() {
    local commands; commands=(
'sso-callback:Hand a browser sign-in callback to the running shell' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell vpn commands' commands "$@"
}
(( $+functions[_lys-shell__vpn__help_commands] )) ||
_lys-shell__vpn__help_commands() {
    local commands; commands=(
'sso-callback:Hand a browser sign-in callback to the running shell' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell vpn help commands' commands "$@"
}
(( $+functions[_lys-shell__vpn__help__help_commands] )) ||
_lys-shell__vpn__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell vpn help help commands' commands "$@"
}
(( $+functions[_lys-shell__vpn__help__sso-callback_commands] )) ||
_lys-shell__vpn__help__sso-callback_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell vpn help sso-callback commands' commands "$@"
}
(( $+functions[_lys-shell__vpn__sso-callback_commands] )) ||
_lys-shell__vpn__sso-callback_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell vpn sso-callback commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper_commands] )) ||
_lys-shell__wallpaper_commands() {
    local commands; commands=(
'set:Set wallpaper from an image file' \
'cycle:Start cycling wallpapers from a directory' \
'stop:Stop wallpaper cycling' \
'next:Skip to next wallpaper' \
'previous:Go back to previous wallpaper' \
'info:Display current wallpaper information' \
'theming-monitor:Set which monitor to use for color extraction' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell wallpaper commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__cycle_commands] )) ||
_lys-shell__wallpaper__cycle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper cycle commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help_commands] )) ||
_lys-shell__wallpaper__help_commands() {
    local commands; commands=(
'set:Set wallpaper from an image file' \
'cycle:Start cycling wallpapers from a directory' \
'stop:Stop wallpaper cycling' \
'next:Skip to next wallpaper' \
'previous:Go back to previous wallpaper' \
'info:Display current wallpaper information' \
'theming-monitor:Set which monitor to use for color extraction' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell wallpaper help commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__cycle_commands] )) ||
_lys-shell__wallpaper__help__cycle_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help cycle commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__help_commands] )) ||
_lys-shell__wallpaper__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help help commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__info_commands] )) ||
_lys-shell__wallpaper__help__info_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help info commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__next_commands] )) ||
_lys-shell__wallpaper__help__next_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help next commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__previous_commands] )) ||
_lys-shell__wallpaper__help__previous_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help previous commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__set_commands] )) ||
_lys-shell__wallpaper__help__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help set commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__stop_commands] )) ||
_lys-shell__wallpaper__help__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help stop commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__help__theming-monitor_commands] )) ||
_lys-shell__wallpaper__help__theming-monitor_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper help theming-monitor commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__info_commands] )) ||
_lys-shell__wallpaper__info_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper info commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__next_commands] )) ||
_lys-shell__wallpaper__next_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper next commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__previous_commands] )) ||
_lys-shell__wallpaper__previous_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper previous commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__set_commands] )) ||
_lys-shell__wallpaper__set_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper set commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__stop_commands] )) ||
_lys-shell__wallpaper__stop_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper stop commands' commands "$@"
}
(( $+functions[_lys-shell__wallpaper__theming-monitor_commands] )) ||
_lys-shell__wallpaper__theming-monitor_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell wallpaper theming-monitor commands' commands "$@"
}
(( $+functions[_lys-shell__widget_commands] )) ||
_lys-shell__widget_commands() {
    local commands; commands=(
'update:Push an output update to a bar widget by its config id' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell widget commands' commands "$@"
}
(( $+functions[_lys-shell__widget__help_commands] )) ||
_lys-shell__widget__help_commands() {
    local commands; commands=(
'update:Push an output update to a bar widget by its config id' \
'help:Print this message or the help of the given subcommand(s)' \
    )
    _describe -t commands 'lys-shell widget help commands' commands "$@"
}
(( $+functions[_lys-shell__widget__help__help_commands] )) ||
_lys-shell__widget__help__help_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell widget help help commands' commands "$@"
}
(( $+functions[_lys-shell__widget__help__update_commands] )) ||
_lys-shell__widget__help__update_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell widget help update commands' commands "$@"
}
(( $+functions[_lys-shell__widget__update_commands] )) ||
_lys-shell__widget__update_commands() {
    local commands; commands=()
    _describe -t commands 'lys-shell widget update commands' commands "$@"
}

if [ "$funcstack[1]" = "_lys-shell" ]; then
    _lys-shell "$@"
else
    compdef _lys-shell lys-shell
fi
