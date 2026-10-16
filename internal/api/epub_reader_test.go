package api

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeEPUBChapterHTMLRewritesAssets(t *testing.T) {
	html, title := sanitizeEPUBChapterHTML([]byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>第一章</title><script>alert(1)</script></head>
<body onload="alert(2)"><h1>第一章</h1><p>正文</p><img src="images/001.png" onerror="alert(3)"/>
<a href="chapter-2.xhtml">下一章</a><a href="javascript:alert(4)">坏链接</a></body></html>`))
	if title != "第一章" {
		t.Fatalf("title = %q", title)
	}
	for _, value := range []string{
		`src="/api/local/epub/file?entry=images%2F001.png"`,
		"正文",
		"下一章",
	} {
		if !strings.Contains(html, value) {
			t.Fatalf("sanitized chapter missing %q: %s", value, html)
		}
	}
	for _, value := range []string{"<script", "onerror", "onload", "javascript:", `href="chapter-2.xhtml"`} {
		if strings.Contains(html, value) {
			t.Fatalf("sanitized chapter kept %q: %s", value, html)
		}
	}
}

func TestReadEPUBPackageDocument(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "book.epub")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	writer := zip.NewWriter(file)
	write := func(name, value string) {
		t.Helper()
		handle, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := handle.Write([]byte(value)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	write("OEBPS/content.opf", `<?xml version="1.0"?><package><manifest><item id="c1" href="chapter-1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close epub: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close epub file: %v", err)
	}

	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open epub: %v", err)
	}
	defer archive.Close()
	packagePath, document, err := readEPUBPackageDocument(archive)
	if err != nil {
		t.Fatalf("read package document: %v", err)
	}
	if packagePath != "OEBPS/content.opf" || !strings.Contains(string(document), `href="chapter-1.xhtml"`) {
		t.Fatalf("package document = %q, %s", packagePath, document)
	}
}

func TestNormalizeEPUBEntryPathRejectsTraversal(t *testing.T) {
	if _, err := normalizeEPUBEntryPath("OEBPS", "../../etc/passwd"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	got, err := normalizeEPUBEntryPath("OEBPS", "images/001.png")
	if err != nil || got != "OEBPS/images/001.png" {
		t.Fatalf("normalize = %q, %v", got, err)
	}
}
