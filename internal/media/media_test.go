// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func writeJPEG(t *testing.T, path string, w, h int, exifOrientation int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	if exifOrientation > 0 {
		// APP1 Exif with one IFD entry (orientation) and a GPS-looking marker string.
		tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, byte(exifOrientation), 0, 0, 0, 0, 0, 0, 0, 0}
		payload := append([]byte("Exif\x00\x00"), tiff...)
		payload = append(payload, []byte("GPSSECRET")...)
		seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
		seg = append(seg, payload...)
		data = append(append([]byte{0xFF, 0xD8}, seg...), data[2:]...)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProcessJPEG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeJPEG(t, src, 1000, 500, 6)
	p := New(Settings{Widths: []int{480, 800, 1600}, Formats: []string{"avif", "webp"}, Quality: 60}, filepath.Join(dir, "cache"), 2)
	r, err := p.Process(src)
	if err != nil {
		t.Fatal(err)
	}
	// Orientation 6 rotates: 1000x500 becomes 500x1000; widths 480 and 500.
	if r.Width != 500 || r.Height != 1000 {
		t.Errorf("size = %dx%d, want 500x1000", r.Width, r.Height)
	}
	if len(r.Variants) != 6 {
		t.Fatalf("variants = %+v", r.Variants)
	}
	out := filepath.Join(dir, "out")
	if err := p.CopyTo(r, out); err != nil {
		t.Fatal(err)
	}
	for _, v := range r.Variants {
		b, err := os.ReadFile(filepath.Join(out, v.Name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("GPSSECRET")) || bytes.Contains(b, []byte("Exif")) {
			t.Errorf("%s still carries EXIF", v.Name)
		}
	}
	// Second run hits the cache and returns the same names.
	r2, err := New(Settings{Widths: []int{480, 800, 1600}, Formats: []string{"avif", "webp"}, Quality: 60}, filepath.Join(dir, "cache"), 1).Process(src)
	if err != nil || r2.Variants[0].Name != r.Variants[0].Name {
		t.Errorf("cache miss: %v %+v", err, r2)
	}
}

func TestRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.png")
	_ = os.WriteFile(src, []byte("not an image"), 0o644)
	if _, err := New(Settings{Widths: []int{100}, Quality: 50}, dir, 1).Process(src); err == nil {
		t.Fatal("want error")
	}
}

func TestSVGSize(t *testing.T) {
	w, h := svgSize([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 40"></svg>`))
	if w != 120 || h != 40 {
		t.Errorf("got %dx%d", w, h)
	}
}
