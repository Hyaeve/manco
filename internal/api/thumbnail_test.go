package api

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestThumbnailRejectsHugeImageBeforePixelDecode(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	binary.BigEndian.PutUint32(data[16:20], 20000)
	binary.BigEndian.PutUint32(data[20:24], 20000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if _, err := buildThumbnailFromReader(bytes.NewReader(data), 240); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("huge declared image dimensions were not rejected: %v", err)
	}
}

func TestBuildThumbnailFromReaderScalesDown(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	for x := 0; x < 800; x++ {
		for y := 0; y < 1200; y++ {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("encode source: %v", err)
	}

	thumb, err := buildThumbnailFromReader(bytes.NewReader(encoded.Bytes()), 200)
	if err != nil {
		t.Fatalf("buildThumbnailFromReader() error = %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if got, want := decoded.Bounds().Dx(), 200; got != want {
		t.Fatalf("缩略图宽度 = %d, want %d", got, want)
	}
	if got, want := decoded.Bounds().Dy(), 300; got != want {
		t.Fatalf("缩略图高度 = %d, want %d（等比缩放）", got, want)
	}

	// 原图本来就不比目标宽时返回错误，调用方会直接回退到原图。
	if _, err := buildThumbnailFromReader(bytes.NewReader(encoded.Bytes()), 2000); err == nil {
		t.Fatal("原图更窄时应返回错误以便回退原图")
	}
	// 不是图片的字节流同样返回错误，不会让请求失败。
	if _, err := buildThumbnailFromReader(bytes.NewReader([]byte("not an image")), 320); err == nil {
		t.Fatal("非图片内容应返回错误")
	}
}
