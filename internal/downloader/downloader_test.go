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

func TestWriteHTTPImageAtomicStreamsResponseToFile(t *testing.T) {
	payload := bytes.Repeat([]byte("image-data-"), 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "page.bin")
	if err := writeHTTPImageAtomic(context.Background(), server.Client(), source.Account{}, model.Page{URL: server.URL}, target); err != nil {
		t.Fatalf("writeHTTPImageAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("streamed file length = %d, want %d", len(got), len(payload))
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists: %v", err)
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

func TestDescrambleMatchesReferenceDecoder(t *testing.T) {
	const width, height, parts = 4, 5, 2
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
	restored, err := Descramble(buffer.Bytes(), parts)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeImage(restored)
	if err != nil {
		t.Fatal(err)
	}
	want := referenceDescrambleImage(source, parts)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			wantR, _, _, _ := want.At(x, y).RGBA()
			gotR, _, _, _ := decoded.At(x, y).RGBA()
			if wantR != gotR {
				t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, gotR, wantR)
			}
		}
	}
}

// referenceDescrambleImage is a direct transcription of the Kototoro/Kotatsu
// JM descrambler, used so the production implementation is checked against the
// real strip layout (bottom-up traversal, remainder on the top source strip).
func referenceDescrambleImage(source image.Image, parts int) image.Image {
	bounds := source.Bounds()
	blockSize := bounds.Dy() / parts
	remainder := bounds.Dy() % parts
	result := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	y := 0
	for index := parts - 1; index >= 0; index-- {
		start := index * blockSize
		end := start + blockSize
		if index == parts-1 {
			end += remainder
		}
		height := end - start
		draw.Draw(result, image.Rect(0, y, bounds.Dx(), y+height), source, image.Pt(bounds.Min.X, bounds.Min.Y+start), draw.Src)
		y += height
	}
	return result
}

// referenceDecodePython is a line-by-line transcription of
// JMComic-Crawler-Python's JmImageTool.decode_and_save, which is the canonical
// reference for the 18comic strip layout.
func referenceDecodePython(source image.Image, parts int) image.Image {
	bounds := source.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	result := image.NewRGBA(image.Rect(0, 0, w, h))
	if parts <= 0 {
		draw.Draw(result, result.Bounds(), source, bounds.Min, draw.Src)
		return result
	}
	over := h % parts
	for i := 0; i < parts; i++ {
		move := h / parts
		ySrc := h - move*(i+1) - over
		yDst := move * i
		if i == 0 {
			move += over
		} else {
			yDst += over
		}
		if move <= 0 {
			continue
		}
		draw.Draw(
			result,
			image.Rect(0, yDst, w, yDst+move),
			source,
			image.Pt(bounds.Min.X, bounds.Min.Y+ySrc),
			draw.Src,
		)
	}
	return result
}

func TestDescrambleMatchesPythonReference(t *testing.T) {
	for _, height := range []int{2, 3, 5, 10, 11, 64, 99, 100, 101, 733, 1000, 1005, 2051} {
		for _, parts := range []int{2, 4, 6, 8, 10, 12, 14, 16, 18, 20} {
			if parts > height {
				continue
			}
			source := image.NewRGBA(image.Rect(0, 0, 3, height))
			for y := 0; y < height; y++ {
				for x := 0; x < 3; x++ {
					source.Set(x, y, color.RGBA{R: uint8(y % 251), G: uint8((y * 5) % 251), B: 3, A: 255})
				}
			}
			buffer := &bytes.Buffer{}
			if err := png.Encode(buffer, source); err != nil {
				t.Fatal(err)
			}
			restored, err := Descramble(buffer.Bytes(), parts)
			if err != nil {
				t.Fatalf("height=%d parts=%d: %v", height, parts, err)
			}
			decoded, err := decodeImage(restored)
			if err != nil {
				t.Fatal(err)
			}
			want := referenceDecodePython(source, parts)
			for y := 0; y < height; y++ {
				for x := 0; x < 3; x++ {
					wantR, wantG, wantB, _ := want.At(x, y).RGBA()
					gotR, gotG, gotB, _ := decoded.At(x, y).RGBA()
					if wantR != gotR || wantG != gotG || wantB != gotB {
						t.Fatalf("height=%d parts=%d pixel (%d,%d) got %d,%d,%d want %d,%d,%d", height, parts, x, y, gotR, gotG, gotB, wantR, wantG, wantB)
					}
				}
			}
		}
	}
}

