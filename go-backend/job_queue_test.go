package main

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestKeyedJobQueueCountsBeforePublishing(t *testing.T) {
	queue := newKeyedJobQueue[int]("test", 1)
	queue.start(func(int) error { return nil })

	for i := 0; i < 1_000; i++ {
		if !queue.enqueue(fmt.Sprintf("job-%d", i), i) {
			t.Fatalf("job %d was unexpectedly dropped", i)
		}
		queue.wait()
	}
}

func TestKeyedJobQueueCoalescesLatestValueWhileRunning(t *testing.T) {
	queue := newKeyedJobQueue[int]("test", 2)
	release := make(chan struct{})
	started := make(chan int, 2)
	var mu sync.Mutex
	var handled []int
	queue.start(func(value int) error {
		started <- value
		<-release
		mu.Lock()
		handled = append(handled, value)
		mu.Unlock()
		return nil
	})
	if !queue.enqueue("same", 1) {
		t.Fatal("first job was not queued")
	}
	if got := <-started; got != 1 {
		t.Fatalf("first value = %d", got)
	}
	if queue.enqueue("same", 2) {
		t.Fatal("duplicate job was queued")
	}
	if queue.enqueue("same", 3) {
		t.Fatal("second duplicate job was queued")
	}

	release <- struct{}{}
	if got := <-started; got != 3 {
		t.Fatalf("coalesced value = %d, want latest 3", got)
	}
	release <- struct{}{}
	queue.wait()
	mu.Lock()
	if !reflect.DeepEqual(handled, []int{1, 3}) {
		t.Fatalf("handled = %v, want [1 3]", handled)
	}
	mu.Unlock()
	if !queue.enqueue("same", 3) {
		t.Fatal("completed key could not be queued again")
	}
	release <- struct{}{}
	queue.wait()
}

func TestKeyedJobQueueDropsWithoutLeakingDrainCount(t *testing.T) {
	queue := newKeyedJobQueue[int]("test", 1)
	if !queue.enqueue("first", 1) {
		t.Fatal("first job was not queued")
	}
	if queue.enqueue("full", 2) {
		t.Fatal("job was queued past capacity")
	}

	queue.start(func(int) error { return nil })
	queue.wait()
	if !queue.enqueue("full", 3) {
		t.Fatal("dropped key remained marked as seen")
	}
	queue.wait()
}

func TestKeyedJobQueueReleasesPanickingJob(t *testing.T) {
	queue := newKeyedJobQueue[int]("test", 1)
	queue.start(func(int) error { panic("boom") })
	if !queue.enqueue("same", 1) {
		t.Fatal("job was not queued")
	}
	queue.wait()
	if !queue.enqueue("same", 2) {
		t.Fatal("panicking job leaked its dedup key")
	}
	queue.wait()
}
