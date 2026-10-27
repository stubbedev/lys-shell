module github.com/stubbedev/lys-shell

go 1.27.3

require (
	github.com/ebitengine/purego v0.11.2
	github.com/godbus/dbus/v5 v5.2.4
	github.com/rivo/uniseg v0.4.7
	github.com/stubbedev/lys-lib v0.1.1-0.20261027092510-2236ab4c9fa6
	github.com/stubbedev/lys-ui v0.0.0-20261027092646-06642b92ce57
	github.com/stubbedev/memory-go v0.1.1-0.20261027092317-8b116b9fa590
	github.com/unxed/xkb-go v0.1.9
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.58.0
	golang.org/x/image v0.47.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	gonum.org/v1/gonum v0.17.0
	modernc.org/sqlite v1.61.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jfreymuth/pulse v0.1.3 // indirect
	github.com/kettek/apng v0.0.0-20250827064933-2bb5f5fcf253 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	golang.org/x/net v0.61.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

// The godbus fd-array decode fix (third_party/godbus-dbus/LYS_SHELL-PATCHES.md).
replace github.com/godbus/dbus/v5 => ./third_party/godbus-dbus
