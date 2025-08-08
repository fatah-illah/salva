package worker

import (
	"context"
	"sync/atomic"
)

type WorkerPool struct {
	taskChan    chan func()
	workerCount int32
}

func NewWorkerPool(size int) *WorkerPool {
	pool := &WorkerPool{
		taskChan:    make(chan func(), 1024),
		workerCount: int32(size),
	}

	for i := 0; i < size; i++ {
		go pool.worker()
	}

	return pool
}

func (p *WorkerPool) worker() {
	for task := range p.taskChan {
		task()
	}
}

func (p *WorkerPool) Submit(task func()) {
	p.taskChan <- task
}

func (p *WorkerPool) SetSize(size int) {
	current := atomic.LoadInt32(&p.workerCount)
	delta := size - int(current)

	if delta > 0 {
		for i := 0; i < delta; i++ {
			atomic.AddInt32(&p.workerCount, 1)
			go p.worker()
		}
	} else if delta < 0 {
		for i := 0; i < -delta; i++ {
			atomic.AddInt32(&p.workerCount, -1)
			p.taskChan <- func() { panic("exit") } // Graceful exit
		}
	}
}

func (p *WorkerPool) Run(ctx context.Context) {
	<-ctx.Done()
	close(p.taskChan)
}
