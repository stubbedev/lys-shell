package config

// ScreenshotConfig is ported from crates/wayle-config/src/schemas/modules/screenshot/mod.rs.
//
// Screenshot capture button.
//
// Click the bar button to capture a region, output, or window. Controllable
// from the CLI / RPC socket: `lys-shell screenshot region|output|window`.
//
// After a capture a thumbnail floats in a screen corner: click it to mark
// the image up (crop, arrow, rectangle, ellipse, freehand, text,
// highlighter, numbered markers and pixelation, with undo and redo; Enter
// saves over the file and copies it, Ctrl+S saves a copy elsewhere), drag
// it into an app, or delete the capture with its close button.
//
// `lys-shell screenshot text` copies the text in a dragged region (through
// tesseract, which must be installed) and `lys-shell screenshot qr` decodes
// the QR code in one; neither saves a file.
type ScreenshotConfig struct {
	// Bar button icon.
	Icon string `cfg:"icon"`
	// Output directory for screenshots. Empty uses the XDG Pictures directory.
	OutputDirectory string `cfg:"output-directory,type=Directory"`
	// Saved file name, formatted with `chrono`/`strftime` specifiers.
	FilenameFormat StrftimeFormat `cfg:"filename-format"`
	// Copy the captured image to the clipboard.
	CopyToClipboard bool `cfg:"copy-to-clipboard"`
	// Show a desktop notification after capturing. With the thumbnail
	// disabled it carries Edit, Open folder and Delete buttons.
	Notify bool `cfg:"notify"`
	// How long, in milliseconds, the floating thumbnail stays after a
	// capture; hovering it pauses the countdown. Click it to annotate, drag
	// it into an app, or delete the capture from it. Set to 0 to disable.
	ThumbnailMs uint32 `cfg:"thumbnail-ms"`
	// Screen corner the floating thumbnail sits in.
	ThumbnailPosition ScreenshotCorner `cfg:"thumbnail-position"`
	// Tesseract language codes `lys-shell screenshot text` reads with (e.g.
	// `[eng, deu]`). Empty follows the session language.
	OcrLanguages []string `cfg:"ocr-languages"`
	// Display border around button.
	BorderShow bool `cfg:"border-show"`
	// Border color token.
	BorderColor ColorValue `cfg:"border-color"`
	// Display module icon.
	IconShow bool `cfg:"icon-show"`
	// Icon foreground color. Auto selects based on variant for contrast.
	IconColor ColorValue `cfg:"icon-color"`
	// Icon container background color token.
	IconBgColor ColorValue `cfg:"icon-bg-color"`
	// Static label text shown beside the icon.
	Label string `cfg:"label"`
	// Display label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Max label characters before truncation with ellipsis. Set to 0 to disable.
	LabelMaxLength uint32 `cfg:"label-max-length"`
	// Button background color token.
	ButtonBgColor ColorValue `cfg:"button-bg-color"`
	// Action on left click. Default captures a region.
	LeftClick ClickAction `cfg:"left-click"`
	// Action on right click. Default captures the focused output.
	RightClick ClickAction `cfg:"right-click"`
	// Action on middle click. Default captures the active window.
	MiddleClick ClickAction `cfg:"middle-click"`
	// Action on scroll up.
	ScrollUp ClickAction `cfg:"scroll-up"`
	// Action on scroll down.
	ScrollDown ClickAction `cfg:"scroll-down"`
}

// DefaultsScreenshot returns the schema defaults.
func DefaultsScreenshot() ScreenshotConfig {
	return ScreenshotConfig{
		Icon:              "ld-camera-symbolic",
		OutputDirectory:   "",
		FilenameFormat:    mustStrftime("Screenshot_%Y-%m-%d_%H-%M-%S.png"),
		CopyToClipboard:   true,
		Notify:            true,
		ThumbnailMs:       5000,
		ThumbnailPosition: ScreenshotBottomRight,
		OcrLanguages:      []string{},
		BorderShow:        false,
		BorderColor:       mustColor("accent"),
		IconShow:          true,
		IconColor:         mustColor("auto"),
		IconBgColor:       mustColor("accent"),
		Label:             "",
		LabelShow:         false,
		LabelColor:        mustColor("fg-default"),
		LabelMaxLength:    0,
		ButtonBgColor:     mustColor("bg-surface-elevated"),
		LeftClick:         ParseClickAction("lys-shell screenshot region"),
		RightClick:        ParseClickAction("lys-shell screenshot output"),
		MiddleClick:       ParseClickAction("lys-shell screenshot window"),
		ScrollUp:          ClickAction{},
		ScrollDown:        ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c ScreenshotConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}

// ScreenshotCorner is the screen corner the capture thumbnail sits in.
//
// Screen corner for the screenshot thumbnail.
type ScreenshotCorner string

// Screenshot thumbnail corners.
const (
	// Top-left corner.
	ScreenshotTopLeft ScreenshotCorner = "top-left"
	// Top-right corner.
	ScreenshotTopRight ScreenshotCorner = "top-right"
	// Bottom-right corner.
	ScreenshotBottomRight ScreenshotCorner = "bottom-right"
	// Bottom-left corner.
	ScreenshotBottomLeft ScreenshotCorner = "bottom-left"
)

var _ = registerEnum(ScreenshotTopLeft, ScreenshotTopRight, ScreenshotBottomRight, ScreenshotBottomLeft)
