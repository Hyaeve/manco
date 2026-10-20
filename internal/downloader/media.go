package downloader

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func streamingClient(client *http.Client) *http.Client {
	if client == nil {
		client = source.NewHTTPClient()
	}
	clone := *client
	// A whole video can take hours; connection/TLS timeouts remain on the
	// transport, while cancellation is owned by the chapter context.
	clone.Timeout = 0
	return &clone
}

func downloadOtherPage(ctx context.Context, client *http.Client, account source.Account, item source.Source, comicID string, chapter model.Chapter, page model.Page, dir string, index int) (string, string, string, error) {
	client = streamingClient(client)
	// Resolve through the plugin, but never send video bytes to its image store.
	resolved := false
	if mediaSource, ok := item.(source.MediaSource); ok {
		var err error
		page, err = mediaSource.ResolveMedia(ctx, account, page)
		if err != nil {
			return "", "", "", err
		}
		resolved = true
	}
	var streamType, filename, address string
	stream, streamType, filename, address, err := openOtherContent(ctx, client, account, item, comicID, chapter, page, resolved)
	if err != nil {
		return "", "", "", err
	}
	defer stream.Close()
	reader := bufio.NewReader(stream)
	sample, _ := reader.Peek(512)
	streamType = source.FirstNonEmpty(streamType, page.ContentType)
	if isMediaManifest(streamType, address, sample) {
		_ = stream.Close()
		page.URL = address
		path := filepath.Join(dir, fmt.Sprintf("%03d.mkv", index+1))
		if err := downloadMediaManifest(ctx, client, account, page, path); err != nil {
			return "", "", "", err
		}
		return path, ".mkv", "video/x-matroska", nil
	}
	return writeOtherPage(reader, dir, index, streamType, source.FirstNonEmpty(page.FileName, filename), address)
}

func openOtherContent(ctx context.Context, client *http.Client, account source.Account, item source.Source, comicID string, chapter model.Chapter, page model.Page, resolved bool) (io.ReadCloser, string, string, string, error) {
	if resolved {
		return openHTTPPageContent(ctx, client, account, page)
	}
	return openPageContent(ctx, client, account, item, comicID, chapter, page)
}

func isMediaManifest(contentType, address string, sample []byte) bool {
	path := ""
	if parsed, err := url.Parse(address); err == nil {
		path = strings.ToLower(parsed.Path)
	}
	kind := strings.ToLower(strings.Split(contentType, ";")[0])
	return strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".mpd") ||
		kind == "application/vnd.apple.mpegurl" || kind == "application/x-mpegurl" ||
		kind == "application/dash+xml" || strings.HasPrefix(strings.TrimSpace(string(sample)), "#EXTM3U")
}

func mediaCommandArgs(client *http.Client, account source.Account, page model.Page, path string) ([]string, error) {
	request, err := http.NewRequest(http.MethodGet, page.URL, nil)
	if err != nil || request.URL.Host == "" || (request.URL.Scheme != "http" && request.URL.Scheme != "https") {
		return nil, errors.New("视频资源地址必须为 HTTP 或 HTTPS")
	}
	request.Header.Set("User-Agent", source.FirstNonEmpty(account.UserAgent, source.DefaultUserAgent))
	request.Header.Set("Accept", "*/*")
	if page.Referer != "" {
		request.Header.Set("Referer", page.Referer)
	}
	for key, value := range page.Headers {
		if strings.ContainsAny(key+value, "\r\n") {
			return nil, errors.New("视频请求头无效")
		}
		request.Header.Set(key, value)
	}
	if request.Header.Get("Cookie") == "" && account.Cookie != "" {
		request.Header.Set("Cookie", account.Cookie)
	}
	var headers strings.Builder
	keys := make([]string, 0, len(request.Header))
	for key := range request.Header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := request.Header.Get(key)
		if strings.ContainsAny(key+value, "\r\n") {
			return nil, errors.New("视频请求头无效")
		}
		headers.WriteString(key + ": " + value + "\r\n")
	}
	proxy, err := source.HTTPClientProxy(client, request)
	if err != nil {
		return nil, errors.New("无法读取视频下载代理设置")
	}
	proxyURL := ""
	if proxy != nil {
		proxyURL = proxy.String()
	}
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-protocol_whitelist", "http,https,httpproxy,tcp,tls,crypto",
		"-rw_timeout", "60000000", "-http_proxy", proxyURL,
		"-headers", headers.String(), "-i", page.URL,
		"-map", "0:v:0?", "-map", "0:a:0?", "-c", "copy",
		"-f", "matroska", path,
	}, nil
}

func downloadMediaManifest(ctx context.Context, client *http.Client, account source.Account, page model.Page, path string) error {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("视频为 HLS/DASH 分段流，需要 FFmpeg；容器镜像已内置，本地运行请安装 FFmpeg")
	}
	temporary := path + ".part"
	args, err := mediaCommandArgs(client, account, page, temporary)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, binary, args...)
	// Do not buffer FFmpeg's stderr: it can be large and contain signed URLs.
	// The selected source client is the sole authority for proxy routing.
	for _, entry := range os.Environ() {
		key := strings.ToLower(strings.SplitN(entry, "=", 2)[0])
		switch key {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			continue
		}
		command.Env = append(command.Env, entry)
	}
	if err := command.Run(); err != nil {
		_ = os.Remove(temporary)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("视频分段流下载失败，请检查内容源连接、验证状态或资源是否支持下载")
	}
	info, err := os.Stat(temporary)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(temporary)
		return errors.New("视频分段流未生成有效文件")
	}
	return renameFileReplacing(temporary, path)
}