func TestPlainMetadataTextStripsHTML(t *testing.T) {
	cases := map[string]string{
		"<p>简介内容</p>":                   "简介内容",
		"<p>第一段</p><p>第二段</p>":          "第一段\n第二段",
		"普通文本":                          "普通文本",
		"<div>带 <b>加粗</b> 的文本</div>":    "带 加粗 的文本",
		"a &amp; b &lt;tag&gt;":         "a & b <tag>",
		"<script>x()</script><p>正文</p>": "正文",
		"   ":                           "",
	}
	for input, want := range cases {
		if got := plainMetadataText(input); got != want {
			t.Fatalf("plainMetadataText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSeriesMetadataTreatsHTMLDescriptionAsStale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "作品")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":"1.0.1","metadata":{"type":"comicSeries","publisher":"","name":"作品",` +
		`"comicid":"1","year":0,"booktype":"comic","age_rating":"","comic_image":"cover.png",` +
		`"total_issues":1,"publication_run":"","status":"Continuing",` +
		`"description_text":"简介","description_formatted":"<p>简介</p>"}}`
	if err := os.WriteFile(filepath.Join(dir, "series.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyInfo := `<ComicInfo><Series>作品</Series><Summary>&lt;p&gt;简介&lt;/p&gt;</Summary></ComicInfo>`
	if err := os.WriteFile(filepath.Join(dir, "ComicInfo.xml"), []byte(legacyInfo), 0o644); err != nil {
		t.Fatal(err)
	}
	if seriesMetadataCurrent(dir, source.KindComic) {
		t.Fatal("metadata with HTML description should be treated as stale")
	}

	comic := model.Comic{SourceID: "stub", ID: "1", Title: "作品", Description: "<p>简介</p>"}
	item := &staticSource{detail: comic}
	if err := writeSeriesMetadataFiles(comic, item.Info(), dir); err != nil {
		t.Fatalf("writeSeriesMetadataFiles: %v", err)
	}
	if _, err := writeSeriesComicInfo(item, comic, dir); err != nil {
		t.Fatalf("writeSeriesComicInfo: %v", err)
	}
	if !seriesMetadataCurrent(dir, source.KindComic) {
		t.Fatal("metadata should be current after the rewrite")
	}
}

func TestMetadataWritersStripHTMLFromDescription(t *testing.T) {
	comic := model.Comic{
		SourceID:    "stub",
		ID:          "1",
		Title:       "测试作品",
		Description: "<p>第一段简介</p><p>第二段简介</p>",
	}
	item := &staticSource{detail: comic}
	dir := t.TempDir()
	if err := writeSeriesMetadataFiles(comic, item.Info(), dir); err != nil {
		t.Fatalf("writeSeriesMetadataFiles: %v", err)
	}
	seriesRaw, err := os.ReadFile(filepath.Join(dir, "series.json"))
	if err != nil {
		t.Fatalf("read series.json: %v", err)
	}
	if bytes.Contains(seriesRaw, []byte("<p>")) {
		t.Fatalf("series.json description still contains <p>: %s", seriesRaw)
	}
	if !bytes.Contains(seriesRaw, []byte("第一段简介")) {
		t.Fatalf("series.json lost the description text: %s", seriesRaw)
	}

	infoRaw, err := marshalComicInfo(seriesComicInfoDoc(item, comic))
	if err != nil {
		t.Fatalf("marshalComicInfo: %v", err)
	}
	for _, bad := range []string{"<p>", "&lt;p&gt;", "</p>"} {
		if bytes.Contains(infoRaw, []byte(bad)) {
			t.Fatalf("ComicInfo.xml summary still contains %q: %s", bad, infoRaw)
		}
	}
	if !bytes.Contains(infoRaw, []byte("第一段简介")) {
		t.Fatalf("ComicInfo.xml lost the description text: %s", infoRaw)
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

func TestSourceFileExtensionPrefersSourceMetadata(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		filename    string
		address     string
		sample      []byte
		want        string
	}{
		{name: "mime video", contentType: "video/mp4", want: ".mp4"},
		{name: "filename wins", contentType: "application/octet-stream", filename: "movie.mkv", want: ".mkv"},
		{name: "epub", contentType: "application/epub+zip", want: ".epub"},
		{name: "pdf magic", contentType: "application/octet-stream", sample: []byte("%PDF-1.7"), want: ".pdf"},
		{name: "matroska magic", contentType: "application/octet-stream", sample: []byte{0x1A, 0x45, 0xDF, 0xA3}, want: ".mkv"},
		{name: "url fallback", address: "https://example.test/media/movie.webm", sample: []byte("data"), want: ".webm"},
		{name: "unknown", contentType: "application/octet-stream", sample: []byte("data"), want: ".bin"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := sourceFileExtension(test.contentType, test.filename, test.address, test.sample); got != test.want {
				t.Fatalf("sourceFileExtension() = %q, want %q", got, test.want)
			}
		})
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
