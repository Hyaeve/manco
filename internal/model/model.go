package model

import (
	"encoding/json"
	"time"
)

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type SourceAccount struct {
	ID           int64           `json:"id"`
	SourceID     string          `json:"sourceId"`
	Username     string          `json:"username,omitempty"`
	SecretCipher string          `json:"-"`
	TokenCipher  string          `json:"-"`
	CookieCipher string          `json:"-"`
	HomeURL      string          `json:"homeUrl,omitempty"`
	Extra        json.RawMessage `json:"extra,omitempty"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type Subscription struct {
	ID               int64      `json:"id"`
	SourceID         string     `json:"sourceId"`
	ComicID          string     `json:"comicId"`
	Title            string     `json:"title"`
	Cover            string     `json:"cover"`
	Author           string     `json:"author"`
	Enabled          bool       `json:"enabled"`
	AutoDownload     bool       `json:"autoDownload"`
	LastChapterID    string     `json:"lastChapterId"`
	LastChapterTitle string     `json:"lastChapterTitle"`
	LastChapterOrder float64    `json:"lastChapterOrder"`
	LastCheckedAt    *time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type DownloadJob struct {
	ID             int64      `json:"id"`
	SourceID       string     `json:"sourceId"`
	ComicID        string     `json:"comicId"`
	ComicTitle     string     `json:"comicTitle"`
	ComicCover     string     `json:"comicCover"`
	ChapterID      string     `json:"chapterId"`
	ChapterTitle   string     `json:"chapterTitle"`
	ChapterOrder   float64    `json:"chapterOrder"`
	Status         string     `json:"status"`
	TotalPages     int        `json:"totalPages"`
	CompletedPages int        `json:"completedPages"`
	FilePath       string     `json:"filePath,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
}

type Stats struct {
	Subscriptions int `json:"subscriptions"`
	ActiveJobs    int `json:"activeJobs"`
	CompletedJobs int `json:"completedJobs"`
	LibraryItems  int `json:"libraryItems"`
}

type SourceInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	NeedsLogin  bool   `json:"needsLogin"`
	CanSearch   bool   `json:"canSearch"`
	CanBrowse   bool   `json:"canBrowse"`
}

type Comic struct {
	SourceID     string   `json:"sourceId"`
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Cover        string   `json:"cover"`
	Author       string   `json:"author,omitempty"`
	Description  string   `json:"description,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Status       string   `json:"status,omitempty"`
	ChapterCount int      `json:"chapterCount,omitempty"`
	UpdatedAt    string   `json:"updatedAt,omitempty"`
}

type Chapter struct {
	ID        string            `json:"id"`
	ComicID   string            `json:"comicId"`
	Title     string            `json:"title"`
	Order     float64           `json:"order"`
	UpdatedAt string            `json:"updatedAt,omitempty"`
	PageCount int               `json:"pageCount,omitempty"`
	URL       string            `json:"url,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

type Page struct {
	URL             string            `json:"url"`
	Alternatives    []string          `json:"alternatives,omitempty"`
	Referer         string            `json:"referer,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	FileName        string            `json:"fileName,omitempty"`
	DescrambleParts int               `json:"descrambleParts,omitempty"`
	AssetID         string            `json:"assetId,omitempty"`
}

type SearchResult struct {
	Items   []Comic `json:"items"`
	Page    int     `json:"page"`
	Pages   int     `json:"pages"`
	Total   int     `json:"total"`
	HasMore bool    `json:"hasMore"`
}

type ComicDetail struct {
	Comic    Comic     `json:"comic"`
	Chapters []Chapter `json:"chapters"`
}
