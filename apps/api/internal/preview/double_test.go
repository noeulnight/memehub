package preview

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os/exec"
	"testing"
)

func TestDouble(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	var red, blue, animation bytes.Buffer
	r := image.NewRGBA(image.Rect(0, 0, 200, 200))
	b := image.NewRGBA(r.Bounds())
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			r.Set(x, y, color.RGBA{R: 255, A: 255})
			b.Set(x, y, color.RGBA{B: 255, A: 255})
		}
	}
	if err := png.Encode(&red, r); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&blue, b); err != nil {
		t.Fatal(err)
	}
	palette := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}}
	f1, f2 := image.NewPaletted(r.Bounds(), palette), image.NewPaletted(r.Bounds(), palette)
	for i := range f2.Pix {
		f2.Pix[i] = 1
	}
	if err := gif.EncodeAll(&animation, &gif.GIF{Image: []*image.Paletted{f1, f2}, Delay: []int{10, 20}, LoopCount: 0}); err != nil {
		t.Fatal(err)
	}
	t.Run("static order and dimensions", func(t *testing.T) {
		data, mime, err := Double(context.Background(), [2][]byte{red.Bytes(), blue.Bytes()})
		if err != nil || mime != "image/png" {
			t.Fatal(mime, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 200 {
			t.Fatal(img.Bounds())
		}
		r1, _, _, _ := img.At(100, 100).RGBA()
		_, _, b1, _ := img.At(300, 100).RGBA()
		if r1 != 65535 || b1 != 65535 {
			t.Fatal("order or pixels lost")
		}
	})
	for _, inputs := range [][2][]byte{{animation.Bytes(), blue.Bytes()}, {red.Bytes(), animation.Bytes()}, {animation.Bytes(), animation.Bytes()}} {
		t.Run("animation", func(t *testing.T) {
			data, mime, err := Double(context.Background(), inputs)
			if err != nil || mime != "image/gif" {
				t.Fatal(mime, err)
			}
			out, err := gif.DecodeAll(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Image) < 2 || out.Config.Width != 400 || out.Config.Height != 200 || out.LoopCount != 0 {
				t.Fatal("lost animation", out.Config, len(out.Image))
			}
			delay := 0
			for _, d := range out.Delay {
				delay += d
			}
			if delay < 25 || delay > 35 {
				t.Fatal("lost duration", delay)
			}
		})
	}
	if _, _, err := Double(context.Background(), [2][]byte{[]byte("invalid"), red.Bytes()}); err == nil {
		t.Fatal("accepted invalid image")
	}
	var long bytes.Buffer
	if err := gif.EncodeAll(&long, &gif.GIF{Image: []*image.Paletted{f1, f2}, Delay: []int{1600, 1600}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Double(context.Background(), [2][]byte{long.Bytes(), red.Bytes()}); err == nil || err.Error() != "double_animation_duration_unsupported" {
		t.Fatal("accepted an animation exceeding 30 seconds", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Double(ctx, [2][]byte{red.Bytes(), blue.Bytes()}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
