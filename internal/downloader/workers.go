package downloader

import (
	"context"
	"sync"
)

// runPageWorkers bounds goroutines as well as network requests. A semaphore
// inside one goroutine per page still retains every page and stack at once.
func runPageWorkers(ctx context.Context, count, limit int, run func(int)) {
	if limit < 1 {
		limit = 1
	}
	if limit > count {
		limit = count
	}
	var workers sync.WaitGroup
	for worker := 0; worker < limit; worker++ {
		workers.Add(1)
		go func(start int) {
			defer workers.Done()
			for index := start; index < count; index += limit {
				if ctx.Err() != nil {
					return
				}
				run(index)
			}
		}(worker)
	}
	workers.Wait()
}
