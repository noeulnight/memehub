package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Probe a local file only. Playlists and remote media references cannot open network protocols.
func prepareAudio(ctx context.Context, data []byte, mime, hash string, size int64) error {
	sum := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(sum[:]) != hash {
		return fmt.Errorf("invalid_audio_integrity")
	}
	file, err := os.CreateTemp("", "memehub-audio-*")
	if err != nil {
		return fmt.Errorf("audio_validation_unavailable")
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("audio_validation_unavailable")
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("audio_validation_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mp3,wav,ogg,mov", "-show_entries", "format=format_name,duration:stream=codec_type,codec_name", "-of", "json", file.Name())
	output, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			return fmt.Errorf("audio_validation_unavailable")
		}
		return fmt.Errorf("invalid_audio")
	}
	var result struct {
		Streams []struct {
			Type  string `json:"codec_type"`
			Codec string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(output, &result) != nil || len(result.Streams) != 1 || result.Streams[0].Type != "audio" || result.Streams[0].Codec == "" {
		return fmt.Errorf("invalid_audio")
	}
	duration, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return fmt.Errorf("invalid_audio")
	}
	codec := result.Streams[0].Codec
	supported := (mime == "audio/mpeg" && codec == "mp3") || (mime == "audio/wav" && strings.HasPrefix(codec, "pcm_")) || (mime == "audio/ogg" && (codec == "vorbis" || codec == "opus")) || (mime == "audio/mp4" && codec == "aac")
	if !supported {
		return fmt.Errorf("unsupported_audio_codec")
	}
	expected := map[string]string{"audio/mpeg": "mp3", "audio/wav": "wav", "audio/ogg": "ogg", "audio/mp4": "mp4"}[mime]
	if expected == "" {
		return fmt.Errorf("unsupported_audio_type")
	}
	for _, format := range strings.Split(result.Format.Name, ",") {
		if format == expected {
			return nil
		}
	}
	return fmt.Errorf("audio_mime_mismatch")
}
