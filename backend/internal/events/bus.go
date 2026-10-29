// Package events is an in-process pub/sub used to push inbox changes to SSE clients
// and IMAP IDLE sessions.
package events

import (
	"sync"

	"github.com/google/uuid"
)

type Event struct {
	Type      string    `json:"type"` // new, updated, deleted, cleared, credentials
	ProjectID uuid.UUID `json:"project_id"`
	EmailID   uuid.UUID `json:"email_id,omitempty"`
	UID       int64     `json:"uid,omitempty"`
	Payload   any       `json:"payload,omitempty"`
}

type Bus struct {
	mu   sync.RWMutex
	subs map[uuid.UUID]map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subs: make(map[uuid.UUID]map[chan Event]struct{})}
}

// Subscribe returns a buffered channel of events for the project and a cancel func.
func (b *Bus) Subscribe(projectID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if b.subs[projectID] == nil {
		b.subs[projectID] = make(map[chan Event]struct{})
	}
	b.subs[projectID][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if set := b.subs[projectID]; set != nil {
				delete(set, ch)
				if len(set) == 0 {
					delete(b.subs, projectID)
				}
			}
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}

// Publish delivers the event to every subscriber; slow subscribers drop events.
func (b *Bus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[ev.ProjectID] {
		select {
		case ch <- ev:
		default:
		}
	}
}
