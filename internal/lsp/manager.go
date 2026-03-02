// Package lsp manages the lifecycle of PCE-controlled LSPs (SR Policies).
package lsp

import (
	"fmt"
	"sync"
	"time"

	"github.com/buraglio/controllore/pkg/srv6"
	"github.com/google/uuid"
)

// SRType indicates whether this is an SRv6 or SR-MPLS LSP.
type SRType string

const (
	SRTypeSRv6 SRType = "srv6"
	SRTypeMPLS SRType = "mpls"
)

// LSPStatus reflects the control-plane state of the LSP.
type LSPStatus string

const (
	LSPStatusActive    LSPStatus = "active"
	LSPStatusDown      LSPStatus = "down"
	LSPStatusDelegated LSPStatus = "delegated"
	LSPStatusReported  LSPStatus = "reported" // PCC-controlled, PCE visible only
	LSPStatusPending   LSPStatus = "pending"  // PCInitiate sent, awaiting PCRpt
)

// Constraints describes the TE requirements for an LSP.
type Constraints struct {
	MetricType   srv6.MetricType `json:"metric_type"`
	MaxCost      uint32          `json:"max_cost,omitempty"`
	MinBandwidth uint64          `json:"min_bandwidth,omitempty"` // bytes/sec
	IncludeAny   uint32          `json:"include_any,omitempty"`
	IncludeAll   uint32          `json:"include_all,omitempty"`
	ExcludeAny   uint32          `json:"exclude_any,omitempty"`
	AvoidSRLG    []uint32        `json:"avoid_srlg,omitempty"`
	ExcludeNodes []string        `json:"exclude_nodes,omitempty"`
	FlexAlgo     uint8           `json:"flex_algo,omitempty"`
	UseUSID      bool            `json:"use_usid,omitempty"`
	USIDBlockLen uint8           `json:"usid_block_len,omitempty"`
	MaxSIDDepth  uint8           `json:"max_sid_depth,omitempty"`
}

