package downloader

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	nethtml "golang.org/x/net/html"
)

// epubChapter is one logical chapter in a generated book.
type epubChapter struct {
	Title string
	HTML  string
}

type epubImage struct {
	name      string
	path      string
	mediaType string
}

// writeBookCBZ packages generated book chapters as XHTML pages plus their
// illustrations. Native EPUB and PDF files are left unchanged by callers.
func writeBookCBZ(path string, item source.Source, book model.Comic, chapterTitle string, chapterOrder float64, chapters []epubChapter, images []pageEntry) error {
	if len(chapters) == 0 && len(images) == 0 {
		return fmt.Errorf("CBZ 至少需要一个页面")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	normalizedImages, err := normalizeEPUBImages(images)
	if err != nil {
		return err
	}
	chapterBodies := make([]string, len(chapters))
	for index, chapter := range chapters {
		body, normalizeErr := normalizeXHTMLFragment(chapter.HTML)
		if normalizeErr != nil {
			return fmt.Errorf("处理章节 %q 失败: %w", chapter.Title, normalizeErr)
		}
		chapterBodies[index] = body
	}
	infoPayload, err := marshalComicInfo(chapterComicInfo(
		item,
		book,
		chapterTitle,
		chapterOrder,
		len(chapters)+len(normalizedImages),
	))
	if err != nil {
		return err
	}

	temp := path + ".part"
	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	closeWithCleanup := func(cause error) error {
		_ = writer.Close()
		_ = file.Close()
		_ = os.Remove(temp)
		return cause
	}
	write := func(name string, data []byte) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()}
		handle, createErr := writer.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		_, writeErr := handle.Write(data)
		return writeErr
	}
	writePath := func(image epubImage) error {
		header := &zip.FileHeader{Name: image.name, Method: zip.Deflate, Modified: time.Now()}
		handle, createErr := writer.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		sourceFile, openErr := os.Open(image.path)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(handle, sourceFile)
		closeErr := sourceFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}

	for index, chapter := range chapters {
		document := buildEPUBChapterDocument(chapter.Title, chapterBodies[index])
		if err := write(fmt.Sprintf("%04d.xhtml", index+1), []byte(document)); err != nil {
			return closeWithCleanup(err)
		}
	}
	for _, image := range normalizedImages {
		if err := writePath(image); err != nil {
			return closeWithCleanup(err)
		}
	}
	if err := write("style.css", []byte(epubStylesheet)); err != nil {
		return closeWithCleanup(err)
	}
	if err := write("ComicInfo.xml", infoPayload); err != nil {
		return closeWithCleanup(err)
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// writeEPUB creates an EPUB 3 archive. The archive is written to a temporary
// .part file first so a failed download never becomes visible as a complete
// book.
func writeEPUB(path string, book model.Comic, sourceURL string, chapters []epubChapter, images []pageEntry) error {
	if len(chapters) == 0 {
		return fmt.Errorf("EPUB 至少需要一个章节")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	normalizedImages, err := normalizeEPUBImages(images)
	if err != nil {
		return err
	}
	chapterFiles := make([]string, 0, len(chapters))
	for index := range chapters {
		chapterFiles = append(chapterFiles, fmt.Sprintf("OEBPS/chapter-%04d.xhtml", index+1))
	}
	chapterBodies := make([]string, len(chapters))
	for index, chapter := range chapters {
		body, err := normalizeXHTMLFragment(chapter.HTML)
		if err != nil {
			return fmt.Errorf("处理章节 %q 失败: %w", chapter.Title, err)
		}
		chapterBodies[index] = body
	}

	temp := path + ".part"
	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	closeWithCleanup := func(cause error) error {
		_ = writer.Close()
		_ = file.Close()
		_ = os.Remove(temp)
		return cause
	}

	write := func(name string, data []byte, method uint16) error {
		header := &zip.FileHeader{Name: name, Method: method}
		if name != "mimetype" {
			header.Modified = time.Now()
		}
		handle, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = handle.Write(data)
		return err
	}
	writePath := func(image epubImage) error {
		header := &zip.FileHeader{Name: "OEBPS/" + image.name, Method: zip.Deflate, Modified: time.Now()}
		handle, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		source, err := os.Open(image.path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(handle, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}

	if err := write("mimetype", []byte("application/epub+zip"), zip.Store); err != nil {
		return closeWithCleanup(err)
	}
	if err := write("META-INF/container.xml", []byte(epubContainerXML), zip.Deflate); err != nil {
		return closeWithCleanup(err)
	}
	if err := write("OEBPS/toc.ncx", []byte(buildEPUBNCX(book, chapters, chapterFiles)), zip.Deflate); err != nil {
		return closeWithCleanup(err)
	}

	for index, chapter := range chapters {
		document := buildEPUBChapterDocument(chapter.Title, chapterBodies[index])
		if err := write(chapterFiles[index], []byte(document), zip.Deflate); err != nil {
			return closeWithCleanup(err)
		}
	}
	for _, image := range normalizedImages {
		if err := writePath(image); err != nil {
			return closeWithCleanup(err)
		}
	}
	if err := write("OEBPS/nav.xhtml", []byte(buildEPUBNavigation(chapters, chapterFiles)), zip.Deflate); err != nil {
		return closeWithCleanup(err)
	}
	if err := write("OEBPS/style.css", []byte(epubStylesheet), zip.Deflate); err != nil {
		return closeWithCleanup(err)
	}
	if err := write("OEBPS/content.opf", []byte(buildEPUBPackage(book, sourceURL, chapters, chapterFiles, normalizedImages)), zip.Deflate); err != nil {
		return closeWithCleanup(err)
	}

	if err := writer.Close(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func normalizeEPUBImages(images []pageEntry) ([]epubImage, error) {
	seen := map[string]int{}
	result := make([]epubImage, 0, len(images))
	for index, image := range images {
		if strings.TrimSpace(image.path) == "" {
			continue
		}
		name := strings.TrimSpace(strings.ReplaceAll(image.name, "\\", "/"))
		name = strings.TrimPrefix(name, "/")
		if name == "" || strings.Contains(name, "..") {
			name = fmt.Sprintf("images/image-%04d%s", index+1, filepath.Ext(image.path))
		}
		if !strings.HasPrefix(name, "images/") {
			name = "images/" + name
		}
		if seen[name] > 0 {
			extension := filepath.Ext(name)
			name = strings.TrimSuffix(name, extension) + fmt.Sprintf("-%d%s", seen[name]+1, extension)
		}
		seen[name]++
		if _, err := os.Stat(image.path); err != nil {
			return nil, err
		}
		result = append(result, epubImage{
			name:      name,
			path:      image.path,
			mediaType: epubMediaType(name),
		})
	}
	return result, nil
}

func epubMediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	default:
		if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); value != "" {
			return value
		}
		return "application/octet-stream"
	}
}

func buildEPUBChapterDocument(title, body string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head>
    <meta charset="utf-8"/>
    <title>` + escapeXML(title) + `</title>
    <link rel="stylesheet" type="text/css" href="style.css"/>
  </head>
  <body>
    <h1>` + escapeXML(title) + `</h1>
` + body + `
  </body>
</html>`
}

func buildEPUBNavigation(chapters []epubChapter, files []string) string {
	var links strings.Builder
	for index, chapter := range chapters {
		links.WriteString(`      <li><a href="`)
		links.WriteString(filepath.Base(files[index]))
		links.WriteString(`">`)
		links.WriteString(escapeXML(chapter.Title))
		links.WriteString("</a></li>\n")
	}
	return `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
  <head>
    <meta charset="utf-8"/>
    <title>目录</title>
  </head>
  <body>
    <nav epub:type="toc" id="toc">
      <h1>目录</h1>
      <ol>
` + links.String() + `      </ol>
    </nav>
  </body>
</html>`
}

func buildEPUBPackage(book model.Comic, sourceURL string, chapters []epubChapter, files []string, images []epubImage) string {
	identifier := epubIdentifier(book, chapters)
	var metadata strings.Builder
	metadata.WriteString(`    <dc:identifier id="book-id">`)
	metadata.WriteString(escapeXML(identifier))
	metadata.WriteString("</dc:identifier>\n")
	metadata.WriteString(`    <dc:title>`)
	metadata.WriteString(escapeXML(source.FirstNonEmpty(book.Title, book.ID, "未命名书籍")))
	metadata.WriteString("</dc:title>\n")
	metadata.WriteString(`    <dc:language>zh-Hans</dc:language>` + "\n")
	if value := strings.TrimSpace(book.Author); value != "" {
		metadata.WriteString(`    <dc:creator>`)
		metadata.WriteString(escapeXML(value))
		metadata.WriteString("</dc:creator>\n")
	}
	if value := strings.TrimSpace(book.Description); value != "" {
		metadata.WriteString(`    <dc:description>`)
		metadata.WriteString(escapeXML(value))
		metadata.WriteString("</dc:description>\n")
	}
	if value := strings.TrimSpace(sourceURL); value != "" {
		metadata.WriteString(`    <dc:source>`)
		metadata.WriteString(escapeXML(value))
		metadata.WriteString("</dc:source>\n")
	}
	metadata.WriteString(`    <meta property="dcterms:modified">`)
	metadata.WriteString(time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	metadata.WriteString("</meta>\n")

	var manifest strings.Builder
	manifest.WriteString(`    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n")
	manifest.WriteString(`    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n")
	manifest.WriteString(`    <item id="style" href="style.css" media-type="text/css"/>` + "\n")
	for index := range chapters {
		manifest.WriteString(fmt.Sprintf(`    <item id="chapter-%d" href="%s" media-type="application/xhtml+xml"/>`+"\n", index+1, filepath.Base(files[index])))
	}
	for index, image := range images {
		manifest.WriteString(fmt.Sprintf(`    <item id="image-%d" href="%s" media-type="%s"/>`+"\n", index+1, image.name, image.mediaType))
	}

	var spine strings.Builder
	for index := range chapters {
		spine.WriteString(fmt.Sprintf(`    <itemref idref="chapter-%d"/>`+"\n", index+1))
	}
	return `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id" xml:lang="zh-Hans">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
` + metadata.String() + `  </metadata>
  <manifest>
` + manifest.String() + `  </manifest>
  <spine toc="ncx">
` + spine.String() + `  </spine>
</package>`
}

func buildEPUBNCX(book model.Comic, chapters []epubChapter, files []string) string {
	identifier := epubIdentifier(book, chapters)
	var points strings.Builder
	for index, chapter := range chapters {
		points.WriteString(fmt.Sprintf(`    <navPoint id="navPoint-%d" playOrder="%d">`+"\n", index+1, index+1))
		points.WriteString("      <navLabel><text>")
		points.WriteString(escapeXML(chapter.Title))
		points.WriteString("</text></navLabel>\n")
		points.WriteString(`      <content src="`)
		points.WriteString(filepath.Base(files[index]))
		points.WriteString("\"/>\n    </navPoint>\n")
	}
	return `<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="` + escapeXML(identifier) + `"/>
    <meta name="dtb:depth" content="1"/>
    <meta name="dtb:totalPageCount" content="0"/>
    <meta name="dtb:maxPageNumber" content="0"/>
  </head>
  <docTitle><text>` + escapeXML(source.FirstNonEmpty(book.Title, book.ID, "未命名书籍")) + `</text></docTitle>
  <navMap>
` + points.String() + `  </navMap>
</ncx>`
}

func epubIdentifier(book model.Comic, chapters []epubChapter) string {
	value := strings.Join([]string{
		book.SourceID,
		book.ID,
		book.Title,
		firstEPUBChapterTitle(chapters),
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return "urn:manco:" + hex.EncodeToString(sum[:])
}

func firstEPUBChapterTitle(chapters []epubChapter) string {
	if len(chapters) == 0 {
		return ""
	}
	return chapters[0].Title
}

func normalizeXHTMLFragment(markup string) (string, error) {
	if strings.TrimSpace(markup) == "" {
		return "", nil
	}
	document, err := nethtml.Parse(strings.NewReader(markup))
	if err != nil {
		return plainTextToXHTML(markup), nil
	}
	body := findHTMLNode(document, "body")
	if body == nil {
		return plainTextToXHTML(markup), nil
	}
	var output strings.Builder
	for node := body.FirstChild; node != nil; node = node.NextSibling {
		if err := renderXHTMLNode(&output, node); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

func findHTMLNode(node *nethtml.Node, name string) *nethtml.Node {
	if node.Type == nethtml.ElementNode && strings.EqualFold(node.Data, name) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLNode(child, name); found != nil {
			return found
		}
	}
	return nil
}

func renderXHTMLNode(output *strings.Builder, node *nethtml.Node) error {
	switch node.Type {
	case nethtml.TextNode:
		output.WriteString(html.EscapeString(node.Data))
		return nil
	case nethtml.ElementNode:
		tag := strings.ToLower(node.Data)
		if tag == "script" || tag == "style" || tag == "head" || tag == "html" || tag == "body" {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if err := renderXHTMLNode(output, child); err != nil {
					return err
				}
			}
			return nil
		}
		output.WriteByte('<')
		output.WriteString(tag)
		for _, attribute := range node.Attr {
			key := strings.TrimSpace(attribute.Key)
			if key == "" || strings.HasPrefix(strings.ToLower(key), "on") {
				continue
			}
			output.WriteByte(' ')
			output.WriteString(key)
			output.WriteString(`="`)
			output.WriteString(html.EscapeString(attribute.Val))
			output.WriteByte('"')
		}
		if isXHTMLVoidElement(tag) {
			output.WriteString("/>")
			return nil
		}
		output.WriteByte('>')
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := renderXHTMLNode(output, child); err != nil {
				return err
			}
		}
		output.WriteString("</")
		output.WriteString(tag)
		output.WriteByte('>')
		return nil
	default:
		return nil
	}
}

func isXHTMLVoidElement(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func plainTextToXHTML(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var output strings.Builder
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		output.WriteString("<p>")
		output.WriteString(html.EscapeString(line))
		output.WriteString("</p>")
	}
	if output.Len() == 0 {
		output.WriteString("<p></p>")
	}
	return output.String()
}

func escapeXML(value string) string {
	return html.EscapeString(value)
}

const epubContainerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

const epubStylesheet = `body {
  font-family: serif;
  line-height: 1.7;
  margin: 1.5em;
}

h1 {
  font-size: 1.4em;
  margin: 0 0 1em;
}

img {
  display: block;
  max-width: 100%;
  height: auto;
  margin: 1em auto;
}`
