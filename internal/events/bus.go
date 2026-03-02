// Package events provides an internal fan-out event bus for Controllore.
// All subsystems publish events here; API WebSocket clients subscribe.
package events

import (
	"sync"
	"time"
)

// EventType classifies the type of event.
type EventType string

const (
	// LSP events
	EvLSPCreated   EventType = "lsp.created"
	EvLSPUpdated   EventType = "lsp.updated"
	EvLSPDeleted   EventType = "lsp.deleted"
	EvLSPStatusChg EventType = "lsp.status_changed"
	EvLSPRerouted  EventType = "lsp.rerouted"

	// Topology events
	EvNodeUp      EventType = "topology.node_up"
	EvNodeDown    EventType = "topology.node_down"
	EvNodeUpdated EventType = "topology.node_updated"
	EvLinkUp      EventType = "topology.link_up"
	EvLinkDown    EventType = "topology.link_down"
	EvLinkUpdated EventType = "topology.link_updated"

	// PCEP session events
	EvSessionUp   EventType = "pcep.session_up"
	EvSessionDown EventType = "pcep.session_down"

	// PCE engine events
	EvPathComputed EventType = "pce.path_computed"
	EvPathFailed   EventType = "pce.path_failed"
)

// Event is a single event published on the bus.
type Event struct {
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"ts"`
	ID        string      `json:"id,omitempty"`   // entity ID (LSP ID, node ID, etc.)
	Data      interface{} `json:"data,omitempty"` // event-specific payload
}

// Bus is a publish/subscribe event bus supporting multiple subscribers.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]chan Event
}

// New creates a new event Bus.
func New() *Bus {
	return &Bus{
		subscribers: make(map[string]chan Event),
	}
}

// Subscribe registers a named subscriber and returns its receive channel.
// The channel is buffered to drop events under backpressure rather than blocking.
func (b *Bus) Subscribe(name string, bufsize int) <-chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Event, bufsize)
	b.subscribers[name] = ch
	return ch
}

// Unsubscribe removes a subscriber by name.
func (b *Bus) Unsubscribe(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[name]; ok {
		close(ch)
		delete(b.subscribers, name)
	}
}

// Publish delivers an event to all subscribers, dropping if their buffer is full.
func (b *Bus) Publish(evt Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// Subscriber is full; drop event (non-blocking)
		}
	}
}

// PublishLSP is a convenience wrapper for LSP events.
func (b *Bus) PublishLSP(evtType EventType, lspID string, data interface{}) {
	b.Publish(Event{
		Type:      evtType,
		Timestamp: time.Now(),
		ID:        lspID,
		Data:      data,
	})
}

// PublishTopology is a convenience wrapper for topology events.
func (b *Bus) PublishTopology(evtType EventType, entityID string, data interface{}) {
	b.Publish(Event{
		Type:      evtType,
		Timestamp: time.Now(),
		ID:        entityID,
		Data:      data,
	})
}

// PublishSession is a convenience wrapper for PCEP session events.
func (b *Bus) PublishSession(evtType EventType, sessionID string, data interface{}) {
	b.Publish(Event{
		Type:      evtType,
		Timestamp: time.Now(),
		ID:        sessionID,
		Data:      data,
	})
}
