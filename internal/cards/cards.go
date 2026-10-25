// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cards draws the 1200x630 social sharing image for a post at
// build time: the post title on the theme's ground, the site name and date.
package cards

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed newsreader-400.ttf
var regularTTF []byte

//go:embed newsreader-600.ttf
var boldTTF []byte

// Width and Height are the card size (the Open Graph standard).
const (
	Width  = 1200
	Height = 630
)

// Card is what goes on one image.
type Card struct {
	Title  string
	Site   string
	Detail string // e.g. "October 20, 2026"
	Accent color.RGBA
}

var (
	loadOnce sync.Once
	regular  *opentype.Font
	bold     *opentype.Font
	loadErr  error
)

func load() error {
	loadOnce.Do(func() {
		if regular, loadErr = opentype.Parse(regularTTF); loadErr != nil {
			return
		}
		bold, loadErr = opentype.Parse(boldTTF)
	})
	return loadErr
}

var (
	ground = color.RGBA{0x0f, 0x14, 0x20, 0xff}
	ivory  = color.RGBA{0xec, 0xe6, 0xd9, 0xff}
	muted  = color.RGBA{0x9a, 0xa3, 0xb5, 0xff}
	rule   = color.RGBA{0x2a, 0x33, 0x45, 0xff}
)

// Render draws a card as PNG.
func Render(c Card) ([]byte, error) {
	if err := load(); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(img, img.Bounds(), &image.Uniform{ground}, image.Point{}, draw.Src)
	const margin = 80
	// One accent: a rule along the left edge.
	draw.Draw(img, image.Rect(0, 0, 12, Height), &image.Uniform{c.Accent}, image.Point{}, draw.Src)
	// A rule above the footer line.
	draw.Draw(img, image.Rect(margin, Height-130, Width-margin, Height-128), &image.Uniform{rule}, image.Point{}, draw.Src)

	// Title: the largest size whose wrapped lines fit in four lines.
	maxW := Width - 2*margin
	var lines []string
	var face font.Face
	var size float64
	for size = 76; size >= 40; size -= 4 {
		f, err := opentype.NewFace(bold, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		lines = wrap(f, c.Title, maxW)
		if len(lines) <= 4 && float64(len(lines))*size*1.12 <= float64(Height-margin-170) {
			face = f
			break
		}
		f.Close()
	}
	if face == nil {
		f, err := opentype.NewFace(bold, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		face = f
		lines = wrap(f, c.Title, maxW)
		if len(lines) > 4 {
			lines = lines[:4]
			lines[3] = strings.TrimRight(lines[3], " .,;:") + "…"
		}
	}
	defer face.Close()
	y := margin + int(size)
	for _, l := range lines {
		text(img, face, ivory, margin, y, l)
		y += int(size * 1.12)
	}

	small, err := opentype.NewFace(regular, &opentype.FaceOptions{Size: 30, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer small.Close()
	text(img, small, ivory, margin, Height-72, c.Site)
	if c.Detail != "" {
		w := font.MeasureString(small, c.Detail).Ceil()
		text(img, small, muted, Width-margin-w, Height-72, c.Detail)
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func text(img draw.Image, face font.Face, c color.Color, x, y int, s string) {
	d := &font.Drawer{Dst: img, Src: &image.Uniform{c}, Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

// wrap breaks s into lines no wider than max pixels.
func wrap(face font.Face, s string, max int) []string {
	var lines []string
	var cur string
	for _, w := range strings.Fields(s) {
		try := w
		if cur != "" {
			try = cur + " " + w
		}
		if font.MeasureString(face, try).Ceil() <= max || cur == "" {
			cur = try
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// ParseHex reads "#rrggbb"; ok is false for anything else.
func ParseHex(s string) (color.RGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return color.RGBA{}, false
	}
	var v [3]uint8
	for i := 0; i < 3; i++ {
		var n uint8
		for _, ch := range s[2*i : 2*i+2] {
			n <<= 4
			switch {
			case ch >= '0' && ch <= '9':
				n |= uint8(ch - '0')
			case ch >= 'a' && ch <= 'f':
				n |= uint8(ch-'a') + 10
			case ch >= 'A' && ch <= 'F':
				n |= uint8(ch-'A') + 10
			default:
				return color.RGBA{}, false
			}
		}
		v[i] = n
	}
	return color.RGBA{v[0], v[1], v[2], 0xff}, true
}
