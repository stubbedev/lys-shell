package filechooser

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"
)

// What a preview reads: a text file's first previewTextBytes, of which
// a NUL in the first previewSniffBytes marks it binary, and a folder's
// entries up to folderCountCap.
const (
	previewTextBytes  = 64 << 10
	previewSniffBytes = 4 << 10
	folderCountCap    = 10_000
)

// The preview's boxes in logical pixels: Quick Look's image and text,
// and the pane's, whose width the pane gives. Images decode for
// twice Quick Look's box, for HiDPI.
const (
	quicklookImageW, quicklookImageH = 480, 360
	quicklookTextW, quicklookTextH   = 480, 320
	paneImageH, paneTextH            = 180, 260
)

// previewKind is what a preview shows besides the name and detail line.
type previewKind uint8

const (
	// previewCard shows the icon or thumbnail alone: a binary file, or
	// one nothing more can be shown of.
	previewCard previewKind = iota
	previewText
	previewImage
	previewFolder
)

// preview is what was found out about a file to show it: its type's
// description, and its first text, its image fitted to the preview's
// box with the source's pixel size (zero when the image is a
// thumbnail), or its folder's item count, more when past the cap. read
// is how many bytes of the file were read for the text.
type preview struct {
	kind        previewKind
	description string
	text        string
	read        int
	raster      thumbRaster
	size        image.Point
	items       int
	more        bool
}

// previewRequest is the entry to preview and its guessed type.
type previewRequest struct {
	entry    Entry
	mimeType string
}

// previewView is one place a preview shows: Quick Look's card or the
// preview pane. visual holds the icon, image or text over the name and
// the detail line, within an image box and a text box.
type previewView struct {
	card, visual   *widget.Box
	name, info     *widget.Label
	code           *widgets.CodeView
	imageW, imageH int
	textW, textH   int
}

func (c *Chooser) newPreviewView(class string, nameChars, imageW, imageH, textW, textH int) *previewView {
	th := widget.Current()
	v := &previewView{imageW: imageW, imageH: imageH, textW: textW, textH: textH}
	v.card = widget.NewBox(widget.Column, th.Spacing, 2*th.Padding)
	v.card.AddClass(class)
	v.visual = widget.NewBox(widget.Row, 0, 0)
	v.visual.AddClass(class + "-icon")
	v.name = c.label("", class+"-name")
	v.name.SetAlignment(render.AlignCenter)
	v.name.SetWrap(true)
	v.name.SetMaxWidthChars(nameChars)
	v.info = c.label("", class+"-info", css.Caption, css.DimLabel)
	v.info.SetAlignment(render.AlignCenter)
	v.info.SetWrap(true)
	v.code = widgets.NewCodeView(c.d.Font, widgets.TextPx(styling.Caption, 1))
	v.card.Append(widgets.Center(v.visual), false)
	v.card.Append(v.name, false)
	v.card.Append(v.info, false)
	return v
}

// previewState is the preview being shown or loaded: the entry and
// what was found for it, the load running and how to cancel it, and
// whether one is queued to start once the loop is done with the
// gestures in hand.
type previewState struct {
	entry     Entry
	shown     *preview
	cancel    context.CancelFunc
	scheduled bool
}

// showPreview fills the previews showing with e at once, its icon and
// what the listing knows, and queues a load of its contents: the
// gestures the loop runs before it starts only move what is loaded.
func (c *Chooser) showPreview(e Entry) {
	for _, v := range c.openPreviews() {
		v.visual.Clear()
		v.visual.Append(c.entryIcon(e, previewIconPx), false)
		v.name.SetText(e.Name())
		v.info.SetText(previewInfo(e))
	}
	if c.pv.entry == e && (c.pv.shown != nil || c.pv.cancel != nil) {
		if c.pv.shown != nil {
			c.applyPreview(*c.pv.shown)
		}
		return
	}
	c.dropPreview()
	c.pv.entry = e
	if c.pv.scheduled {
		return
	}
	c.pv.scheduled = true
	c.d.Invoke(c.startPreview)
}

// openPreviews are the previews showing.
func (c *Chooser) openPreviews() []*previewView {
	var open []*previewView
	if c.ui.quicklook.Visible() {
		open = append(open, c.ui.ql)
	}
	if c.ui.preview.Visible() {
		open = append(open, c.ui.pv)
	}
	return open
}

// startPreview loads the entry the previews last asked for, off the
// loop, unless they closed or the dialog moved on.
func (c *Chooser) startPreview() {
	c.pv.scheduled = false
	if c.reply == nil || c.ui == nil || len(c.openPreviews()) == 0 || c.pv.cancel != nil || c.pv.shown != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.pv.cancel = cancel
	u, req := c.ui, previewRequest{entry: c.pv.entry, mimeType: c.d.MIME.TypeByName(c.pv.entry.Name())}
	load := c.previewLoader
	c.offLoop.Go(func() {
		p, err := load(ctx, req)
		c.d.Invoke(func() { c.landPreview(ctx, u, req.entry, p, err) })
	})
}

