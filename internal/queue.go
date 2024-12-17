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

type OrderPreservingCompletionQueue[T any] struct {
	results        map[int]T
	mu             *sync.Mutex
	nextID         int
	completionChan chan T
}

func NewOrderPreservingCompletionQueue[T any]() *OrderPreservingCompletionQueue[T] {
	mu := &sync.Mutex{}
	return &OrderPreservingCompletionQueue[T]{
		results:        make(map[int]T),
		mu:             mu,
		nextID:         0,
		completionChan: make(chan T, 256),
	}
}

func (q *OrderPreservingCompletionQueue[T]) Push(idx int, item T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.results[idx] = item
	if idx != q.nextID {
		return
	}
	for {
		if item, ok := q.results[q.nextID]; ok {
			delete(q.results, q.nextID)
			q.nextID++
			q.completionChan <- item
		} else {
			break
		}
	}
}

func (q *OrderPreservingCompletionQueue[T]) GetCompletionChan() <-chan T {
	return q.completionChan
}
