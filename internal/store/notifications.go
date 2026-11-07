package store

import (
	"sync"

	"github.com/jackc/pgx/v5"
)

// The transaction still serializes network revisions and durable event IDs.
// These hints become visible only after the database commit succeeds.
type notifyingTx struct {
	pgx.Tx
	networks map[string]struct{}
}

type eventNotifications struct {
	mu          sync.Mutex
	subscribers map[string]map[chan struct{}]struct{}
	published   uint64
	coalesced   uint64
}

type NotificationStatistics struct {
	Networks    int    `json:"networks"`
	Subscribers int    `json:"subscribers"`
	Published   uint64 `json:"published,string"`
	Coalesced   uint64 `json:"coalesced,string"`
}

// SubscribeEvents must be called before reading the durable event log. A hint
// received during that read remains queued for the next read, closing the
// subscribe/read race. The caller must cancel when it stops consuming events.
func (s *Store) SubscribeEvents(networkID string) (<-chan struct{}, func()) {
	n := &s.notifications
	wake := make(chan struct{}, 1)
	n.mu.Lock()
	if n.subscribers == nil {
		n.subscribers = make(map[string]map[chan struct{}]struct{})
	}
	if n.subscribers[networkID] == nil {
		n.subscribers[networkID] = make(map[chan struct{}]struct{})
	}
	n.subscribers[networkID][wake] = struct{}{}
	n.mu.Unlock()
	return wake, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		delete(n.subscribers[networkID], wake)
		if len(n.subscribers[networkID]) == 0 {
			delete(n.subscribers, networkID)
		}
	}
}

func (n *eventNotifications) publish(networkID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.published++
	for wake := range n.subscribers[networkID] {
		select {
		case wake <- struct{}{}:
		default:
			n.coalesced++
		}
	}
}

func (s *Store) NotificationStats() NotificationStatistics {
	n := &s.notifications
	n.mu.Lock()
	defer n.mu.Unlock()
	stats := NotificationStatistics{Networks: len(n.subscribers), Published: n.published, Coalesced: n.coalesced}
	for _, subscribers := range n.subscribers {
		stats.Subscribers += len(subscribers)
	}
	return stats
}
