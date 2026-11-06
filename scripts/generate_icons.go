//go:build ignore

package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

func main() {
	outDir := filepath.Join("internal", "web", "static")
	_ = os.MkdirAll(outDir, 0755)

	generateIcon(192, filepath.Join(outDir, "icon-192.png"), false)
	generateIcon(512, filepath.Join(outDir, "icon-512.png"), false)
	generateIcon(192, filepath.Join(outDir, "icon-maskable-192.png"), true)
	generateIcon(512, filepath.Join(outDir, "icon-maskable-512.png"), true)
	generateIcon(180, filepath.Join(outDir, "apple-touch-icon.png"), false)
}

func generateIcon(size int, path string, maskable bool) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Colors
	bgColor := color.RGBA{R: 0x0b, G: 0x0e, B: 0x14, A: 0xff}       // #0b0e14
	accentColor := color.RGBA{R: 0xf0, G: 0xb4, B: 0x29, A: 0xff}  // #f0b429 (Amber)
	textColor := color.RGBA{R: 0x1a, G: 0x13, B: 0x00, A: 0xff}    // #1a1300
	greenColor := color.RGBA{R: 0x35, G: 0xc4, B: 0x6a, A: 0xff}   // #35c46a
	redColor := color.RGBA{R: 0xff, G: 0x5d, B: 0x5d, A: 0xff}     // #ff5d5d

	// Fill background
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, bgColor)
		}
	}

	scale := float64(size) / 512.0

	// Badge dimensions
	var badgeSize, cornerRadius float64
	if maskable {
		// In maskable icon, keep within safe zone (80% diameter)
		badgeSize = 300.0 * scale
		cornerRadius = 70.0 * scale
	} else {
		badgeSize = 380.0 * scale
		cornerRadius = 90.0 * scale
	}

	cx := float64(size) / 2.0
	cy := float64(size) / 2.0
	bx1 := cx - badgeSize/2.0
	by1 := cy - badgeSize/2.0
	bx2 := cx + badgeSize/2.0
	by2 := cy + badgeSize/2.0

	// Draw rounded badge with subtle anti-aliasing
	for y := 0; y < size; y++ {
		fy := float64(y)
		for x := 0; x < size; x++ {
			fx := float64(x)
			d := roundedRectSDF(fx, fy, bx1, by1, bx2, by2, cornerRadius)
			if d <= 0 {
				img.Set(x, y, accentColor)
			} else if d < 1.0 {
				alpha := uint8((1.0 - d) * 255)
				img.Set(x, y, blend(bgColor, accentColor, alpha))
			}
		}
	}

	// Inside the badge, draw stylized bold 'S' and candlestick accents
	// We use signed distance field for thick strokes of the "S"
	sScale := badgeSize / 380.0
	sWidth := 34.0 * sScale

	// Outer bounds of S
	sx1 := cx - 75.0*sScale
	sx2 := cx + 75.0*sScale
	sy1 := cy - 110.0*sScale
	sy2 := cy + 110.0*sScale
	symid := cy

	for y := 0; y < size; y++ {
		fy := float64(y)
		for x := 0; x < size; x++ {
			fx := float64(x)
			// Check if pixel is inside the amber badge
			if roundedRectSDF(fx, fy, bx1, by1, bx2, by2, cornerRadius) > 0 {
				continue
			}

			// Draw stylized geometric S:
			// Top bar: [sx1 .. sx2] at sy1
			// Upper left vertical: sy1 .. symid at sx1
			// Middle bar: [sx1 .. sx2] at symid
			// Lower right vertical: symid .. sy2 at sx2
			// Bottom bar: [sx1 .. sx2] at sy2
			d := segmentDist(fx, fy, sx1+sWidth/2, sy1+sWidth/2, sx2-sWidth/2, sy1+sWidth/2)
			d = math.Min(d, segmentDist(fx, fy, sx1+sWidth/2, sy1+sWidth/2, sx1+sWidth/2, symid+sWidth/2))
			d = math.Min(d, segmentDist(fx, fy, sx1+sWidth/2, symid, sx2-sWidth/2, symid))
			d = math.Min(d, segmentDist(fx, fy, sx2-sWidth/2, symid-sWidth/2, sx2-sWidth/2, sy2-sWidth/2))
			d = math.Min(d, segmentDist(fx, fy, sx1+sWidth/2, sy2-sWidth/2, sx2-sWidth/2, sy2-sWidth/2))

			if d <= sWidth/2.0 {
				img.Set(x, y, textColor)
			} else if d <= sWidth/2.0+1.0 {
				alpha := uint8((sWidth/2.0 + 1.0 - d) * 255)
				img.Set(x, y, blend(accentColor, textColor, alpha))
			}

			// Green bull wick at top right
			gw := segmentDist(fx, fy, sx2+25*sScale, sy1-20*sScale, sx2+25*sScale, sy1+25*sScale)
			if gw <= 7*sScale {
				img.Set(x, y, greenColor)
			}

			// Red bear wick at bottom left
			rw := segmentDist(fx, fy, sx1-25*sScale, sy2-25*sScale, sx1-25*sScale, sy2+20*sScale)
			if rw <= 7*sScale {
				img.Set(x, y, redColor)
			}
		}
	}

	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func roundedRectSDF(x, y, x1, y1, x2, y2, r float64) float64 {
	cx := (x1 + x2) / 2.0
	cy := (y1 + y2) / 2.0
	hw := (x2 - x1) / 2.0
	hh := (y2 - y1) / 2.0

	dx := math.Abs(x-cx) - (hw - r)
	dy := math.Abs(y-cy) - (hh - r)

	ax := math.Max(dx, 0)
	ay := math.Max(dy, 0)
	outside := math.Sqrt(ax*ax+ay*ay) - r
	inside := math.Min(math.Max(dx, dy), 0) - r
	return outside + inside + r
}

func segmentDist(px, py, x1, y1, x2, y2 float64) float64 {
	dx := x2 - x1
	dy := y2 - y1
	lenSq := dx*dx + dy*dy
	if lenSq == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	projX := x1 + t*dx
	projY := y1 + t*dy
	return math.Hypot(px-projX, py-projY)
}

func blend(c1, c2 color.RGBA, alpha uint8) color.RGBA {
	a := float64(alpha) / 255.0
	inv := 1.0 - a
	return color.RGBA{
		R: uint8(float64(c1.R)*inv + float64(c2.R)*a),
		G: uint8(float64(c1.G)*inv + float64(c2.G)*a),
		B: uint8(float64(c1.B)*inv + float64(c2.B)*a),
		A: 0xff,
	}
}
