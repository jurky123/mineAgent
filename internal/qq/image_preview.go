package qq

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"mineagent/internal/tools"
)

// Decode only bounded images; preserve original bytes independently. GIF is a
// first-frame preview, not a conversion of the persisted animation.
func (b *inputBundle) imagePreview(ctx context.Context, path string, data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil && cfg.Width > 0 && cfg.Height > 0 && int64(cfg.Width)*int64(cfg.Height) <= 16_000_000 {
		src, _, e := image.Decode(bytes.NewReader(data))
		if e != nil {
			return nil, e
		}
		w, h := cfg.Width, cfg.Height
		if w > 1600 || h > 1600 {
			if w >= h {
				h = max(1, h*1600/w)
				w = 1600
			} else {
				w = max(1, w*1600/h)
				h = 1600
			}
		}
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		bounds := src.Bounds()
		for y := 0; y < h; y++ {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			for x := 0; x < w; x++ {
				r, g, bl, a := src.At(bounds.Min.X+x*cfg.Width/w, bounds.Min.Y+y*cfg.Height/h).RGBA()
				dst.SetRGBA(x, y, color.RGBA{uint8((r + 65535 - a) >> 8), uint8((g + 65535 - a) >> 8), uint8((bl + 65535 - a) >> 8), 255})
			}
		}
		var out bytes.Buffer
		if e := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80}); e != nil {
			return nil, e
		}
		if out.Len() > 4<<20 {
			return nil, fmt.Errorf("preview too large")
		}
		return out.Bytes(), nil
	}
	// Formats unsupported by Go (e.g. WebP) or oversized dimensions are decoded
	// only in the existing parser sandbox with memory/CPU/file limits.
	out := path + "-preview.jpg"
	_, err = tools.RunInputParser(ctx, b.root, "ffmpeg", "-nostdin", "-v", "error", "-threads", "1", "-protocol_whitelist", "file", "-i", path, "-frames:v", "1", "-vf", "scale=1600:1600:force_original_aspect_ratio=decrease", "-q:v", "4", "-threads", "1", "-y", out)
	if err != nil {
		return nil, err
	}
	result, err := os.ReadFile(filepath.Join(b.root, out))
	if err != nil || len(result) > 4<<20 {
		return nil, fmt.Errorf("preview unavailable")
	}
	return result, nil
}
