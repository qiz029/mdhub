package main

import (
	"log"
	"sync"
)

// keyedJobQueue owns the shared enqueue/dedup/drain protocol used by
// background workers. A job is counted before it becomes visible to a worker,
// so an immediately completing worker can never call Done before Add.
type keyedJobQueue[T any] struct {
	name string
	ch   chan keyedJob[T]

	mu        sync.Mutex
	seen      map[string]struct{}
	running   map[string]bool
	latest    map[string]T
	dirty     map[string]bool
	wg        sync.WaitGroup
	startOnce sync.Once
}

type keyedJob[T any] struct {
	key   string
	value T
}

func newKeyedJobQueue[T any](name string, capacity int) *keyedJobQueue[T] {
	return &keyedJobQueue[T]{
		name:    name,
		ch:      make(chan keyedJob[T], capacity),
		seen:    make(map[string]struct{}),
		running: make(map[string]bool),
		latest:  make(map[string]T),
		dirty:   make(map[string]bool),
	}
}

func (q *keyedJobQueue[T]) enqueue(key string, value T) bool {
	q.mu.Lock()
	if _, exists := q.seen[key]; exists {
		q.latest[key] = value
		if q.running[key] {
			q.dirty[key] = true
		}
		q.mu.Unlock()
		return false
	}
	q.seen[key] = struct{}{}
	q.latest[key] = value
	q.wg.Add(1)
	q.mu.Unlock()

	select {
	case q.ch <- keyedJob[T]{key: key, value: value}:
		return true
	default:
		q.mu.Lock()
		delete(q.seen, key)
		delete(q.latest, key)
		q.mu.Unlock()
		q.wg.Done()
		log.Printf("%s queue full, dropped %s", q.name, key)
		return false
	}
}

// start launches the queue's single consumer. The queue owns dedup release,
// completion accounting, error logging, and panic isolation so workers cannot
// accidentally leak a key or block wait forever. Repeated starts are ignored.
func (q *keyedJobQueue[T]) start(handle func(T) error) {
	q.startOnce.Do(func() {
		go func() {
			for job := range q.ch {
				q.handle(job, handle)
			}
		}()
	})
}

func (q *keyedJobQueue[T]) handle(job keyedJob[T], handle func(T) error) {
	for {
		q.mu.Lock()
		q.running[job.key] = true
		value := q.latest[job.key]
		delete(q.dirty, job.key)
		q.mu.Unlock()

		q.invoke(job.key, value, handle)

		q.mu.Lock()
		if q.dirty[job.key] {
			q.mu.Unlock()
			continue
		}
		delete(q.seen, job.key)
		delete(q.running, job.key)
		delete(q.latest, job.key)
		q.mu.Unlock()
		q.wg.Done()
		return
	}
}

func (q *keyedJobQueue[T]) invoke(key string, value T, handle func(T) error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("%s job %s panicked: %v", q.name, key, recovered)
		}
	}()
	if err := handle(value); err != nil {
		log.Printf("%s job %s: %v", q.name, key, err)
	}
}

func (q *keyedJobQueue[T]) wait() {
	q.wg.Wait()
}