// LSP represents a PCE-managed SR Label Switched Path / SR Policy.
type LSP struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// PCC is the router-id of the Path Computation Client that owns this LSP.
	PCC         string    `json:"pcc"`
	SrcRouterID string    `json:"src"`
	DstRouterID string    `json:"dst"`
	SRType      SRType    `json:"sr_type"`
	Status      LSPStatus `json:"status"`

	// BSID is the Binding SID for the SR Policy (SRv6: IPv6 address, MPLS: label).
	BSID string `json:"bsid,omitempty"`

	// SegmentList is the ordered list of SRv6 SIDs or MPLS labels.
	SegmentList []srv6.SID `json:"segment_list"`

	// ComputedMetric is the cost of the computed path.
	ComputedMetric uint32 `json:"computed_metric"`

	// Constraints are the TE constraints used for path computation.
	Constraints Constraints `json:"constraints"`

	// PCEPID is the PCEP PLSP-ID assigned by the PCC (non-zero for existing LSPs).
	PCEPID uint32 `json:"pcep_id,omitempty"`

	// SRPID is the last SRP-ID used in a PCInitiate/PCUpdate for this LSP.
	SRPID uint32 `json:"srp_id,omitempty"`

	// SessionID is the PCEP session UUID (which PCC connection manages this LSP).
	SessionID uuid.UUID `json:"session_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ChangeEventType describes what changed in a HistoryEntry.
type ChangeEventType string

const (
	EventCreated   ChangeEventType = "created"
	EventUpdated   ChangeEventType = "updated"
	EventDeleted   ChangeEventType = "deleted"
	EventStatusChg ChangeEventType = "status_change"
	EventRerouted  ChangeEventType = "rerouted"
)

// HistoryEntry records a change to an LSP.
type HistoryEntry struct {
	Timestamp time.Time       `json:"timestamp"`
	Event     ChangeEventType `json:"event"`
	OldStatus LSPStatus       `json:"old_status,omitempty"`
	NewStatus LSPStatus       `json:"new_status,omitempty"`
	Details   string          `json:"details,omitempty"`
}

// Manager is the thread-safe LSP lifecycle manager.
type Manager struct {
	mu      sync.RWMutex
	lsps    map[uuid.UUID]*LSP
	history map[uuid.UUID][]HistoryEntry
	// nextSRPID is a monotonically increasing SRP-ID counter.
	nextSRPID uint32
}

// NewManager creates an empty LSP manager.
func NewManager() *Manager {
	return &Manager{
		lsps:    make(map[uuid.UUID]*LSP),
		history: make(map[uuid.UUID][]HistoryEntry),
	}
}

// Create registers a new LSP and returns it.
func (m *Manager) Create(lsp *LSP) (*LSP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if lsp.ID == uuid.Nil {
		lsp.ID = uuid.New()
	}
	now := time.Now()
	lsp.CreatedAt = now
	lsp.UpdatedAt = now
	if lsp.Status == "" {
		lsp.Status = LSPStatusPending
	}

	m.lsps[lsp.ID] = lsp
	m.history[lsp.ID] = []HistoryEntry{{
		Timestamp: now,
		Event:     EventCreated,
		NewStatus: lsp.Status,
	}}
	return lsp, nil
}

// Get returns an LSP by ID.
func (m *Manager) Get(id uuid.UUID) (*LSP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lsp, ok := m.lsps[id]
	if !ok {
		return nil, fmt.Errorf("LSP %s not found", id)
	}
	return lsp, nil
}

// All returns a snapshot of all LSPs.
func (m *Manager) All() []*LSP {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*LSP, 0, len(m.lsps))
	for _, l := range m.lsps {
		result = append(result, l)
	}
	return result
}

// UpdateStatus updates the operational status of an LSP.
func (m *Manager) UpdateStatus(id uuid.UUID, newStatus LSPStatus, details string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lsp, ok := m.lsps[id]
	if !ok {
		return fmt.Errorf("LSP %s not found", id)
	}
	old := lsp.Status
	lsp.Status = newStatus
	lsp.UpdatedAt = time.Now()
	m.history[id] = append(m.history[id], HistoryEntry{
		Timestamp: lsp.UpdatedAt,
		Event:     EventStatusChg,
		OldStatus: old,
		NewStatus: newStatus,
		Details:   details,
	})
	return nil
}

// UpdateSegmentList replaces the computed segment list for an LSP.
func (m *Manager) UpdateSegmentList(id uuid.UUID, segs []srv6.SID, cost uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lsp, ok := m.lsps[id]
	if !ok {
		return fmt.Errorf("LSP %s not found", id)
	}
	lsp.SegmentList = segs
	lsp.ComputedMetric = cost
	lsp.UpdatedAt = time.Now()
	m.history[id] = append(m.history[id], HistoryEntry{
		Timestamp: lsp.UpdatedAt,
		Event:     EventRerouted,
		Details:   fmt.Sprintf("segment list updated: %d SIDs, cost=%d", len(segs), cost),
	})
	return nil
}

// Delete removes an LSP from management.
func (m *Manager) Delete(id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.lsps[id]; !ok {
		return fmt.Errorf("LSP %s not found", id)
	}
	delete(m.lsps, id)
	m.history[id] = append(m.history[id], HistoryEntry{
		Timestamp: time.Now(),
		Event:     EventDeleted,
	})
	return nil
}

// History returns the change history for an LSP.
func (m *Manager) History(id uuid.UUID) ([]HistoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.history[id]
	if !ok {
		return nil, fmt.Errorf("no history for LSP %s", id)
	}
	return h, nil
}

// NextSRPID returns the next SRP-ID for PCEP requests.
func (m *Manager) NextSRPID() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSRPID++
	return m.nextSRPID
}

// ByPCC returns all LSPs associated with a given PCC router-id.
func (m *Manager) ByPCC(pcc string) []*LSP {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*LSP
	for _, l := range m.lsps {
		if l.PCC == pcc {
			result = append(result, l)
		}
	}
	return result
}
