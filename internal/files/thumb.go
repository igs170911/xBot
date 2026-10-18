package files

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	_ "image/gif" // 縮圖支援 GIF
	"image/jpeg"
	"image/png"

	"xbot/internal/limits"
)

// ErrNoThumb 表示這張圖做不了縮圖（格式不支援或太大）；畫面改用圖示。
var ErrNoThumb = errors.New("這張圖做不了縮圖")

// Thumbnail 把 PNG、JPEG、GIF 縮成長邊 size 像素以內（不放大）。有透明的輸出 PNG，其他輸出 JPEG。
// 先看尺寸：超過 limits.ThumbPixelsMax 的不解碼（擋解壓縮炸彈）。
func Thumbnail(data []byte, size int) ([]byte, string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > limits.ThumbPixelsMax {
		return nil, "", ErrNoThumb
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrNoThumb
	}
	dst := scale(src, size)
	var buf bytes.Buffer
	if opaque(dst) {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82})
		return buf.Bytes(), "image/jpeg", err
	}
	err = png.Encode(&buf, dst)
	return buf.Bytes(), "image/png", err
}

// scale 用每個目的像素範圍內最多 4×4 個取樣點的平均縮小（夠快，大圖也不會整張轉換）。
func scale(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if w > size || h > size {
		if w >= h {
			dw, dh = size, max(1, h*size/w)
		} else {
			dw, dh = max(1, w*size/h), size
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		y0, y1 := b.Min.Y+y*h/dh, b.Min.Y+(y+1)*h/dh
		for x := range dw {
			x0, x1 := b.Min.X+x*w/dw, b.Min.X+(x+1)*w/dw
			var r, g, bl, a, n uint32
			for _, sy := range samples(y0, y1) {
				for _, sx := range samples(x0, x1) {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					r, g, bl, a = r+uint32(c.R), g+uint32(c.G), bl+uint32(c.B), a+uint32(c.A)
					n++
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{uint8(r / n), uint8(g / n), uint8(bl / n), uint8(a / n)})
		}
	}
	return dst
}

func samples(lo, hi int) []int {
	if hi <= lo {
		return []int{lo}
	}
	n := min(hi-lo, 4)
	out := make([]int, n)
	for i := range n {
		out[i] = lo + (2*i+1)*(hi-lo)/(2*n)
	}
	return out
}

func opaque(m *image.NRGBA) bool {
	for i := 3; i < len(m.Pix); i += 4 {
		if m.Pix[i] != 0xff {
			return false
		}
	}
	return true
}
