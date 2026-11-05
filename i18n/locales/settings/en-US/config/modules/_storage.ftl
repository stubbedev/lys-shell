### lys-shell Configuration - Storage Module

## Storage Module Configuration

settings-modules-storage-poll-interval-ms = Poll Interval
    .description = Polling interval in milliseconds

settings-modules-storage-mount-point = Mount Point(s)
    .description = Mount point(s) to monitor (e.g., "/" or ["/", "/mnt/drive1"])

settings-modules-storage-format = Display Format
    .description = Format string with placeholders: {"{{ percent }}"}, {"{{ used_tib }}"}, {"{{ used_gib }}"}, {"{{ used_mib }}"}, {"{{ used_auto }}"}, {"{{ total_tib }}"}, {"{{ total_gib }}"}, {"{{ total_mib }}"}, {"{{ total_auto }}"}, {"{{ free_tib }}"}, {"{{ free_gib }}"}, {"{{ free_mib }}"}, {"{{ free_auto }}"}, {"{{ filesystem }}"}

settings-modules-storage-icon-name = Icon Name
    .description = Symbolic icon name

settings-modules-storage-border-show = Show Border
    .description = Display border around button

settings-modules-storage-border-color = Border Color
    .description = Border color token

settings-modules-storage-icon-show = Show Icon
    .description = Display module icon

settings-modules-storage-icon-color = Icon Color
    .description = Icon foreground color

settings-modules-storage-icon-bg-color = Icon Background
    .description = Icon container background color

settings-modules-storage-label-show = Show Label
    .description = Display text label

settings-modules-storage-label-color = Label Color
    .description = Label text color

settings-modules-storage-label-max-length = Label Max Length
    .description = Max characters before truncation

settings-modules-storage-button-bg-color = Button Background
    .description = Button background color

settings-modules-storage-left-click = Left Click
    .description = Shell command on left click

settings-modules-storage-right-click = Right Click
    .description = Shell command on right click

settings-modules-storage-middle-click = Middle Click
    .description = Shell command on middle click

settings-modules-storage-scroll-up = Scroll Up
    .description = Shell command on scroll up

settings-modules-storage-scroll-down = Scroll Down
    .description = Shell command on scroll down

settings-modules-storage-thresholds = Thresholds
    .description = Color thresholds that change based on value levels

## Format placeholders

settings-modules-storage-format-placeholder-percent = Disk usage (0-100)

settings-modules-storage-format-placeholder-used-tib = Used space in TiB

settings-modules-storage-format-placeholder-used-gib = Used space in GiB

settings-modules-storage-format-placeholder-used-mib = Used space in MiB

settings-modules-storage-format-placeholder-used-auto = Used space with its unit (e.g. 128.5 GiB)

settings-modules-storage-format-placeholder-total-tib = Total space in TiB

settings-modules-storage-format-placeholder-total-gib = Total space in GiB

settings-modules-storage-format-placeholder-total-mib = Total space in MiB

settings-modules-storage-format-placeholder-total-auto = Total space with its unit

settings-modules-storage-format-placeholder-free-tib = Free space in TiB

settings-modules-storage-format-placeholder-free-gib = Free space in GiB

settings-modules-storage-format-placeholder-free-mib = Free space in MiB

settings-modules-storage-format-placeholder-free-auto = Free space with its unit

settings-modules-storage-format-placeholder-filesystem = Filesystem type (e.g. ext4, btrfs)
