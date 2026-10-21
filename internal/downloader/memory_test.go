package downloader

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

type queuedRefTestStore struct {
	memoryJobs
	loads int
}

func (s *queuedRefTestStore) ListQueuedJobRefsPage(_ context.Context, offset, _ int) ([]model.QueuedJobRef, error) {
	if offset > 0 {
		return nil, nil
	}
	return []model.QueuedJobRef{{ID: 1, SourceID: "busy"}}, nil
}

func (s *queuedRefTestStore) DownloadJob(ctx context.Context, id int64) (model.DownloadJob, error) {
	s.loads++
	return s.memoryJobs.DownloadJob(ctx, id)
}

func TestQueueDoesNotLoadPayloadWhenSourceHasNoFreeSlot(t *testing.T) {
	store := &queuedRefTestStore{}
	engine := NewEngine(nil, store, t.TempDir(), 2, 1, log.New(io.Discard, "", 0))
	engine.sourceConcurrency["busy"] = 1
	engine.activeBySource["busy"] = 1
	if !engine.dispatchQueuedJobs(context.Background()) || store.loads != 0 {
		t.Fatalf("blocked task payload was loaded %d times", store.loads)
	}
}

func TestDecodeRejectsOversizedImageBeforePixelAllocation(t *testing.T) {
	// A valid PNG header is enough for DecodeConfig; no pixel data is needed.
	data := append([]byte(nil), []byte("\x89PNG\r\n\x1a\n")...)
	chunk := make([]byte, 25)
	binary.BigEndian.PutUint32(chunk[:4], 13)
	copy(chunk[4:8], "IHDR")
	binary.BigEndian.PutUint32(chunk[8:12], 10000)
	binary.BigEndian.PutUint32(chunk[12:16], 10000)
	chunk[16], chunk[17] = 8, 2
	binary.BigEndian.PutUint32(chunk[21:], crc32.ChecksumIEEE(chunk[4:21]))
	data = append(data, chunk...)
	if _, err := decodeImage(data); err == nil || !strings.Contains(err.Error(), "像素") {
		t.Fatalf("100 MP image was not rejected before decoding: %v", err)
	}
}

func TestHeavyImageWaitsBeforeFetchingBytes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "image")
	}))
	defer server.Close()
	engine := NewEngine(nil, &memoryJobs{}, t.TempDir(), 1, 1, log.New(io.Discard, "", 0))
	engine.heavyImageSem <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := downloadPageToFile(engine, ctx, server.Client(), source.Account{}, &staticSource{}, "work", model.Chapter{},
		model.Page{URL: server.URL, DescrambleParts: 10}, filepath.Join(t.TempDir(), "image.jpg"))
	<-engine.heavyImageSem
	if err == nil || calls.Load() != 0 {
		t.Fatalf("waiting image was fetched into memory: calls=%d err=%v", calls.Load(), err)
	}
}
