package kb

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// withExif inserts an APP1 Exif segment carrying only the orientation tag
// right after the JPEG SOI marker.
func withExif(jpg []byte, orientation uint16, bo binary.ByteOrder) []byte {
	tiff := make([]byte, 8+2+12+4)
	if bo == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	bo.PutUint16(tiff[2:], 42)
	bo.PutUint32(tiff[4:], 8)
	bo.PutUint16(tiff[8:], 1)
	bo.PutUint16(tiff[10:], 0x0112)
	bo.PutUint16(tiff[12:], 3) // SHORT
	bo.PutUint32(tiff[14:], 1)
	bo.PutUint16(tiff[18:], orientation)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	head := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(head[2:], uint16(len(seg)+2))
	out := append([]byte{}, jpg[:2]...)
	out = append(out, head...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func TestMakeThumbnail(t *testing.T) {
	ctx := context.Background()

	// Small images are shown as they are.
	if _, _, err := makeThumbnail(ctx, encodePNG(t, solid(300, 200, color.White))); err != errNoThumbnail {
		t.Fatalf("small image: %v", err)
	}
	// Not an image, or too many pixels to decode safely: keep the original.
	if _, _, err := makeThumbnail(ctx, []byte("not an image")); err != errNoThumbnail {
		t.Fatalf("garbage: %v", err)
	}
	huge := encodePNG(t, image.NewGray(image.Rect(0, 0, 8000, 4001))) // 32,008,000 px, over the 24M cap
	if _, _, err := makeThumbnail(ctx, huge); err != errNoThumbnail {
		t.Fatalf("over pixel cap: %v", err)
	}

	// Opaque PNG → JPEG, longer side 480, aspect kept.
	out, kind, err := makeThumbnail(ctx, encodePNG(t, solid(1600, 900, color.RGBA{200, 30, 30, 255})))
	if err != nil || kind != "image/jpeg" {
		t.Fatalf("opaque: %v %s", err, kind)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 480 || cfg.Height != 270 {
		t.Fatalf("opaque size: %v %dx%d", err, cfg.Width, cfg.Height)
	}

	// Transparency survives as PNG.
	out, kind, err = makeThumbnail(ctx, encodePNG(t, solid(600, 1200, color.NRGBA{0, 0, 255, 100})))
	if err != nil || kind != "image/png" {
		t.Fatalf("alpha: %v %s", err, kind)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil || img.Bounds().Dx() != 240 || img.Bounds().Dy() != 480 {
		t.Fatalf("alpha size: %v %v", err, img.Bounds())
	}
	if _, _, _, a := img.At(10, 10).RGBA(); a == 0xffff {
		t.Fatal("alpha lost")
	}

	// GIF: first frame.
	var g bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, 1000, 500), []color.Color{color.Black, color.White})
	if err = gif.Encode(&g, pal, nil); err != nil {
		t.Fatal(err)
	}
	if _, kind, err = makeThumbnail(ctx, g.Bytes()); err != nil || kind != "image/jpeg" {
		t.Fatalf("gif: %v %s", err, kind)
	}
}

func TestThumbnailOrientation(t *testing.T) {
	ctx := context.Background()
	// 800×400, left half red, right half blue.
	src := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			if x < 400 {
				src.Set(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				src.Set(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if jpegOrientation(b.Bytes()) != 0 {
		t.Fatal("orientation without EXIF")
	}
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		data := withExif(b.Bytes(), 6, bo) // rotate 90° clockwise to display
		if o := jpegOrientation(data); o != 6 {
			t.Fatalf("%v orientation %d", bo, o)
		}
		out, kind, err := makeThumbnail(ctx, data)
		if err != nil || kind != "image/jpeg" {
			t.Fatalf("rotated: %v %s", err, kind)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 240 || img.Bounds().Dy() != 480 {
			t.Fatalf("rotated size %v", img.Bounds())
		}
		// After a clockwise turn the red (left) half is on top.
		if r, _, bl, _ := img.At(120, 60).RGBA(); r < bl {
			t.Fatal("top should be red")
		}
		if r, _, bl, _ := img.At(120, 420).RGBA(); bl < r {
			t.Fatal("bottom should be blue")
		}
	}
	// A small image that needs turning still gets a thumbnail.
	var small bytes.Buffer
	jpeg.Encode(&small, image.NewRGBA(image.Rect(0, 0, 40, 20)), nil)
	if _, _, err := makeThumbnail(ctx, withExif(small.Bytes(), 8, binary.BigEndian)); err != nil {
		t.Fatalf("small rotated: %v", err)
	}
	// Truncated or odd headers never panic.
	for _, d := range [][]byte{{0xFF, 0xD8}, {0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, withExif(b.Bytes(), 9, binary.BigEndian)[:30]} {
		jpegOrientation(d)
	}
}

func TestOrientAllCodes(t *testing.T) {
	// 3×2 image with a marked top-left pixel; check where it lands.
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	mark := color.NRGBA{255, 0, 0, 255}
	src.SetNRGBA(0, 0, mark)
	want := map[int]image.Point{1: {0, 0}, 2: {2, 0}, 3: {2, 1}, 4: {0, 1}, 5: {0, 0}, 6: {1, 0}, 7: {1, 2}, 8: {0, 2}}
	for o, p := range want {
		out := orient(src, o)
		if o >= 5 && (out.Bounds().Dx() != 2 || out.Bounds().Dy() != 3) {
			t.Fatalf("orientation %d size %v", o, out.Bounds())
		}
		if out.NRGBAAt(p.X, p.Y) != mark {
			t.Fatalf("orientation %d: mark not at %v", o, p)
		}
	}
	if thumbnailKeyFor("attachments/p/i") != "thumbnails/p/i" || thumbnailKeyFor("other/x") != "" {
		t.Fatal("thumbnail key mapping")
	}
	if w, h := fitWithin(481, 1, 480); w != 480 || h != 1 {
		t.Fatalf("fitWithin keeps 1px: %dx%d", w, h)
	}
}
