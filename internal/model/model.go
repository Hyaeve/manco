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
	DownloadDir         string     `json:"downloadDir"`
	ConvertToSimplified bool       `json:"convertToSimplified"`
	ID                  int64      `json:"id"`
	SourceID            string     `json:"sourceId"`
	ComicID             string     `json:"comicId"`
	Title               string     `json:"title"`
	Cover               string     `json:"cover"`
	Author              string     `json:"author"`
	Enabled             bool       `json:"enabled"`
	AutoDownload        bool       `json:"autoDownload"`
	CronExpr            string     `json:"cronExpr"`
	LastChapterID       string     `json:"lastChapterId"`
	LastChapterTitle    string     `json:"lastChapterTitle"`
	LastChapterOrder    float64    `json:"lastChapterOrder"`
	ChapterCount        int        `json:"chapterCount,omitempty"`
	LastCheckedAt       *time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	ComicStatus         string     `json:"comicStatus,omitempty"`
	LastNewChapterAt    *time.Time `json:"lastNewChapterAt,omitempty"`
	DisabledAt          *time.Time `json:"disabledAt,omitempty"`
	CompletedAt         *time.Time `json:"completedAt,omitempty"`
	ArchivedAt          *time.Time `json:"archivedAt,omitempty"`
	ArchiveReason       string     `json:"archiveReason,omitempty"`
}

type DownloadJob struct {
	DownloadDir         string     `json:"downloadDir"`
	ConvertToSimplified bool       `json:"convertToSimplified"`
	ID                  int64      `json:"id"`
	SourceID            string     `json:"sourceId"`
	ComicID             string     `json:"comicId"`
	ComicTitle          string     `json:"comicTitle"`
	ComicCover          string     `json:"comicCover"`
	ChapterID           string     `json:"chapterId"`
	ChapterTitle        string     `json:"chapterTitle"`
	ChapterOrder        float64    `json:"chapterOrder"`
	Status              string     `json:"status"`
	TotalPages          int        `json:"totalPages"`
	CompletedPages      int        `json:"completedPages"`
	FilePath            string     `json:"filePath,omitempty"`
	Error               string     `json:"error,omitempty"`
	RetryCount          int        `json:"retryCount"`
	NextRetryAt         *time.Time `json:"nextRetryAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	StartedAt           *time.Time `json:"startedAt,omitempty"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
}

type Stats struct {
	Subscriptions int `json:"subscriptions"`
	ActiveJobs    int `json:"activeJobs"`
	CompletedJobs int `json:"completedJobs"`
	LibraryItems  int `json:"libraryItems"`
}

type SourceInfo struct {
	Builtin     bool          `json:"builtin,omitempty"`
	Editable    bool          `json:"editable,omitempty"`
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Kind        string        `json:"kind,omitempty"`
	Description string        `json:"description"`
	Homepage    string        `json:"homepage"`
	NeedsLogin  bool          `json:"needsLogin"`
	CanSearch   bool          `json:"canSearch"`
	CanBrowse   bool          `json:"canBrowse"`
	Icon        string        `json:"icon,omitempty"`
	Sites       []string      `json:"sites,omitempty"`
	Filters     []FilterGroup `json:"filters,omitempty"`
	Hidden      bool          `json:"hidden,omitempty"`
}

type FilterOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Disabled bool   `json:"disabled,omitempty"`
}

type FilterGroup struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Default string         `json:"default,omitempty"`
	Options []FilterOption `json:"options"`
}

type BrowseOptions struct {
	Category string
	Sort     string
	State    string
	Region   string
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

type CustomSource struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Homepage    string          `json:"homepage"`
	Icon        string          `json:"icon,omitempty"`
	RepoURL     string          `json:"repoUrl,omitempty"`
	Config      json.RawMessage `json:"config"`
	Enabled     bool            `json:"enabled"`
	Hidden      bool            `json:"hidden"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type ExtensionRepository struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Kind        string                `json:"kind"`
	URL         string                `json:"url"`
	Description string                `json:"description,omitempty"`
	Icon        string                `json:"icon,omitempty"`
	Enabled     bool                  `json:"enabled"`
	Catalog     json.RawMessage       `json:"catalog,omitempty"`
	Extensions  []RepositoryExtension `json:"extensions,omitempty"`
	Status      string                `json:"status,omitempty"`
	Error       string                `json:"error,omitempty"`
	LastSyncAt  *time.Time            `json:"lastSyncAt,omitempty"`
	LastError   string                `json:"lastError,omitempty"`
	CreatedAt   time.Time             `json:"createdAt"`
	UpdatedAt   time.Time             `json:"updatedAt"`
}

type RepositoryExtension struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	PackageName string          `json:"packageName,omitempty"`
	Version     string          `json:"version,omitempty"`
	Kind        string          `json:"kind"`
	PluginType  string          `json:"pluginType"`
	Description string          `json:"description,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	Icon        string          `json:"icon,omitempty"`
	InstallURL  string          `json:"installUrl,omitempty"`
	Installable bool            `json:"installable"`
	Config      json.RawMessage `json:"config,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}