// landPreview shows a load that ended, unless it was superseded; a
// superseded image is freed at once.
func (c *Chooser) landPreview(ctx context.Context, u *ui, e Entry, p preview, err error) {
	if ctx.Err() != nil || c.ui != u || c.pv.entry != e {
		c.freePreview(p)
		return
	}
	c.pv.cancel = nil
	if err != nil {
		log.Printf("file chooser: previewing %s: %v", e.Path, err)
		p = preview{}
	}
	c.pv.shown = &p
	c.applyPreview(p)
}

// applyPreview shows p in the previews showing.
func (c *Chooser) applyPreview(p preview) {
	for _, v := range c.openPreviews() {
		switch p.kind {
		case previewText:
			if v.code.Text() != p.text || v.code.Name() != c.pv.entry.Name() {
				v.code.SetText(p.text, c.pv.entry.Name())
			}
			v.visual.Clear()
			v.visual.Append(widgets.Pin(v.code, v.textW, v.textH), false)
		case previewImage:
			size := fitSize(p.raster.image.Bounds(), v.imageW, v.imageH)
			img := widget.NewImage(p.raster.image)
			img.SetScale(widget.ImageFit)
			img.AddClass("file-chooser-preview-image")
			v.visual.Clear()
			v.visual.Append(widgets.Pin(img, size.X, size.Y), false)
		}
		v.info.SetText(previewDetail(c.pv.entry, p))
	}
}

// dropPreview cancels the load running and frees what was shown.
func (c *Chooser) dropPreview() {
	if c.pv.cancel != nil {
		c.pv.cancel()
		c.pv.cancel = nil
	}
	if c.pv.shown != nil {
		c.freePreview(*c.pv.shown)
		c.pv.shown = nil
	}
	c.pv.entry = Entry{}
}

// freePreview frees a preview's image once no frame draws it.
func (c *Chooser) freePreview(p preview) {
	if p.kind != previewImage {
		return
	}
	c.d.ForgetImage(p.raster.image.ID)
	th := c.thumbnailer
	render.Retire(func() {
		if err := th.free([]thumbRaster{p.raster}); err != nil {
			log.Printf("file chooser: freeing a preview image: %v", err)
		}
	})
}

// loadPreview finds out what to show of an entry; safe off the loop.
// A folder counts its entries; a text type reads its first text; a
// raster image decodes whole for Quick Look's box; another type shows
// its large thumbnail when there is one, else the card alone.
func (c *Chooser) loadPreview(ctx context.Context, req previewRequest) (preview, error) {
	e := req.entry
	if e.IsDir {
		items, more, err := c.reader.Count(e.Path, folderCountCap)
		return preview{kind: previewFolder, items: items, more: more}, err
	}
	p := preview{description: c.d.MIME.Comment(req.mimeType)}
	if c.d.MIME.IsA(req.mimeType, "text/plain") {
		text, err := readHead(e.Path)
		p.read = len(text)
		if err != nil || bytes.IndexByte(text[:min(len(text), previewSniffBytes)], 0) >= 0 {
			return p, err
		}
		p.kind, p.text = previewText, string(text)
		return p, nil
	}
	raster, size, err := c.thumbnailer.preview(ctx, e.Path, req.mimeType, 2*quicklookImageW, 2*quicklookImageH)
	switch {
	case errors.Is(err, errNoThumb):
		return p, nil
	case err != nil:
		return p, err
	}
	p.kind, p.raster, p.size = previewImage, raster, size
	return p, nil
}

// readHead reads the first previewTextBytes of path, and no more.
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // a file the user is choosing among
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, previewTextBytes)
	n, err := io.ReadFull(f, buf)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		err = nil
	}
	return buf[:n], err
}

// previewDetail is the detail line a loaded preview shows: a folder's
// item count, a file's type described, an image's pixel size.
func previewDetail(e Entry, p preview) string {
	if p.kind == previewFolder {
		items := strconv.Itoa(p.items) + " items"
		switch {
		case p.more:
			items = groupThousands(p.items) + "+ items"
		case p.items == 1:
			items = "1 item"
		}
		return "Folder · " + items + " · " + FormatTime(e.Modified)
	}
	kind := p.description
	if kind == "" {
		kind = fileKind(e)
	}
	if p.size != (image.Point{}) {
		kind += " · " + strconv.Itoa(p.size.X) + " × " + strconv.Itoa(p.size.Y)
	}
	return kind + " · " + HumanSize(e.Size) + " · " + FormatTime(e.Modified)
}

// groupThousands writes n with a comma between each group of three
// digits.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	var b []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, s[i])
	}
	return string(b)
}
