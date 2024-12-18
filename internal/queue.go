package internal

import "sync"

type SafeQueue[T any] struct {
	queue []T
	mu    *sync.Mutex
	cond  *sync.Cond
}

func NewSafeQueue[T any]() *SafeQueue[T] {
	mu := &sync.Mutex{}
	return &SafeQueue[T]{
		queue: make([]T, 0),
		mu:    mu,
		cond:  sync.NewCond(mu),
	}
}

func (q *SafeQueue[T]) Push(item T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queue = append(q.queue, item)
	q.cond.Signal()
}

func (q *SafeQueue[T]) WaitToPop() T {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.queue) == 0 {
		q.cond.Wait()
	}
	item := q.queue[0]
	q.queue = q.queue[1:]
	return item
}
