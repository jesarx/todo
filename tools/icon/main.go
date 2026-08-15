// Genera los PNG del ícono (los que usa el celular al instalar la app) a
// partir del mismo dibujo que static/favicon.svg: una casilla con una palomita.
// No hace falta ninguna herramienta externa:
//
//	go run ./tools/icon
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

var (
	bg  = color.NRGBA{0x10, 0x10, 0x14, 0xff}
	acc = color.NRGBA{0x4c, 0xd3, 0xa5, 0xff}
)

type vec struct{ x, y float64 }

// distancia con signo a un rectángulo redondeado (negativa dentro)
func sdRoundRect(p, c vec, hw, hh, r float64) float64 {
	qx := math.Abs(p.x-c.x) - (hw - r)
	qy := math.Abs(p.y-c.y) - (hh - r)
	ox, oy := math.Max(qx, 0), math.Max(qy, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(qx, qy), 0) - r
}

// distancia a un segmento (para trazos con punta redonda)
func sdSegment(p, a, b vec) float64 {
	pax, pay := p.x-a.x, p.y-a.y
	bax, bay := b.x-a.x, b.y-a.y
	h := math.Min(1, math.Max(0, (pax*bax+pay*bay)/(bax*bax+bay*bay)))
	return math.Hypot(pax-bax*h, pay-bay*h)
}

// cobertura del borde: 1 dentro, 0 fuera, degradado de un pixel (antialias)
func cover(d, px float64) float64 {
	return math.Min(1, math.Max(0, 0.5-d/px))
}

func over(dst color.NRGBA, src color.NRGBA, a float64) color.NRGBA {
	a *= float64(src.A) / 255
	f := func(s, d uint8) uint8 { return uint8(math.Round(float64(s)*a + float64(d)*(1-a))) }
	return color.NRGBA{f(src.R, dst.R), f(src.G, dst.G), f(src.B, dst.B), 0xff}
}

// draw dibuja el logo en un lienzo de size×size. El dibujo vive en un espacio
// de 100×100 (el mismo del SVG). Con maskable el fondo llena todo el cuadro y
// el logo se encoge para sobrevivir al recorte circular de Android.
func draw(size int, maskable bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	px := 100 / float64(size) // un pixel medido en unidades del dibujo
	scale, center := 1.0, vec{50, 50}
	if maskable {
		scale = 0.62
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			p := vec{(float64(x) + 0.5) * px, (float64(y) + 0.5) * px}
			c := bg
			tile := 1.0 // recorte del mosaico: opaco dentro, transparente fuera
			if !maskable {
				tile = cover(sdRoundRect(p, center, 50, 50, 24), px)
			}
			// el logo se evalúa en su propio espacio, encogido si hace falta
			q := vec{center.x + (p.x-center.x)/scale, center.y + (p.y-center.y)/scale}
			e := px / scale

			// casilla: rectángulo redondeado hueco de 6 de grosor
			box := math.Abs(sdRoundRect(q, center, 28, 28, 15)) - 3
			if a := cover(box, e); a > 0 {
				c = over(c, acc, a*0.35)
			}
			// palomita: dos segmentos de 9 de grosor con punta redonda
			check := math.Min(
				sdSegment(q, vec{32, 52}, vec{45, 65}),
				sdSegment(q, vec{45, 65}, vec{72, 32})) - 4.5
			if a := cover(check, e); a > 0 {
				c = over(c, acc, a)
			}
			c.A = uint8(math.Round(tile * 255))
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func write(path string, img *image.NRGBA) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Println("escrito", path)
}

func main() {
	write("static/icon-192.png", draw(192, false))
	write("static/icon-512.png", draw(512, false))
	write("static/icon-maskable-512.png", draw(512, true))
}
