package server

import (
	"sync"

	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
)

// subscribeBufferSize bounds how many updates a slow subscriber can fall
// behind by before publish starts dropping for it rather than blocking.
// One slow or stuck dashboard connection must never stall the Kafka
// consumer loop for every other subscriber, on this batch or any other
// (docs/ARCHITECTURE.md section 10a: losing a live-dashboard tick costs a
// stale UI for one refresh, never a wrong number -- the same tolerance
// that already justifies audit.events being best-effort on the publish
// side applies here on the fan-out side too).
const subscribeBufferSize = 32

// Hub fans BatchUpdate messages out to every subscriber currently watching
// a given batch_id (docs/ARCHITECTURE.md section 6a). One Hub instance is
// shared by the Kafka consumer (the sole publisher, consume.go) and every
// concurrent StreamBatchUpdates call (one subscriber each, server.go).
//
// Pure in-memory, deliberately: this is a live fan-out to whatever gRPC
// streams happen to be open on this pod right now, not state anything else
// reads, so there is nothing here to persist or share across pods.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan *reportingv1.BatchUpdate]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan *reportingv1.BatchUpdate]struct{})}
}

// subscribe registers a new watcher for batchID and returns its channel
// plus an unsubscribe func the caller must call exactly once, typically
// deferred, when it stops reading. unsubscribe closes the channel, so a
// caller must stop reading from it only after calling unsubscribe (or
// after observing it closed), never before.
func (h *Hub) subscribe(batchID string) (ch chan *reportingv1.BatchUpdate, unsubscribe func()) {
	ch = make(chan *reportingv1.BatchUpdate, subscribeBufferSize)

	h.mu.Lock()
	if h.subs[batchID] == nil {
		h.subs[batchID] = make(map[chan *reportingv1.BatchUpdate]struct{})
	}
	h.subs[batchID][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[batchID], ch)
			if len(h.subs[batchID]) == 0 {
				delete(h.subs, batchID)
			}
			h.mu.Unlock()
			close(ch)
		})
	}
}

// publish fans update out to every current subscriber of batchID.
// Non-blocking: a full channel (a subscriber not reading fast enough)
// drops this update for that one subscriber rather than blocking every
// other subscriber, or the Kafka consumer loop that calls this for every
// batch, not just this one.
func (h *Hub) publish(batchID string, update *reportingv1.BatchUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[batchID] {
		select {
		case ch <- update:
		default:
		}
	}
}
