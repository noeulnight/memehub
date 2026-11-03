package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"github.com/google/uuid"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type demoSample struct {
	slug, title, top, bottom, footer string
	background, face                 color.RGBA
	panic                            bool
	tags                             []string
}

// Original, offline-generated artwork: no remote assets or licensing dependencies.
var demoSamples = []demoSample{
	{slug: "works-on-my-machine", title: "Works on my machine", top: "IT WORKS ON MY MACHINE", bottom: "PRODUCTION WOULD LIKE A WORD.", footer: "localhost: 200   production: 500", background: color.RGBA{36, 48, 80, 255}, face: color.RGBA{93, 218, 196, 255}, tags: []string{"demo", "programming", "production"}},
	{slug: "one-small-fix", title: "Just one small fix", top: "JUST ONE SMALL FIX", bottom: "47 FILES CHANGED. 3 HOURS LATER.", footer: "+ 1,204 lines   - 3 lines", background: color.RGBA{84, 40, 76, 255}, face: color.RGBA{251, 161, 184, 255}, panic: true, tags: []string{"demo", "programming", "debugging"}},
	{slug: "friday-deploy", title: "Friday deployment", top: "DEPLOYING ON FRIDAY AT 5 PM", bottom: "WHAT COULD POSSIBLY GO WRONG?", footer: "[!] 28 new alerts", background: color.RGBA{87, 48, 30, 255}, face: color.RGBA{255, 191, 100, 255}, panic: true, tags: []string{"demo", "deployment", "friday"}},
	{slug: "tests-pass", title: "All tests passed", top: "ALL TESTS PASSED", bottom: "THERE WERE NO TESTS.", footer: "Tests: 0 passed, 0 failed. Success!", background: color.RGBA{40, 64, 53, 255}, face: color.RGBA{180, 220, 130, 255}, tags: []string{"demo", "programming", "testing"}},
}

func demoSampleID(slug string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("memehub/demo/v1/"+slug))
}

// SeedDemo adds missing fixtures, preserving edits and soft-deleted samples.
// It is called by the executable, not New, so handler tests can start with an empty DB.
func (s *Server) SeedDemo(ctx context.Context) error {
	if !s.cfg.DemoMode {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('memehub-demo-seed-v1',0))"); err != nil {
		return err
	}
	userID, err := s.demoUser(ctx, tx)
	if err != nil {
		return err
	}
	for _, sample := range demoSamples {
		id := demoSampleID(sample.slug)
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM memes WHERE id=$1)", id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		data, err := renderDemoSample(sample)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		width, height, thumbnail, err := prepareImage(data, "image/png", hash, int64(len(data)))
		if err != nil {
			return err
		}
		originalKey, thumbnailKey := "originals/"+id.String()+".png", "thumbnails/"+id.String()+".png"
		if err := s.store.Put(ctx, originalKey, "image/png", data); err != nil {
			return err
		}
		if err := s.store.Put(ctx, thumbnailKey, "image/png", thumbnail); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO memes(id,created_by,content_type,title,description,extracted_text,source_name,original_filename,original_object_key,thumbnail_object_key,mime_type,size_bytes,width,height,sha256,status,analysis_status)
 VALUES($1,$2,'image',$3,$4,$5,'MemeHub demo',$6,$7,$8,'image/png',$9,$10,$11,$12,'ready','not_requested')`, id, userID, sample.title, "Original sample artwork generated locally for demo mode.", sample.top+"\n"+sample.bottom+"\n"+sample.footer, sample.slug+".png", originalKey, thumbnailKey, len(data), width, height, hash)
		if err != nil {
			return err
		}
		if err := saveTags(ctx, tx, id, sample.tags); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func renderDemoSample(sample demoSample) ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, 400, 300))
	fill := func(rect image.Rectangle, c color.Color) {
		draw.Draw(canvas, rect, image.NewUniform(c), image.Point{}, draw.Src)
	}
	fill(canvas.Bounds(), sample.background)
	ink := color.RGBA{23, 29, 43, 255}
	white := color.RGBA{250, 250, 245, 255}
	text := func(y int, value string, c color.Color) {
		d := font.Drawer{Dst: canvas, Src: image.NewUniform(c), Face: basicfont.Face7x13}
		d.Dot = fixed.P((400-d.MeasureString(value).Ceil())/2, y)
		d.DrawString(value)
	}
	circle := func(cx, cy, radius int, c color.Color) {
		for y := -radius; y <= radius; y++ {
			for x := -radius; x <= radius; x++ {
				if x*x+y*y <= radius*radius {
					canvas.Set(cx+x, cy+y, c)
				}
			}
		}
	}
	text(24, "MEMEHUB / DEMO", white)
	text(55, sample.top, white)
	// A deliberately simple pixel-art cat reacting to the caption.
	fill(image.Rect(141, 92, 170, 137), ink)
	fill(image.Rect(230, 92, 259, 137), ink)
	circle(200, 155, 66, ink)
	circle(200, 155, 60, sample.face)
	circle(177, 146, 13, white)
	circle(223, 146, 13, white)
	circle(180, 148, 6, ink)
	circle(220, 148, 6, ink)
	if sample.panic {
		circle(200, 183, 15, ink)
		circle(200, 186, 8, color.RGBA{235, 108, 114, 255})
		circle(253, 119, 7, color.RGBA{110, 204, 255, 255})
	} else {
		fill(image.Rect(180, 179, 220, 186), ink)
		fill(image.Rect(185, 179, 215, 182), white)
	}
	text(248, sample.bottom, white)
	fill(image.Rect(0, 268, 400, 300), ink)
	text(288, sample.footer, white)
	out := image.NewRGBA(image.Rect(0, 0, 800, 600))
	xdraw.NearestNeighbor.Scale(out, out.Bounds(), canvas, canvas.Bounds(), draw.Src, nil)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, out); err != nil {
		return nil, fmt.Errorf("encode demo sample: %w", err)
	}
	return encoded.Bytes(), nil
}
