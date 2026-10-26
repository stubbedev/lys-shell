package recorder

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
)

// Camera pixel formats, V4L2 fourccs.
const (
	pixYUYV = 'Y' | 'U'<<8 | 'Y'<<16 | 'V'<<24
	pixMJPG = 'M' | 'J'<<8 | 'P'<<16 | 'G'<<24
)

// fourccName spells a V4L2 fourcc.
func fourccName(f uint32) string {
	return string([]byte{byte(f), byte(f >> 8), byte(f >> 16), byte(f >> 24)})
}

// cameraFrame is the layout a camera delivers: size, pixel format, and
// bytes per line (YUYV).
type cameraFrame struct {
	width, height int
	format        uint32
	stride        int
}

// paddedFrame allocates the overlay picture a camera frame of f's size
// is letterboxed into for dst, black, and where the camera's pixels go.
func paddedFrame(f cameraFrame, dst image.Rectangle) (*image.RGBA, image.Point) {
	size, at := letterbox(f.width, f.height, dst)
	img := image.NewRGBA(image.Rectangle{Max: size})
	clearOpaque(img)
	return img, at
}

// clearOpaque paints img opaque black.
func clearOpaque(img *image.RGBA) {
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
}

// decodeFrame writes one camera frame's pixels into img at at; the
// padding around them is left as it is.
func decodeFrame(img *image.RGBA, at image.Point, f cameraFrame, data []byte) error {
	switch f.format {
	case pixYUYV:
		return yuyvToRGBA(img, at, f, data)
	case pixMJPG:
		pic, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("decode an MJPEG camera frame: %w", err)
		}
		if b := pic.Bounds(); b.Dx() != f.width || b.Dy() != f.height {
			return fmt.Errorf("an MJPEG camera frame is %dx%d, the stream %dx%d", b.Dx(), b.Dy(), f.width, f.height)
		}
		draw.Draw(img, image.Rectangle{Min: at, Max: at.Add(image.Pt(f.width, f.height))}, pic, pic.Bounds().Min, draw.Src)
		return nil
	}
	return fmt.Errorf("camera pixel format %s is neither YUYV nor MJPG", fourccName(f.format))
}

// yuyvToRGBA converts packed 4:2:2 BT.601 limited-range YUYV.
func yuyvToRGBA(img *image.RGBA, at image.Point, f cameraFrame, data []byte) error {
	if f.width%2 != 0 || f.stride < f.width*2 || len(data) < f.stride*(f.height-1)+f.width*2 {
		return fmt.Errorf("a YUYV camera frame of %d bytes does not hold %dx%d at stride %d", len(data), f.width, f.height, f.stride)
	}
	for y := range f.height {
		src := data[y*f.stride : y*f.stride+f.width*2]
		dst := img.Pix[img.PixOffset(at.X, at.Y+y):]
		for x := 0; x < f.width; x += 2 {
			s := src[x*2 : x*2+4]
			u, v := int32(s[1])-128, int32(s[3])-128
			ycbcrPixel(dst[x*4:x*4+4], s[0], u, v)
			ycbcrPixel(dst[x*4+4:x*4+8], s[2], u, v)
		}
	}
	return nil
}

// ycbcrPixel writes one opaque RGBA pixel from limited-range BT.601,
// in 16.16 fixed point.
func ycbcrPixel(px []byte, y8 byte, u, v int32) {
	y := (int32(y8) - 16) * 76309
	px[0] = clamp8((y + 104597*v + 1<<15) >> 16)
	px[1] = clamp8((y - 25675*u - 53279*v + 1<<15) >> 16)
	px[2] = clamp8((y + 132201*u + 1<<15) >> 16)
	px[3] = 0xff
}

func clamp8(v int32) byte { return byte(min(max(v, 0), 255)) }
