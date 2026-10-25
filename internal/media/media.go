// SPDX-License-Identifier: AGPL-3.0-or-later

// Package media resizes and re-encodes images at build time: responsive
// sizes, AVIF and WebP, EXIF (and GPS) stripped, width and height known.
// Results are cached on disk by content hash, so rebuilds are fast.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Settings control encoding.
type Settings struct {
	Widths  []int
	Formats []string // "avif", "webp"
	Quality int
	MaxSize int64
}

func (s Settings) key() string {
	return fmt.Sprintf("v2|%v|%v|%d", s.Widths, s.Formats, s.Quality)
}

// Variant is one encoded file.
type Variant struct {
	Format string `json:"format"` // "avif", "webp", "jpeg", "png", "gif", "svg"
	Width  int    `json:"width"`
	Name   string `json:"name"` // file name under media/
}

// Result describes a processed image.
type Result struct {
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Variants []Variant `json:"variants"`
}

// Pipeline processes images. It is safe for concurrent use.
type Pipeline struct {
	cfg      Settings
	cacheDir string
	mu       sync.Mutex
	inflight map[string]*call
	// sem limits concurrent encodes (each holds a full decoded image).
	sem chan struct{}
}

type call struct {
	wg  sync.WaitGroup
	res *Result
	err error
}

// New returns a pipeline caching under cacheDir.
func New(cfg Settings, cacheDir string, workers int) *Pipeline {
	if workers < 1 {
		workers = 1
	}
	return &Pipeline{cfg: cfg, cacheDir: cacheDir, inflight: map[string]*call{}, sem: make(chan struct{}, workers)}
}

// Process encodes the image at path (once per content) and returns its variants.
// The files live in the cache; CopyTo puts them in the output.
func (p *Pipeline) Process(path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if p.cfg.MaxSize > 0 && int64(len(data)) > p.cfg.MaxSize {
		return nil, fmt.Errorf("%s is %d MB, over the %d MB limit", filepath.Base(path), len(data)>>20, p.cfg.MaxSize>>20)
	}
	sum := sha256.Sum256(append([]byte(p.cfg.key()+"|"), data...))
	key := hex.EncodeToString(sum[:8])

	p.mu.Lock()
	if c, ok := p.inflight[key]; ok {
		p.mu.Unlock()
		c.wg.Wait()
		return c.res, c.err
	}
	c := &call{}
	c.wg.Add(1)
	p.inflight[key] = c
	p.mu.Unlock()

	c.res, c.err = p.process(key, path, data)
	c.wg.Done()
	return c.res, c.err
}

func (p *Pipeline) dir(key string) string { return filepath.Join(p.cacheDir, key[:2], key) }

func (p *Pipeline) process(key, path string, data []byte) (*Result, error) {
	dir := p.dir(key)
	if b, err := os.ReadFile(filepath.Join(dir, "result.json")); err == nil {
		var r Result
		if json.Unmarshal(b, &r) == nil && p.complete(dir, &r) {
			return &r, nil
		}
	}
	p.sem <- struct{}{}
	defer func() { <-p.sem }()
	r, files, err := encode(key, path, data, p.cfg)
	if err != nil {
		return nil, err
	}
	tmp := dir + ".tmp" + strconv.Itoa(os.Getpid())
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			return nil, err
		}
	}
	meta, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(tmp, "result.json"), meta, 0o644); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	return r, nil
}

func (p *Pipeline) complete(dir string, r *Result) bool {
	for _, v := range r.Variants {
		if _, err := os.Stat(filepath.Join(dir, v.Name)); err != nil {
			return false
		}
	}
	return len(r.Variants) > 0
}

