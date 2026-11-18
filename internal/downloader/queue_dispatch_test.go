package downloader

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

type dispatchTestSource struct {
	*staticSource
	started chan string
}

func (s *dispatchTestSource) Pages(ctx context.Context, _ source.Account, _ string, chapter model.Chapter) ([]model.Page, error) {
	s.started <- chapter.ID
	<-ctx.Done()
	return nil, ctx.Err()
}

func newDispatchTestEngine(t *testing.T, slots int) (*Engine, *store.Store, context.Context, <-chan string) {
	t.Helper()
	root := t.TempDir()
	repository, err := store.Open(filepath.Join(root, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	registry := sources.NewRegistry(nil, nil, nil)
	item := &dispatchTestSource{staticSource: &staticSource{}, started: make(chan string, slots)}
	registry.Register(item)
	engine := NewEngine(registry, repository, root, slots, 1, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		waitFor(t, 2*time.Second, func() bool {
			engine.mu.Lock()
			defer engine.mu.Unlock()
			return engine.activeTotal == 0
		})
	})
	return engine, repository, ctx, item.started
}

func enqueueDispatchTestJob(t *testing.T, repository *store.Store, sourceID, chapterID string, priority int) model.DownloadJob {
	t.Helper()
	job, err := repository.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID: sourceID, ComicID: "work", ComicTitle: "Work", ChapterID: chapterID,
		ChapterTitle: chapterID, QueuePriority: priority,
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func expectDispatchedChapter(t *testing.T, started <-chan string, chapterID string) {
	t.Helper()
	select {
	case got := <-started:
		if got != chapterID {
			t.Fatalf("dispatched chapter %q, want %q", got, chapterID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("chapter %q did not start", chapterID)
	}
}

func TestDispatchRechecksQueueHeadWhenSlotOpens(t *testing.T) {
	engine, repository, ctx, started := newDispatchTestEngine(t, 1)
	jobs := make([]model.DownloadJob, 0, queueBatchSize+5)
	for index := 1; index <= cap(jobs); index++ {
		jobs = append(jobs, enqueueDispatchTestJob(t, repository, "stub", strconv.Itoa(index), model.QueuePriorityNormal))
	}
	stopJob := func(job model.DownloadJob) {
		if !engine.Cancel(job.ID) {
			t.Fatalf("unable to cancel test job %d", job.ID)
		}
		waitFor(t, 2*time.Second, func() bool {
			engine.mu.Lock()
			defer engine.mu.Unlock()
			return engine.activeTotal == 0
		})
	}
	engine.dispatchQueuedJobs(ctx)
	expectDispatchedChapter(t, started, "1")
	stopJob(jobs[0])
	engine.dispatchQueuedJobs(ctx)
	expectDispatchedChapter(t, started, "2")

	enqueueDispatchTestJob(t, repository, "stub", "manual", model.QueuePriorityManual)
	stopJob(jobs[1])
	engine.dispatchQueuedJobs(ctx)
	expectDispatchedChapter(t, started, "manual")
}

func TestDispatchFindsRunnableJobPastBlockedSources(t *testing.T) {
	engine, repository, ctx, started := newDispatchTestEngine(t, 2)
	busy := model.DownloadJob{ID: -1, SourceID: "busy"}
	engine.SetSourceConcurrency(map[string]int{"busy": 1})
	if !engine.claimJob(busy.ID, busy.SourceID) {
		t.Fatal("unable to occupy source slot")
	}
	defer engine.release(busy)
	engine.sourceBatchSize["paused"] = 1
	engine.sourceCooldownUntil["paused"] = time.Now().Add(time.Hour)
	for index := 0; index < 520; index++ {
		sourceID := "busy"
		if index%2 != 0 {
			sourceID = "paused"
		}
		enqueueDispatchTestJob(t, repository, sourceID, strconv.Itoa(index), model.QueuePriorityManual)
	}
	ready := enqueueDispatchTestJob(t, repository, "stub", "ready", model.QueuePriorityNormal)
	engine.dispatchQueuedJobs(ctx)
	expectDispatchedChapter(t, started, "ready")
	if !engine.Cancel(ready.ID) {
		t.Fatal("unable to cancel ready job")
	}
	waitFor(t, 2*time.Second, func() bool {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		return engine.activeTotal == 1
	})
	enqueueDispatchTestJob(t, repository, "stub", "ready-after-pause", model.QueuePriorityNormal)
	engine.batchSize = 1
	engine.cooldownUntil = time.Now().Add(time.Hour)
	engine.dispatchQueuedJobs(ctx)
	engine.mu.Lock()
	active := engine.activeTotal
	engine.cooldownUntil = time.Time{}
	engine.mu.Unlock()
	if active != 1 {
		t.Fatalf("global cooldown allowed more work: %d active slots", active)
	}
	engine.dispatchQueuedJobs(ctx)
	expectDispatchedChapter(t, started, "ready-after-pause")
}
