### lys-shell Configuration - Night Light Module

## Night Light Module Configuration

settings-modules-nightlight-temperature = Temperature
    .description = Color temperature in Kelvin when filter is enabled (1000-20000)

settings-modules-nightlight-gamma = Gamma
    .description = Display gamma percentage when filter is enabled (0-200)

settings-modules-nightlight-auto-schedule = Auto Schedule
    .description = Ease the filter on at dusk and off at dawn. Follows civil dawn and dusk at the location GeoClue reports, or at the coordinates below when both are set

settings-modules-nightlight-transition-duration = Transition
    .description = Length of the transition between day and night in minutes, 0 for an instant switch (0-120)

settings-modules-nightlight-latitude = Latitude
    .description = Latitude in decimal degrees (north positive, -90 to 90). Set with the longitude, it overrides GeoClue

settings-modules-nightlight-longitude = Longitude
    .description = Longitude in decimal degrees (east positive, -180 to 180). Set with the latitude, it overrides GeoClue

settings-modules-nightlight-icon-off = Icon Off
    .description = Icon when filter is disabled

settings-modules-nightlight-icon-on = Icon On
    .description = Icon when filter is enabled

settings-modules-nightlight-format = Format
    .description = Label format string. Placeholders: {"{{ status }}"}, {"{{ temp }}"}, {"{{ gamma }}"}, {"{{ config_temp }}"}, {"{{ config_gamma }}"}

settings-modules-nightlight-border-show = Show Border
    .description = Display border around button

settings-modules-nightlight-border-color = Border Color
    .description = Border color token

settings-modules-nightlight-icon-show = Show Icon
    .description = Display module icon

settings-modules-nightlight-tooltip = Show Tooltip
    .description = Show the module's state in a tooltip on hover

settings-modules-nightlight-icon-color = Icon Color
    .description = Icon foreground color

settings-modules-nightlight-icon-bg-color = Icon Background
    .description = Icon container background color

settings-modules-nightlight-label-show = Show Label
    .description = Display label

settings-modules-nightlight-label-color = Label Color
    .description = Label text color

settings-modules-nightlight-label-max-length = Label Max Length
    .description = Max characters before truncation

settings-modules-nightlight-button-bg-color = Button Background
    .description = Button background color

settings-modules-nightlight-left-click = Left Click
    .description = Action on left click. Use `:toggle` for built-in on/off, empty for no action, or a shell command

settings-modules-nightlight-right-click = Right Click
    .description = Shell command on right click

settings-modules-nightlight-middle-click = Middle Click
    .description = Shell command on middle click

settings-modules-nightlight-scroll-up = Scroll Up
    .description = Shell command on scroll up

settings-modules-nightlight-scroll-down = Scroll Down
    .description = Shell command on scroll down

## Format placeholders

settings-modules-nightlight-format-placeholder-status = Filter status (On, Off)

settings-modules-nightlight-format-placeholder-temp = Current temperature in Kelvin (-- while off)

settings-modules-nightlight-format-placeholder-gamma = Current gamma percentage (-- while off)

settings-modules-nightlight-format-placeholder-config-temp = Configured temperature in Kelvin

settings-modules-nightlight-format-placeholder-config-gamma = Configured gamma percentage
