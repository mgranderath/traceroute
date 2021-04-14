package taskgroup

import (
	"log"
	"sync"
)

type TaskGroup struct {
	count int
	mu    sync.Mutex
	done  []chan struct{}
}

func New() *TaskGroup {
	return &TaskGroup{
		count: 0,
		mu:    sync.Mutex{},
		done:  []chan struct{}{},
	}
}

func (t *TaskGroup) Add() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count++
}

func (t *TaskGroup) Done() {
	log.Println("before lock")
	t.mu.Lock()
	log.Println("after lock")
	defer t.mu.Unlock()
	if t.count-1 == 0 {
		for _, doneChannel := range t.done {
			log.Println("before send done")
			doneChannel <- struct{}{}
			log.Println("after send done")
		}
		t.done = []chan struct{}{}
	}
	t.count--
}

func (t *TaskGroup) Wait() {
	doneChannel := make(chan struct{})
	t.mu.Lock()
	t.done = append(t.done, doneChannel)
	t.mu.Unlock()
	<-doneChannel
}
