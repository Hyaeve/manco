package pluginruntime

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"
)

func TestIdleHostIsReleasedAfterRequest(t *testing.T) {
	manager, _ := newProtocolTestManager(t)
	manager.idleTimeout = 20 * time.Millisecond
	defer manager.Close()
	process := manager.process
	if _, err := manager.Call(context.Background(), "details", map[string]any{"sourceName": "demo"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		manager.mu.Lock()
		released := manager.process == nil
		manager.mu.Unlock()
		if released {
			break
		}
		select {
		case <-deadline:
			t.Fatal("idle process was retained")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !process.isClosed() {
		t.Fatal("released process was not stopped")
	}
}

func TestHostResponseLineBoundsAndReleasesLargeBuffer(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 4096)+"\nsmall\nlast"), 64)
	line, err := readHostResponseLine(reader, 8192)
	if err != nil || len(line) != 4097 {
		t.Fatalf("large line len=%d err=%v", len(line), err)
	}
	line, err = readHostResponseLine(reader, 8192)
	if err != nil || string(line) != "small\n" || reader.Size() != 64 {
		t.Fatalf("small line=%q size=%d err=%v", line, reader.Size(), err)
	}
	line, err = readHostResponseLine(reader, 8192)
	if err != nil || string(line) != "last" {
		t.Fatalf("last line=%q err=%v", line, err)
	}
	if _, err := readHostResponseLine(bufio.NewReader(strings.NewReader(strings.Repeat("x", 100))), 32); err == nil {
		t.Fatal("unbounded response accepted")
	}
}

func TestIdleTimerCannotStopBusyOrReplacedHost(t *testing.T) {
	manager, _ := newProtocolTestManager(t)
	manager.idleTimeout = 20 * time.Millisecond
	defer manager.Close()
	manager.mu.Lock()
	process := manager.process
	manager.scheduleIdleStopLocked(RuntimeKototoro, process)
	time.Sleep(40 * time.Millisecond)
	if process.isClosed() {
		t.Fatal("timer interrupted a request holding the manager lock")
	}
	// Reset after a long request. The expired callback must not stop it.
	manager.scheduleIdleStopLocked(RuntimeKototoro, process)
	manager.idleTimeout = time.Hour
	manager.scheduleIdleStopLocked(RuntimeKototoro, process)
	manager.mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	if process.isClosed() {
		t.Fatal("stale timer stopped a recently used process")
	}
}

func TestIdleTimerKeepsMultiplexedRequestsAlive(t *testing.T) {
	manager, _ := newProtocolTestManager(t)
	manager.idleTimeout = 10 * time.Millisecond
	defer manager.Close()
	manager.mu.Lock()
	process := manager.process
	manager.scheduleIdleStopLocked(RuntimeKototoro, process)
	process.inFlight = 1
	manager.mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	if process.isClosed() {
		t.Fatal("idle timer killed a multiplexed request")
	}
	manager.mu.Lock()
	process.inFlight = 0
	manager.scheduleIdleStopLocked(RuntimeKototoro, process)
	manager.mu.Unlock()
	deadline := time.After(2 * time.Second)
	for !process.isClosed() {
		select {
		case <-deadline:
			t.Fatal("idle host was not released after last request")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
