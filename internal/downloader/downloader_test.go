package downloader

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"第 1 话":         "第 1 话",
		"a/b:c*d?e":     "a_b_c_d_e",
		"  trailing.  ": "trailing",
		"":              "untitled",
	}
	for input, want := range cases {
		if got := SafeName(input); got != want {
			t.Fatalf("SafeName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestImageExtension(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	if got := imageExtension(jpeg, "https://example.com/a.webp"); got != ".jpg" {
		t.Fatalf("jpg detection = %q", got)
	}
	if got := imageExtension([]byte("unknown"), "https://example.com/a.PNG?x=1"); got != ".png" {
		t.Fatalf("png detection = %q", got)
	}
}

func TestWriteZip(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "001.jpg")
	second := filepath.Join(dir, "002.jpg")
	if err := os.WriteFile(first, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "chapter.cbz")
	if err := writeZip(output, []pageEntry{{name: "001.jpg", path: first}, {name: "002.jpg", path: second}}); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 2 || reader.File[0].Name != "001.jpg" || reader.File[1].Name != "002.jpg" {
		t.Fatalf("unexpected entries: %#v", reader.File)
	}
}

func TestDescrambleRotatesStrips(t *testing.T) {
	const width, height = 4, 4
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.Set(x, y, color.RGBA{R: uint8(y * 40), A: 255})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, source); err != nil {
		t.Fatal(err)
	}
	// parts = 2 swaps the two halves.
	decodedScrambled := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			mirror := (y + height/2) % height
			decodedScrambled.Set(x, y, source.At(x, mirror))
		}
	}
	scrambled := &bytes.Buffer{}
	if err := png.Encode(scrambled, decodedScrambled); err != nil {
		t.Fatal(err)
	}
	restored, err := Descramble(scrambled.Bytes(), 2)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeImage(restored)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			wantR, _, _, _ := source.At(x, y).RGBA()
			gotR, _, _, _ := decoded.At(x, y).RGBA()
			if wantR != gotR {
				t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, gotR, wantR)
			}
		}
	}
}
