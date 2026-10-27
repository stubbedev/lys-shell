### lys-shell Configuration - Night Light Module

## Night Light Module Configuration

settings-modules-nightlight-temperature = Temperature
    .description = Color temperature in Kelvin when filter is enabled (1000-20000)

settings-modules-nightlight-gamma = Gamma
    .description = Display gamma percentage when filter is enabled (0-200)

settings-modules-nightlight-auto-schedule = Auto Schedule
    .description = Automatically enable at night and disable during the day, based on local sunrise/sunset. Location is detected via GeoClue, falling back to the coordinates below

settings-modules-nightlight-latitude = Latitude
    .description = Fallback latitude in decimal degrees (north positive, -90 to 90), used when GeoClue is unavailable

settings-modules-nightlight-longitude = Longitude
    .description = Fallback longitude in decimal degrees (east positive, -180 to 180), used when GeoClue is unavailable

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