// CopyTo writes a result's files into outDir (for example dist/media).
func (p *Pipeline) CopyTo(r *Result, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, v := range r.Variants {
		key := strings.SplitN(v.Name, "-", 2)[0]
		src := filepath.Join(p.dir(key), v.Name)
		dst := filepath.Join(outDir, v.Name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.Link(src, dst); err == nil {
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ErrUnsupported is returned for files that are not images.
var ErrUnsupported = errors.New("unsupported image type (use JPEG, PNG, GIF, WebP or SVG)")

func encode(key, path string, data []byte, cfg Settings) (*Result, map[string][]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	files := map[string][]byte{}
	if ext == ".svg" {
		w, h := svgSize(data)
		name := key + "-0.svg"
		files[name] = data
		return &Result{Width: w, Height: h, Variants: []Variant{{Format: "svg", Name: name}}}, files, nil
	}
	cfgImg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, ErrUnsupported
	}
	if cfgImg.Width*cfgImg.Height > 80_000_000 {
		return nil, nil, fmt.Errorf("image is %dx%d; images over 80 megapixels are refused", cfgImg.Width, cfgImg.Height)
	}
	if format == "gif" {
		// GIFs keep their animation; they carry no EXIF.
		if g, err := gif.DecodeAll(bytes.NewReader(data)); err == nil && len(g.Image) > 1 {
			name := key + "-0.gif"
			files[name] = data
			return &Result{Width: g.Config.Width, Height: g.Config.Height, Variants: []Variant{{Format: "gif", Width: g.Config.Width, Name: name}}}, files, nil
		}
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("decoding: %w", err)
	}
	if format == "jpeg" {
		src = orient(src, jpegOrientation(data))
	}
	b := src.Bounds()
	r := &Result{Width: b.Dx(), Height: b.Dy()}
	widths := pickWidths(cfg.Widths, b.Dx())
	// The largest variant sets the intrinsic size we report.
	if widths[len(widths)-1] < b.Dx() {
		r.Height = int(float64(b.Dy()) * float64(widths[len(widths)-1]) / float64(b.Dx()))
		r.Width = widths[len(widths)-1]
	}
	opaque := isOpaque(src)
	fallback := "png"
	if opaque && format != "png" && format != "gif" {
		fallback = "jpeg"
	}
	for _, w := range widths {
		img := resize(src, w)
		for _, f := range append(append([]string{}, cfg.Formats...), fallback) {
			var buf bytes.Buffer
			var err error
			switch f {
			case "avif":
				err = avif.Encode(&buf, img, avif.Options{Quality: cfg.Quality, QualityAlpha: cfg.Quality, Speed: 8})
			case "webp":
				err = webp.Encode(&buf, img, webp.Options{Quality: cfg.Quality, Method: 4})
			case "jpeg":
				err = jpeg.Encode(&buf, flatten(img), &jpeg.Options{Quality: min(cfg.Quality+12, 92)})
			case "png":
				err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("encoding %s: %w", f, err)
			}
			extName := map[string]string{"avif": "avif", "webp": "webp", "jpeg": "jpg", "png": "png"}[f]
			name := fmt.Sprintf("%s-%d.%s", key, w, extName)
			files[name] = buf.Bytes()
			r.Variants = append(r.Variants, Variant{Format: f, Width: w, Name: name})
		}
	}
	sort.SliceStable(r.Variants, func(i, j int) bool { return r.Variants[i].Width < r.Variants[j].Width })
	return r, files, nil
}

func pickWidths(widths []int, orig int) []int {
	var out []int
	for _, w := range widths {
		if w < orig {
			out = append(out, w)
		}
	}
	max := orig
	if len(widths) > 0 && widths[len(widths)-1] < orig {
		max = widths[len(widths)-1]
	}
	if len(out) == 0 || out[len(out)-1] != max {
		out = append(out, max)
	}
	return out
}

func resize(src image.Image, w int) image.Image {
	b := src.Bounds()
	if w >= b.Dx() {
		dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		return dst
	}
	h := int(float64(b.Dy())*float64(w)/float64(b.Dx()) + 0.5)
	if h < 1 {
		h = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}

// svgSize reads width/height or the viewBox of an SVG, best effort.
func svgSize(data []byte) (int, int) {
	head := string(data[:min(len(data), 4096)])
	num := func(attr string) int {
		i := strings.Index(head, attr+"=\"")
		if i < 0 {
			return 0
		}
		rest := head[i+len(attr)+2:]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			return 0
		}
		f, _ := strconv.ParseFloat(strings.TrimSuffix(rest[:j], "px"), 64)
		return int(f)
	}
	w, h := num("width"), num("height")
	if w > 0 && h > 0 {
		return w, h
	}
	if i := strings.Index(head, "viewBox=\""); i >= 0 {
		rest := head[i+9:]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			parts := strings.Fields(strings.ReplaceAll(rest[:j], ",", " "))
			if len(parts) == 4 {
				fw, _ := strconv.ParseFloat(parts[2], 64)
				fh, _ := strconv.ParseFloat(parts[3], 64)
				return int(fw), int(fh)
			}
		}
	}
	return 0, 0
}
