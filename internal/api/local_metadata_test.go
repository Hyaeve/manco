package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplySeriesMetaReadsMylarSeriesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "series.json")
	payload := `{
  "version": "1.0.1",
  "metadata": {
    "type": "comicSeries",
    "publisher": "测试出版社",
    "name": "测试作品",
    "comicid": 9527,
    "year": 2026,
    "description_text": "简介纯文本",
    "description_formatted": "<p>简介<b>格式化</b></p>",
    "volume": 1,
    "booktype": "comic",
    "age_rating": "Adult",
    "comic_image": "cover.jpg",
    "total_issues": 12,
    "publication_run": "2026 - ",
    "status": "Continuing",
    "language": "zh-CN",
    "authors": ["作者甲", "作者乙"],
    "genres": ["漫画", "冒险"],
    "tags": ["热血"]
  }
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	item := localItemEntry{Title: "目录名"}
	applySeriesMeta(path, &item)
	if item.Title != "测试作品" || item.ComicID != "9527" {
		t.Fatalf("series identity = %q/%q", item.Title, item.ComicID)
	}
	if item.Author != "作者甲, 作者乙" {
		t.Fatalf("author = %q", item.Author)
	}
	if item.Description != "简介 格式化" {
		t.Fatalf("description = %q", item.Description)
	}
	if item.Status != "连载中" || item.Publisher != "测试出版社" || item.PublishedAt != "2026" {
		t.Fatalf("series labels = %+v", item)
	}
	if len(item.Tags) != 3 || item.Tags[0] != "漫画" || item.Tags[2] != "热血" {
		t.Fatalf("tags = %v", item.Tags)
	}
}

func TestApplyOPFMetaReadsEPUBPackageFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.opf")
	payload := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>OPF 作品</dc:title>
    <dc:creator>作者甲</dc:creator>
    <dc:creator>作者乙</dc:creator>
    <dc:description>OPF 简介</dc:description>
    <dc:language>zh-CN</dc:language>
    <dc:date>2025-03-02</dc:date>
    <dc:publisher>OPF 出版社</dc:publisher>
    <dc:identifier>urn:book-9527</dc:identifier>
  </metadata>
  <spine page-progression-direction="ltr"/>
</package>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	item := localItemEntry{Title: "目录名"}
	applyOPFMeta(path, &item)
	if item.Title != "OPF 作品" || item.ComicID != "book-9527" {
		t.Fatalf("opf identity = %q/%q", item.Title, item.ComicID)
	}
	if item.Author != "作者甲, 作者乙" || item.Description != "OPF 简介" {
		t.Fatalf("opf text = %+v", item)
	}
	if item.Language != "zh-CN" || item.Publisher != "OPF 出版社" || item.PublishedAt != "2025-03-02" {
		t.Fatalf("opf labels = %+v", item)
	}
}

func TestApplyComicMetaReadsEnhancedComicInfoFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ComicInfo.xml")
	payload := `<?xml version="1.0" encoding="UTF-8"?>
<ComicInfo>
  <Title>第1话</Title>
  <Series>测试作品</Series>
  <Number>1</Number>
  <Summary>测试简介</Summary>
  <Year>2026</Year>
  <Month>4</Month>
  <Day>5</Day>
  <Writer>测试作者</Writer>
  <Publisher>测试出版社</Publisher>
  <Genre>测试,漫画</Genre>
  <Tags>测试,漫画</Tags>
  <LanguageISO>zh-Hans</LanguageISO>
  <AgeRating>Mature 17+</AgeRating>
  <Notes>Manco source=stub comicId=42</Notes>
</ComicInfo>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	item := localItemEntry{Title: "目录名"}
	applyComicMeta(path, &item)
	if item.Title != "测试作品" || item.Author != "测试作者" || item.Description != "测试简介" {
		t.Fatalf("comicinfo text = %+v", item)
	}
	if item.SourceID != "stub" || item.ComicID != "42" {
		t.Fatalf("comicinfo identity = %q/%q", item.SourceID, item.ComicID)
	}
	if item.Publisher != "测试出版社" || item.Language != "zh-Hans" || item.PublishedAt != "2026-04-05" {
		t.Fatalf("comicinfo labels = %+v", item)
	}
}

func TestApplyComicMetaReadsLegacyStatusElement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ComicInfo.xml")
	payload := `<?xml version="1.0" encoding="UTF-8"?>
<ComicInfo>
  <Title>旧版章节</Title>
  <Series>旧版作品</Series>
  <Status>连载中</Status>
</ComicInfo>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	item := localItemEntry{Title: "目录名"}
	applyComicMeta(path, &item)
	if item.Status != "连载中" {
		t.Fatalf("legacy ComicInfo status = %q, want 连载中", item.Status)
	}
}

func TestApplyComicMetaReadsNotesStatusAndBookKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ComicInfo.xml")
	// 单范式 ComicInfo 用 Genre 里的「书籍」标记作品类型，并把 v2.1 没有的
	// Status 以 url 转义键值放进 Notes。
	payload := `<?xml version="1.0" encoding="UTF-8"?>
<ComicInfo>
  <Series>插件小说</Series>
  <Writer>测试作者</Writer>
  <Genre>小说,测试,书籍</Genre>
  <Notes>Manco source=archive-book comicId=book-1 status=%E8%BF%9E%E8%BD%BD%E4%B8%AD</Notes>
</ComicInfo>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	item := localItemEntry{Title: "目录名", Kind: "comic"}
	applyComicMeta(path, &item)
	if item.Kind != "book" {
		t.Fatalf("genre 书籍 should mark the entry as a book, got %q", item.Kind)
	}
	if item.Status != "连载中" {
		t.Fatalf("notes status = %q, want 连载中", item.Status)
	}
	if item.SourceID != "archive-book" || item.ComicID != "book-1" {
		t.Fatalf("notes identity = %q/%q", item.SourceID, item.ComicID)
	}
}
