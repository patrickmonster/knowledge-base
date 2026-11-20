package kb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif" // registers the GIF decoder (first frame)
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

// Issue image thumbnails. The issue cards show photos in a 160×120 box, so
// the page asks for ?size=thumb and gets a small copy instead of the original
// (up to MaxIssueImageBytes). The copy is made the first time it is asked for
// and kept in the bucket next to the original, so existing photos need no
// backfill and each photo is decoded at most once per copy lost.
const (
	// thumbMaxSide bounds the longer side: covers the 160×120 card box on a
	// 2× screen with room for object-fit: cover cropping.
	thumbMaxSide = 480
	// maxThumbPixels caps decoding. A decoded image takes 1.5–4 bytes per
	// pixel (a progressive JPEG also keeps its coefficients, several bytes
	// more), so one decode stays around 100–200 MB at most. Larger images keep
	// being served as the original, as before.
	maxThumbPixels = 24_000_000
	// maxThumbBytes caps a stored thumbnail read back from the bucket.
	maxThumbBytes    = 2 << 20
	thumbJPEGQuality = 82
)

// thumbSlots lets one thumbnail decode run at a time so several first views
// of large photos cannot stack their memory. Thumbnails are made once, so
// waiting here is rare.
var thumbSlots = make(chan struct{}, 1)

func thumbnailKey(projectID, id string) string { return "thumbnails/" + projectID + "/" + id }

// thumbnailKeyFor maps an original's object key to its thumbnail key, or ""
// when the key is not an attachment key.
func thumbnailKeyFor(objectKey string) string {
	rest, ok := strings.CutPrefix(objectKey, "attachments/")
	if !ok {
		return ""
	}
	return "thumbnails/" + rest
}

// errNoThumbnail means the original should be shown as is: it is already
// small, too large to decode safely, or not decodable.
var errNoThumbnail = errors.New("no thumbnail")

// makeThumbnail returns a scaled copy of a PNG, JPEG, GIF (first frame) or
// WebP image whose longer side is thumbMaxSide, as JPEG when opaque and PNG
// otherwise. JPEG EXIF orientation is applied, as browsers do for the original.
func makeThumbnail(ctx context.Context, data []byte) ([]byte, string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", errNoThumbnail
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxThumbPixels {
		return nil, "", errNoThumbnail
	}
	orientation := jpegOrientation(data)
	if cfg.Width <= thumbMaxSide && cfg.Height <= thumbMaxSide && orientation <= 1 {
		return nil, "", errNoThumbnail
	}
	select {
	case thumbSlots <- struct{}{}:
		defer func() { <-thumbSlots }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errNoThumbnail
	}
	w, h := fitWithin(cfg.Width, cfg.Height, thumbMaxSide)
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	out := orient(dst, orientation)
	var buf bytes.Buffer
	if out.Opaque() {
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: thumbJPEGQuality})
		return buf.Bytes(), "image/jpeg", err
	}
	err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, out)
	return buf.Bytes(), "image/png", err
}

// fitWithin scales w×h so the longer side is at most max, keeping at least 1px.
func fitWithin(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, maxInt(1, int(int64(h)*int64(max)/int64(w)))
	}
	return maxInt(1, int(int64(w)*int64(max)/int64(h))), max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// orient applies an EXIF orientation (1–8) to a small image.
func orient(src *image.NRGBA, o int) *image.NRGBA {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 clockwise
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90 counter-clockwise
				dx, dy = y, w-1-x
			}
			dst.SetNRGBA(dx, dy, src.NRGBAAt(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// jpegOrientation reads the EXIF orientation tag (0x0112) of a JPEG, or 0
// when the data is not a JPEG or carries none.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 0
		}
		marker := b[i+1]
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // image data starts; EXIF comes before it
			return 0
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 0
		}
		seg := b[i+4 : i+2+n]
		if marker == 0xE1 && len(seg) >= 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 0
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:]) != 42 {
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	count := int(bo.Uint16(t[off:]))
	for k := 0; k < count; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			v := int(bo.Uint16(t[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 0
		}
	}
	return 0
}

// serveIssueThumbnail answers ?size=thumb for a ready issue image: the stored
// thumbnail if there is one, else a new one (stored for next time), else the
// original. The caller has already checked access.
func (a *App) serveIssueThumbnail(w http.ResponseWriter, r *http.Request, f Attachment) {
	ctx := r.Context()
	key := thumbnailKey(f.ProjectID, f.ID)
	if body, err := a.Store.Objects.Get(ctx, key); err == nil {
		data, err := io.ReadAll(io.LimitReader(body, maxThumbBytes+1))
		body.Close()
		if err == nil && len(data) <= maxThumbBytes {
			if kind := http.DetectContentType(data); kind == "image/jpeg" || kind == "image/png" {
				writeIssueImage(w, f.Filename, kind, data)
				return
			}
		}
	}
	body, err := a.Store.Objects.Get(ctx, attachmentKey(f.ProjectID, f.ID))
	if err != nil {
		a.fail(w, problem("STORAGE_UNAVAILABLE", "Could not read the image."))
		return
	}
	data, err := io.ReadAll(io.LimitReader(body, f.SizeBytes))
	body.Close()
	if err != nil {
		a.fail(w, problem("STORAGE_UNAVAILABLE", "Could not read the image."))
		return
	}
	kind, _, ok := inlineType(f.Filename, data[:min(len(data), 512)], true)
	if !ok {
		a.fail(w, problem("NOT_FOUND", "Image not found."))
		return
	}
	thumb, thumbKind, err := makeThumbnail(ctx, data)
	if err != nil {
		if !errors.Is(err, errNoThumbnail) && ctx.Err() == nil {
			log.Printf("issue thumbnail %s: %v", f.ID, err)
		}
		writeIssueImage(w, f.Filename, kind, data)
		return
	}
	if err = a.Store.Objects.Put(ctx, key, f.Filename, bytes.NewReader(thumb), int64(len(thumb))); err != nil {
		log.Printf("issue thumbnail %s: store: %v", f.ID, err) // still served; made again next time
	}
	writeIssueImage(w, f.Filename, thumbKind, thumb)
}

// writeIssueImage sends image bytes with the same headers as the original
// image route: sandboxed, inline, private short cache.
func writeIssueImage(w http.ResponseWriter, filename, kind string, data []byte) {
	h := w.Header()
	h.Set("Content-Type", kind)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}
