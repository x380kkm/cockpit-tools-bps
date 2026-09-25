package imagehost

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

const (
	// highDetailLongSide 与 highDetailShortSide 是 OpenAI 以 high 细节看图时的有效尺寸上限：
	// 先缩到最长边不超过 2048，再缩到最短边不超过 768。
	highDetailLongSide  = 2048
	highDetailShortSide = 768
	// shrinkJPEGQuality 是不透明图片重新编码为 JPEG 时的质量。
	shrinkJPEGQuality = 90
)

//// 把 PNG/JPEG 缩到 high 细节的有效尺寸，不透明图改用 JPEG；结果不更小或无法解码时原样返回 [@x380kkm 2026-09-26] ////
func Shrink(payload []byte, mediaType string) ([]byte, string) {
	var decoded image.Image
	var err error
	switch mediaType {
	case "image/png":
		decoded, err = png.Decode(bytes.NewReader(payload))
	case "image/jpeg":
		decoded, err = jpeg.Decode(bytes.NewReader(payload))
	default:
		return payload, mediaType
	}
	if err != nil {
		return payload, mediaType
	}
	width, height := highDetailSize(decoded.Bounds().Dx(), decoded.Bounds().Dy())
	resized := downscale(decoded, width, height)
	var encoded bytes.Buffer
	encodedType := "image/jpeg"
	if resized.Opaque() {
		err = jpeg.Encode(&encoded, resized, &jpeg.Options{Quality: shrinkJPEGQuality})
	} else {
		encodedType = "image/png"
		err = png.Encode(&encoded, resized)
	}
	if err != nil || encoded.Len() >= len(payload) {
		return payload, mediaType
	}
	return encoded.Bytes(), encodedType
}

// highDetailSize 按 high 细节规则计算缩放后的宽高，已在范围内的尺寸保持不变。
func highDetailSize(width, height int) (int, int) {
	scale := 1.0
	if long := max(width, height); long > highDetailLongSide {
		scale = float64(highDetailLongSide) / float64(long)
	}
	if short := float64(min(width, height)) * scale; short > highDetailShortSide {
		scale *= highDetailShortSide / short
	}
	return max(1, int(float64(width)*scale+0.5)), max(1, int(float64(height)*scale+0.5))
}

//// 用区域平均把图片缩到指定宽高 [@x380kkm 2026-09-26] ////
func downscale(source image.Image, width, height int) *image.RGBA {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		top := bounds.Min.Y + y*sourceHeight/height
		bottom := max(top+1, bounds.Min.Y+(y+1)*sourceHeight/height)
		for x := 0; x < width; x++ {
			left := bounds.Min.X + x*sourceWidth/width
			right := max(left+1, bounds.Min.X+(x+1)*sourceWidth/width)
			var red, green, blue, alpha, count uint64
			for sy := top; sy < bottom; sy++ {
				for sx := left; sx < right; sx++ {
					r, g, b, a := source.At(sx, sy).RGBA()
					red, green, blue, alpha = red+uint64(r), green+uint64(g), blue+uint64(b), alpha+uint64(a)
					count++
				}
			}
			// RGBA() 返回 16 位分量，右移 8 位得到 8 位通道值。
			target.SetRGBA(x, y, color.RGBA{uint8(red / count >> 8), uint8(green / count >> 8), uint8(blue / count >> 8), uint8(alpha / count >> 8)})
		}
	}
	return target
}
