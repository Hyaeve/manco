package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
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

// TestDescrambleMatchesJMReferenceRemainderEncoding mirrors the Kototoro/Kotatsu
// JM descrambler so a non-divisible height cannot silently rotate the wrong
// block. The reference reads blocks bottom-up and gives the remainder to the
// block that ends up at the top; Descramble iterates top-down instead.
func TestDescrambleMatchesJMReferenceRemainderEncoding(t *testing.T) {
	const width, height, parts = 3, 100, 3
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.Set(x, y, color.RGBA{R: uint8(y), G: uint8(y / 2), B: uint8(y / 3), A: 255})
		}
	}
	scrambled := image.NewRGBA(image.Rect(0, 0, width, height))
	blockSize := height / parts
	remainder := height % parts
	y := 0
	for index := parts - 1; index >= 0; index-- {
		start := index * blockSize
		end := start + blockSize
		if index == parts-1 {
			end += remainder
		}
		copied := end - start
		draw.Draw(
			scrambled,
			image.Rect(0, y, width, y+copied),
			source,
			image.Pt(0, start),
			draw.Src,
		)
		y += copied
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, scrambled); err != nil {
		t.Fatal(err)
	}
	restored, err := Descramble(buffer.Bytes(), parts)
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

func TestBookFileExtension(t *testing.T) {
	cases := map[string]string{
		"Test Book.epub":     ".epub",
		"book.pdf":           ".pdf",
		"  spaced.MOBI  ":    ".mobi",
		"dir/sub/novel.AZW3": ".azw3",
		"no-extension":       ".bin",
		"weird.na<me":        ".bin",
	}
	for input, want := range cases {
		if got := bookFileExtension(input); got != want {
			t.Fatalf("bookFileExtension(%q) = %q, want %q", input, got, want)
		}
	}
}

// 禁漫等来源的图床按 User-Agent 做防盗链，图片请求必须优先使用来源账号
// 里配置的 UA，否则浏览与封面正常但下载章节会失败。
func TestFetchImageUsesAccountUserAgent(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("User-Agent"))
		if r.Header.Get("User-Agent") != "custom-agent" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-data"))
	}))
	defer server.Close()

	data, err := fetchImage(context.Background(), server.Client(), source.Account{UserAgent: "custom-agent"}, model.Page{URL: server.URL})
	if err != nil {
		t.Fatalf("fetchImage: %v", err)
	}
	if string(data) != "png-data" {
		t.Fatalf("unexpected data %q", data)
	}
	if len(seen) == 0 || seen[0] != "custom-agent" {
		t.Fatalf("expected account UA first, got %v", seen)
	}
}

func TestFetchImageFallsBackToDefaultUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "custom-agent" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("fallback-data"))
	}))
	defer server.Close()

	data, err := fetchImage(context.Background(), server.Client(), source.Account{UserAgent: "custom-agent"}, model.Page{URL: server.URL})
	if err != nil {
		t.Fatalf("fetchImage: %v", err)
	}
	if string(data) != "fallback-data" {
		t.Fatalf("unexpected data %q", data)
	}
}
