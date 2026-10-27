### lys-shell Configuration - Login Screen

## Login screen: the greeter and the lock screen

settings-login-background-mode = Background
    .description = How the login screen background is drawn: solid color, an image, or the wallpaper

settings-login-background-image = Background Image
    .description = Image file shown behind the login screen when background is set to "image"

settings-login-background-color = Background Color
    .description = Fill color shown behind the login screen when background is set to "color"

settings-login-blur = Blur
    .description = Gaussian blur radius applied to image/wallpaper backgrounds (0 = none)

settings-login-show-clock = Show Clock
    .description = Display a clock above the credential prompt

settings-login-clock-format = Clock Format
    .description = strftime format for the login-screen time (e.g. %H:%M)

settings-login-date-format = Date Format
    .description = strftime format for the login-screen date (e.g. %A, %B %-d)

settings-login-max-attempts = Max Attempts
    .description = Failed password attempts before input is blocked (0 = unlimited). Screen stays locked.

settings-login-show-failed-attempts = Show Failed Attempts
    .description = Show the failed-attempt count on the login screen

settings-login-lock-enabled = Enabled
    .description = Let lys-shell lock the session (responds to loginctl lock-session and `lys-shell lock`)

settings-login-lock-lock-on-start = Lock on Start
    .description = Lock the session as soon as the shell starts, for autologin setups where the lock screen is the access gate

settings-login-lock-grace-period-ms = Grace Period
    .description = Window after locking during which the screen unlocks without a password (ms, 0 = always require it)

settings-login-lock-blank-timeout-ms = Blank Timeout
    .description = Black out the lock screen after this idle time (ms, 0 = never; visual only — DPMS is the idle daemon's job)

settings-login-lock-pam-service = PAM Service
    .description = PAM service used to verify the password (e.g. system-auth, login)

settings-login-greeter-show-user-list = Show User List
    .description = Show clickable avatars for the machine's login users above the username field

settings-login-greeter-show-power-buttons = Show Power Buttons
    .description = Show the shutdown/reboot buttons at the bottom of the login screen

settings-login-greeter-cursor-theme = Cursor Theme
    .description = Xcursor theme used on the login screen (empty = system default)

settings-login-greeter-cursor-size = Cursor Size
    .description = Logical cursor size; scaled per display so HiDPI outputs get a sharp cursor

## LockBackground variants
enum-lock-background-color = Color
enum-lock-background-image = Image
enum-lock-background-wallpaper = Wallpaper
