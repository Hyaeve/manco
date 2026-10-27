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
	ID              int64           `json:"id"`
	SourceID        string          `json:"sourceId"`
	Username        string          `json:"username,omitempty"`
	SecretCipher    string          `json:"-"`
	TokenCipher     string          `json:"-"`
	CookieCipher    string          `json:"-"`
	UserAgentCipher string          `json:"-"`
	HomeURL         string          `json:"homeUrl,omitempty"`
	Extra           json.RawMessage `json:"extra,omitempty"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type Subscription struct {
	SeriesDir               string     `json:"seriesDir,omitempty"`
	DownloadDir             string     `json:"downloadDir"`
	EffectiveDownloadDir    string     `json:"effectiveDownloadDir,omitempty"`
	FollowSourceDownloadDir bool       `json:"followSourceDownloadDir,omitempty"`
	ConvertToSimplified     bool       `json:"convertToSimplified"`
	ChapterMarker           bool       `json:"chapterMarker"`
	CoverBlur               bool       `json:"coverBlur"`
	ID                      int64      `json:"id"`
	SourceID                string     `json:"sourceId"`
	ComicID                 string     `json:"comicId"`
	OriginSourceID          string     `json:"originSourceId,omitempty"`
	OriginComicID           string     `json:"originComicId,omitempty"`
	OriginSourceURL         string     `json:"originSourceUrl,omitempty"`
	MatchKind               string     `json:"matchKind,omitempty"`
	MatchStatus             string     `json:"matchStatus,omitempty"`
	MatchError              string     `json:"matchError,omitempty"`
	MatchAttemptAt          *time.Time `json:"matchAttemptAt,omitempty"`
	Title                   string     `json:"title"`
	Cover                   string     `json:"cover"`
	LocalCover              string     `json:"localCover,omitempty"`
	CachedCover             string     `json:"cachedCover,omitempty"`
	Author                  string     `json:"author"`
	Enabled                 bool       `json:"enabled"`
	AutoDownload            bool       `json:"autoDownload"`
	CronExpr                string     `json:"cronExpr"`
	LastChapterID           string     `json:"lastChapterId"`
	LastChapterTitle        string     `json:"lastChapterTitle"`
	LastChapterOrder        float64    `json:"lastChapterOrder"`
	ChapterCount            int        `json:"chapterCount,omitempty"`
	LastCheckedAt           *time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
	ComicStatus             string     `json:"comicStatus,omitempty"`
	LastNewChapterAt        *time.Time `json:"lastNewChapterAt,omitempty"`
	DisabledAt              *time.Time `json:"disabledAt,omitempty"`
	// Temporary 表示这条订阅是「点了一次下载」临时生成的卡片：任务跑完就
	// 自动移除卡片，已下载的文件保留。
	Temporary          bool       `json:"temporary,omitempty"`
	CompletedAt        *time.Time `json:"completedAt,omitempty"`
	ArchivedAt         *time.Time `json:"archivedAt,omitempty"`
	ArchiveReason      string     `json:"archiveReason,omitempty"`
	ArchiveWarningAt   *time.Time `json:"archiveWarningAt,omitempty"`
	ArchiveWarningDays int        `json:"archiveWarningDays,omitempty"`
}

type DownloadJob struct {
	DownloadDir         string     `json:"downloadDir"`
	ConvertToSimplified bool       `json:"convertToSimplified"`
	ChapterMarker       bool       `json:"chapterMarker"`
	QueuePriority       int        `json:"queuePriority,omitempty"`
	MergeChapters       []Chapter  `json:"mergeChapters,omitempty"`
	ID                  int64      `json:"id"`
	SourceID            string     `json:"sourceId"`
	ComicID             string     `json:"comicId"`
	ComicTitle          string     `json:"comicTitle"`
	ComicCover          string     `json:"comicCover"`
	ComicAuthor         string     `json:"comicAuthor,omitempty"`
	ComicDescription    string     `json:"comicDescription,omitempty"`
	ComicTags           []string   `json:"comicTags,omitempty"`
	ChapterID           string     `json:"chapterId"`
	ChapterTitle        string     `json:"chapterTitle"`
	ChapterOrder        float64    `json:"chapterOrder"`
	Status              string     `json:"status"`
	TotalPages          int        `json:"totalPages"`
	CompletedPages      int        `json:"completedPages"`
	FilePath            string     `json:"filePath,omitempty"`
	LocalCover          string     `json:"localCover,omitempty"`
	Size                int64      `json:"size,omitempty"`
	Error               string     `json:"error,omitempty"`
	RetryCount          int        `json:"retryCount"`
	NextRetryAt         *time.Time `json:"nextRetryAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	StartedAt           *time.Time `json:"startedAt,omitempty"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
}

