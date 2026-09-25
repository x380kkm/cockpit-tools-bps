package imagehost

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"testing"
)

// encodePNG 生成指定尺寸、带固定种子噪点的 PNG，压缩率接近真实截图；transparent 为真时左上角像素半透明。
func encodePNG(t *testing.T, width, height int, transparent bool) []byte {
	t.Helper()
	noise := rand.New(rand.NewSource(1))
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			jitter := uint8(noise.Intn(48))
			picture.SetNRGBA(x, y, color.NRGBA{uint8(x/6) + jitter, uint8(y/4) + jitter, 160 + jitter, 255})
		}
	}
	if transparent {
		picture.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 10})
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

//// high 细节尺寸：先限最长边 2048，再限最短边 768，范围内不变 [@x380kkm 2026-09-26] ////
func TestHighDetailSize(t *testing.T) {
	for _, c := range []struct{ w, h, wantW, wantH int }{
		{1440, 900, 1229, 768},
		{4096, 1024, 2048, 512},
		{600, 400, 600, 400},
		{900, 1440, 768, 1229},
	} {
		if w, h := highDetailSize(c.w, c.h); w != c.wantW || h != c.wantH {
			t.Fatalf("%dx%d -> %dx%d, want %dx%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

//// 大截图缩到 high 尺寸并转 JPEG，透明图保留 PNG，无法处理的类型原样返回 [@x380kkm 2026-09-26] ////
func TestShrink(t *testing.T) {
	screenshot := encodePNG(t, 1440, 900, false)
	shrunk, mediaType := Shrink(screenshot, "image/png")
	if mediaType != "image/jpeg" || len(shrunk) >= len(screenshot) {
		t.Fatalf("opaque screenshot must become a smaller JPEG: %s, %d -> %d bytes", mediaType, len(screenshot), len(shrunk))
	}
	if config, err := jpeg.DecodeConfig(bytes.NewReader(shrunk)); err != nil || config.Width != 1229 || config.Height != 768 {
		t.Fatalf("shrunk screenshot has the wrong size: %+v, %v", config, err)
	}
	transparent := encodePNG(t, 1440, 900, true)
	if shrunk, mediaType := Shrink(transparent, "image/png"); mediaType != "image/png" || len(shrunk) >= len(transparent) {
		t.Fatalf("transparent image must stay a smaller PNG: %s", mediaType)
	}
	gif := []byte("GIF89a....")
	if shrunk, mediaType := Shrink(gif, "image/gif"); mediaType != "image/gif" || !bytes.Equal(shrunk, gif) {
		t.Fatal("unsupported types must pass through unchanged")
	}
	if shrunk, mediaType := Shrink([]byte("not a png"), "image/png"); mediaType != "image/png" || string(shrunk) != "not a png" {
		t.Fatal("undecodable images must pass through unchanged")
	}
}

//// 同一张原图第二次发布时复用已缩小的落盘条目 [@x380kkm 2026-09-26] ////
func TestPublishReusesShrunkImage(t *testing.T) {
	host, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host.SetBaseURL("https://example.trycloudflare.com")
	screenshot := encodePNG(t, 1440, 900, false)
	first, err := host.Publish(screenshot, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id := host.shrunk[contentID(screenshot)]
	stored, err := os.ReadFile(host.dir + string(os.PathSeparator) + id)
	if err != nil || len(stored) >= len(screenshot) {
		t.Fatalf("published file must be the shrunk image: %d bytes, %v", len(stored), err)
	}
	second, err := host.Publish(screenshot, "image/png")
	if err != nil || first != second {
		t.Fatalf("republishing the same image must reuse its link: %q vs %q, %v", first, second, err)
	}
}
