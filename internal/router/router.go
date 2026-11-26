package router

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

// Handler processes one message on the topic's worker goroutine.
type Handler func(model.Message)

// Router fans messages out to per-topic workers so that a slow Lua script on
// one topic cannot block the others. Each topic queue is bounded; when full the
// oldest message is dropped in favour of the newest.
type Router struct {
	ctx     context.Context
	cancel  context.CancelFunc
	handler Handler
	dedupe  *Deduper
	topics  map[string]*topicQueue
	wg      sync.WaitGroup
	dropped atomic.Int64
}

type topicQueue struct {
	name string
	ch   chan model.Message
}

func New(parent context.Context, topics []string, handler Handler, dedupe *Deduper, queueSize int) *Router {
	if queueSize <= 0 {
		queueSize = 100
	}
	ctx, cancel := context.WithCancel(parent)
	r := &Router{
		ctx:     ctx,
		cancel:  cancel,
		handler: handler,
		dedupe:  dedupe,
		topics:  make(map[string]*topicQueue, len(topics)),
	}
	for _, name := range topics {
		r.topics[name] = &topicQueue{name: name, ch: make(chan model.Message, queueSize)}
	}
	return r
}

func (r *Router) Start() {
	for _, t := range r.topics {
		r.wg.Add(1)
		go func(t *topicQueue) {
			defer r.wg.Done()
			for {
				select {
				case <-r.ctx.Done():
					return
				case m := <-t.ch:
					r.handler(m)
				}
			}
		}(t)
	}
}

// Dispatch queues a message. Unknown topics are ignored. Duplicate
// (topic,id) pairs are dropped.
func (r *Router) Dispatch(m model.Message) {
	select {
	case <-r.ctx.Done():
		return
	default:
	}
	t, ok := r.topics[m.Topic]
	if !ok {
		return
	}
	key := m.Topic + "/" + m.ID
	if m.ID != "" && r.dedupe != nil && r.dedupe.Seen(key) {
		return
	}

	select {
	case t.ch <- m:
	default:
		// Drop the oldest queued message, then retry once.
		select {
		case <-t.ch:
		default:
		}
		select {
		case t.ch <- m:
		default:
		}
		r.dropped.Add(1)
	}
}

func (r *Router) Dropped() int64 { return r.dropped.Load() }

func (r *Router) Close() {
	r.cancel()
	r.wg.Wait()
}