type QueuedJobRef struct {
	ID       int64
	SourceID string
}

const (
	QueuePriorityNormal = 0
	QueuePriorityManual = 1
)

// DownloadJobStats counts persisted task outcomes per work.
type DownloadJobStats struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	// Pending 是排队中/进行中的章节数，订阅页用它决定是否需要继续轮询。
	Pending int `json:"pending"`
}

// SourceDownloadSettings stores per-source download limits in the source
// account's extra JSON so each card can be tuned independently.
type SourceDownloadSettings struct {
	ChapterConcurrency   int `json:"chapterConcurrency"`
	PageConcurrency      int `json:"pageConcurrency"`
	BatchSize            int `json:"batchSize"`
	BatchIntervalMinutes int `json:"batchIntervalMinutes"`
	// CoverBlur is only a default for newly created subscriptions and Telegram
	// search results. Each subscription keeps its own editable CoverBlur value.
	CoverBlur bool `json:"coverBlur"`
	// DefaultDownloadDir pre-fills the download location when subscribing to a
	// work from this source; the user can still override it per subscription.
	DefaultDownloadDir string `json:"defaultDownloadDir,omitempty"`
}

type Stats struct {
	Subscriptions int `json:"subscriptions"`
	ActiveJobs    int `json:"activeJobs"`
	RunningJobs   int `json:"runningJobs"`
	QueuedJobs    int `json:"queuedJobs"`
	FailedJobs    int `json:"failedJobs"`
	CompletedJobs int `json:"completedJobs"`
	LibraryItems  int `json:"libraryItems"`
}

type SourceInfo struct {
	Builtin        bool          `json:"builtin,omitempty"`
	Editable       bool          `json:"editable,omitempty"`
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Kind           string        `json:"kind,omitempty"`
	Description    string        `json:"description"`
	Homepage       string        `json:"homepage"`
	NeedsLogin     bool          `json:"needsLogin"`
	CanSearch      bool          `json:"canSearch"`
	CanBrowse      bool          `json:"canBrowse"`
	Icon           string        `json:"icon,omitempty"`
	Order          int           `json:"order,omitempty"`
	Sites          []string      `json:"sites,omitempty"`
	Filters        []FilterGroup `json:"filters,omitempty"`
	Hidden         bool          `json:"hidden,omitempty"`
	PluginSource   bool          `json:"pluginSource,omitempty"`
	RepositoryName string        `json:"repositoryName,omitempty"`
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
	Extra    map[string]string
}

type Comic struct {
	SourceID           string   `json:"sourceId"`
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Cover              string   `json:"cover"`
	SourceURL          string   `json:"sourceUrl,omitempty"`
	CachedCover        string   `json:"cachedCover,omitempty"`
	Cached             bool     `json:"cached,omitempty"`
	Author             string   `json:"author,omitempty"`
	Description        string   `json:"description,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	Status             string   `json:"status,omitempty"`
	ChapterCount       int      `json:"chapterCount,omitempty"`
	LatestChapterTitle string   `json:"latestChapterTitle,omitempty"`
	UpdatedAt          string   `json:"updatedAt,omitempty"`
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
	ContentType     string            `json:"contentType,omitempty"`
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
	CustomName  string                `json:"customName,omitempty"`
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
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	PackageName string             `json:"packageName,omitempty"`
	Version     string             `json:"version,omitempty"`
	Kind        string             `json:"kind"`
	PluginType  string             `json:"pluginType"`
	Description string             `json:"description,omitempty"`
	Homepage    string             `json:"homepage,omitempty"`
	Icon        string             `json:"icon,omitempty"`
	InstallURL  string             `json:"installUrl,omitempty"`
	Installable bool               `json:"installable"`
	Runtime     string             `json:"runtime,omitempty"`
	Config      json.RawMessage    `json:"config,omitempty"`
	Sources     []RepositorySource `json:"sources,omitempty"`
	Raw         json.RawMessage    `json:"raw,omitempty"`
}

type RepositorySource struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	Language string   `json:"language,omitempty"`
	HomeURL  string   `json:"homeUrl,omitempty"`
	Mirrors  []string `json:"mirrors,omitempty"`
	Message  string   `json:"message,omitempty"`
}
