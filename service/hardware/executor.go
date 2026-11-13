package hardware

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrBusy        = errors.New("hardware command queue is full")
	ErrClosed      = errors.New("hardware executor is closed")
	ErrCardClosed  = errors.New("motion card is not open")
	ErrTransition  = errors.New("motion card is changing state")
	ErrCardUnknown = errors.New("motion card was not found")
)

type Priority uint8

const (
	PriorityRealtime Priority = iota
	PriorityControl
)

type task struct {
	ctx      context.Context
	call     func() error
	priority Priority
	readOnly bool
	started  chan struct{}
	done     chan error
}

// executor bounds concurrent LTDMC reads and excludes writes/lifecycle calls
// from them. Queued real-time work takes precedence; control work is admitted
// after a bounded burst once the real-time queue drains. Database, JSON and
// network work never runs on these workers.
type executor struct {
	realtime     chan task
	control      chan task
	jobs         chan task
	stop         chan struct{}
	sdkMu        sync.RWMutex
	controlSlots chan struct{}
	once         sync.Once
	workers      int
}

func newExecutor(workers int) *executor {
	if workers < 1 {
		workers = 1
	}
	e := &executor{
		realtime:     make(chan task, 512),
		control:      make(chan task, 64),
		controlSlots: make(chan struct{}, 1),
		jobs:         make(chan task, workers),
		stop:         make(chan struct{}),
		workers:      workers,
	}
	go e.run()
	for i := 0; i < workers; i++ {
		go e.work()
	}
	return e
}

func (e *executor) run() {
	realtimeBurst := 0
	for {
		if realtimeBurst >= 64 && len(e.realtime) == 0 {
			select {
			case req := <-e.control:
				if !e.dispatch(req) {
					return
				}
				realtimeBurst = 0
				continue
			default:
			}
		}
		select {
		case <-e.stop:
			return
		default:
		}
		select {
		case req := <-e.realtime:
			if !e.dispatch(req) {
				return
			}
			realtimeBurst++
			continue
		default:
		}
		select {
		case req := <-e.realtime:
			if !e.dispatch(req) {
				return
			}
			realtimeBurst++
		case req := <-e.control:
			if !e.dispatch(req) {
				return
			}
			realtimeBurst = 0
		case <-e.stop:
			return
		}
	}
}

func (e *executor) dispatch(req task) bool {
	select {
	case <-e.stop:
		return false
	case e.jobs <- req:
		return true
	}
}

func (e *executor) work() {
	for {
		select {
		case <-e.stop:
			return
		case req := <-e.jobs:
			e.execute(req)
		}
	}
}

func (e *executor) execute(req task) {
	if err := req.ctx.Err(); err != nil {
		req.done <- err
		return
	}
	if req.readOnly {
		e.sdkMu.RLock()
		defer e.sdkMu.RUnlock()
	} else if req.priority == PriorityControl {
		// A pending REST write must not hold RWMutex's writer preference over
		// new real-time reads. Retry TryLock until current readers drain.
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for !e.sdkMu.TryLock() {
			select {
			case <-e.stop:
				req.done <- ErrClosed
				return
			case <-req.ctx.Done():
				req.done <- req.ctx.Err()
				return
			case <-ticker.C:
			}
		}
		defer e.sdkMu.Unlock()
	} else {
		e.sdkMu.Lock()
		defer e.sdkMu.Unlock()
	}
	if err := req.ctx.Err(); err != nil {
		req.done <- err
		return
	}
	close(req.started)
	req.done <- req.call()
}

func (e *executor) do(ctx context.Context, p Priority, call func() error) error {
	return e.submit(ctx, p, false, call)
}

func (e *executor) doRead(ctx context.Context, p Priority, call func() error) error {
	return e.submit(ctx, p, true, call)
}

func (e *executor) submit(ctx context.Context, p Priority, readOnly bool, call func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == PriorityControl {
		select {
		case <-e.stop:
			return ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		case e.controlSlots <- struct{}{}:
		}
		defer func() { <-e.controlSlots }()
	}
	q := e.control
	if p == PriorityRealtime {
		q = e.realtime
	}
	req := task{ctx: ctx, call: call, priority: p, readOnly: readOnly, started: make(chan struct{}), done: make(chan error, 1)}
	select {
	case <-e.stop:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case q <- req:
	default:
		return ErrBusy
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		select {
		case <-req.started:
			return <-req.done
		default:
		}
		return ctx.Err()
	case <-e.stop:
		return ErrClosed
	}
}

func (e *executor) close() { e.once.Do(func() { close(e.stop) }) }

func parallel(ctx context.Context, limit, count int, fn func(context.Context, int) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if count == 0 {
		return nil
	}
	if limit < 1 {
		limit = 1
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error

schedule:
	for i := 0; i < count; i++ {
		select {
		case <-workCtx.Done():
			break schedule
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(workCtx, index); err != nil {
				once.Do(func() { firstErr = err })
				cancel()
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
