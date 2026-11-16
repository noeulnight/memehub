package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	_ "golang.org/x/image/webp"
)

// Double joins two 200px cells without changing their aspect ratios.
func Double(ctx context.Context, inputs [2][]byte) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "memehub-double-*")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	var paths [2]string
	animated, duration := false, 0.1
	for i, data := range inputs {
		cfg, format, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || len(data) > 10<<20 || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20_000_000 {
			return nil, "", errors.New("invalid_double_image")
		}
		paths[i] = filepath.Join(dir, fmt.Sprintf("%d.%s", i, format))
		if err = os.WriteFile(paths[i], data, 0600); err != nil {
			return nil, "", err
		}
		if format == "gif" {
			animated = true
			raw, e := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-ignore_loop", "1", "-show_entries", "format=duration", "-of", "json", paths[i]).Output()
			if e != nil {
				return nil, "", errors.New("double_generation_failed")
			}
			var probe struct{ Format struct{ Duration string } }
			if json.Unmarshal(raw, &probe) != nil {
				return nil, "", errors.New("double_generation_failed")
			}
			seconds, e := strconv.ParseFloat(probe.Format.Duration, 64)
			if e != nil || seconds <= 0 || seconds > 30 {
				return nil, "", errors.New("double_animation_duration_unsupported")
			}
			duration = max(duration, seconds)
		}
	}
	ext, mime := "png", "image/png"
	if animated {
		ext, mime = "gif", "image/gif"
	}
	output := filepath.Join(dir, "double."+ext)
	args := []string{"-v", "error", "-nostdin", "-threads", "1"}
	for _, path := range paths {
		args = append(args, "-stream_loop", "-1", "-t", strconv.FormatFloat(duration, 'f', 3, 64), "-i", path)
	}
	filter := "[0:v]fps=20,scale=200:200:force_original_aspect_ratio=decrease,format=rgba,pad=200:200:(ow-iw)/2:(oh-ih)/2:color=black@0,setsar=1[a];[1:v]fps=20,scale=200:200:force_original_aspect_ratio=decrease,format=rgba,pad=200:200:(ow-iw)/2:(oh-ih)/2:color=black@0,setsar=1[b];[a][b]hstack=inputs=2:shortest=1"
	if animated {
		// shortcut: 20fps and one longest input cycle; revisit for long-form animation editing.
		filter += ",split[p][v];[p]palettegen=reserve_transparent=1[palette];[v][palette]paletteuse"
	}
	args = append(args, "-filter_complex_threads", "1", "-filter_complex", filter)
	if animated {
		args = append(args, "-loop", "0")
	} else {
		args = append(args, "-frames:v", "1")
	}
	args = append(args, "-fs", "10485760", output)
	if err = exec.CommandContext(ctx, "ffmpeg", args...).Run(); err != nil {
		return nil, "", errors.New("double_generation_failed")
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 || info.Size() >= 10<<20 {
		return nil, "", errors.New("double_generation_failed")
	}
	data, err := os.ReadFile(output)
	return data, mime, err
}
