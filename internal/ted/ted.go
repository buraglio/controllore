// Package ted implements the Traffic Engineering Database (TED) for Controllore.
// It maintains a live view of network topology: nodes, links, SRv6 locators,
// and flex-algorithm domains, sourced from BGP-LS and/or IS-IS adjacencies.
package ted

import (
	"net/netip"
	"sync"
	"time"

	"github.com/buraglio/controllore/pkg/srv6"
	"github.com/google/uuid"
)

// NodeCapabilities captures the SR capabilities advertised by a node.
type NodeCapabilities struct {
	// SRMPLSCapable indicates SR-MPLS support.
	SRMPLSCapable bool
	// SRv6Capable indicates SRv6 support.
	SRv6Capable bool
	// MSD is the Maximum SID Depth for SR-MPLS.
	MSD uint8
	// SRv6MSD is the Maximum SID Depth for SRv6 (SRv6-MSD, type 41).
	SRv6MSD uint8
	// SRv6USIDCapable indicates support for SRv6 uSID compression.
	SRv6USIDCapable bool
}

// Node represents a router visible to the PCE through BGP-LS or IS-IS.
type Node struct {
	ID uuid.UUID
	// RouterID is the unique identifier: for IPv4-topology, the TE Router ID.
	// For SRv6-only nodes it may be the IPv6 loopback.
	RouterID string
	Hostname string
	// ISISAreaIDs are the configured IS-IS area identifiers (NET format).
	ISISAreaIDs  []string
	ASN          uint32
	Capabilities NodeCapabilities
	// SRv6Locators are all locators advertised by this node.
	SRv6Locators []srv6.Locator
	// FlexAlgos is the set of flex-algorithm IDs this node participates in.
	FlexAlgos []uint8
	// IPv4 management address (may be zero if IPv6-only).
	ManagementIPv4 netip.Addr
	// IPv6 loopback / management address.
	ManagementIPv6 netip.Addr
	// LastSeen is when the node NLRI was last updated from BGP-LS.
	LastSeen time.Time
	// Source tracks where this node was learned from.
	Source string // "bgp-ls" | "isis-direct" | "static"
}

// Link represents a TE-capable adjacency between two nodes.
type Link struct {
	ID uuid.UUID
	// LocalNodeID and RemoteNodeID reference Node.RouterID values.
	LocalNodeID  string
	RemoteNodeID string
	// LocalIP and RemoteIP are the interface addresses on this link.
	LocalIP  netip.Addr
	RemoteIP netip.Addr
	// LocalAdjSID is the SRv6 Adjacency SID for this link direction.
	LocalAdjSID *srv6.SID
	// TEMetric is the TE metric (RFC 3630/5305).
	TEMetric uint32
	// IGPMetric is the IS-IS metric.
	IGPMetric uint32
	// MaxBandwidth is the maximum reservable bandwidth in bytes/sec.
	MaxBandwidth uint64
	// ReservedBandwidth is the currently reserved bandwidth in bytes/sec.
	ReservedBandwidth uint64
	// AdminGroup is the link admin group bitmask (RFC 3630 §2.5.3).
	AdminGroup uint32
	// SRLG is the Shared Risk Link Group membership list.
	SRLG []uint32
	// Latency is the measured one-way latency in microseconds.
	Latency uint32
	// FlexAlgoMetrics stores per-algo link metrics.
	FlexAlgoMetrics map[uint8]uint32
	// LastSeen is when this link was last updated.
	LastSeen time.Time
}

// TED is the thread-safe Traffic Engineering Database.
type TED struct {
	mu    sync.RWMutex
	nodes map[string]*Node // keyed by RouterID
	links map[string]*Link // keyed by LocalNodeID+RemoteNodeID+LocalIP
	// Reverse index: links by node
	nodeLinks map[string][]*Link

	// Optional persistence callbacks — called while NOT holding the lock
	onUpsertNode func(*Node)
	onUpsertLink func(*Link)
	onDeleteNode func(string)
}

// New creates an empty TED.
func New() *TED {
	return &TED{
		nodes:     make(map[string]*Node),
		links:     make(map[string]*Link),
		nodeLinks: make(map[string][]*Link),
	}
}

// SetOnUpsertNode registers a callback invoked after every node upsert.
func (t *TED) SetOnUpsertNode(fn func(*Node)) { t.onUpsertNode = fn }

// SetOnUpsertLink registers a callback invoked after every link upsert.
func (t *TED) SetOnUpsertLink(fn func(*Link)) { t.onUpsertLink = fn }

// SetOnDeleteNode registers a callback invoked after every node deletion.
func (t *TED) SetOnDeleteNode(fn func(string)) { t.onDeleteNode = fn }

// linkKey builds a stable map key for a link given its endpoints.
func linkKey(localNodeID, remoteNodeID string, localIP netip.Addr) string {
	return localNodeID + "|" + remoteNodeID + "|" + localIP.String()
}

// UpsertNode inserts or updates a node in the TED.
func (t *TED) UpsertNode(n *Node) {
	t.mu.Lock()
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	n.LastSeen = time.Now()
	t.nodes[n.RouterID] = n
	cb := t.onUpsertNode
	t.mu.Unlock()
	if cb != nil {
		cb(n)
	}
}

// DeleteNode removes a node from the TED by RouterID.
func (t *TED) DeleteNode(routerID string) {
	t.mu.Lock()
	delete(t.nodes, routerID)
	cb := t.onDeleteNode
	t.mu.Unlock()
	if cb != nil {
		cb(routerID)
	}
}

// GetNode returns a node by RouterID (nil if not found).
func (t *TED) GetNode(routerID string) *Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nodes[routerID]
}

// AllNodes returns a snapshot of all nodes.
func (t *TED) AllNodes() []*Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]*Node, 0, len(t.nodes))
	for _, n := range t.nodes {
		result = append(result, n)
	}
	return result
}

// UpsertLink inserts or updates a link in the TED.
func (t *TED) UpsertLink(l *Link) {
	t.mu.Lock()
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	l.LastSeen = time.Now()
	key := linkKey(l.LocalNodeID, l.RemoteNodeID, l.LocalIP)
	t.links[key] = l

	// Update node → link index
	found := false
	for i, existing := range t.nodeLinks[l.LocalNodeID] {
		if existing.ID == l.ID {
			t.nodeLinks[l.LocalNodeID][i] = l
			found = true
			break
		}
	}
	if !found {
		t.nodeLinks[l.LocalNodeID] = append(t.nodeLinks[l.LocalNodeID], l)
	}
	cb := t.onUpsertLink
	t.mu.Unlock()
	if cb != nil {
		cb(l)
	}
}

// DeleteLink removes a link identified by its endpoints.
func (t *TED) DeleteLink(localNodeID, remoteNodeID string, localIP netip.Addr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := linkKey(localNodeID, remoteNodeID, localIP)
	if l, ok := t.links[key]; ok {
		delete(t.links, key)
		links := t.nodeLinks[l.LocalNodeID]
		for i, existing := range links {
			if existing.ID == l.ID {
				t.nodeLinks[l.LocalNodeID] = append(links[:i], links[i+1:]...)
				break
			}
		}
	}
}

// AllLinks returns a snapshot of all links.
func (t *TED) AllLinks() []*Link {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]*Link, 0, len(t.links))
	for _, l := range t.links {
		result = append(result, l)
	}
	return result
}

// LinksFromNode returns all links where the given routerID is the local node.
func (t *TED) LinksFromNode(routerID string) []*Link {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nodeLinks[routerID]
}

// Stats returns a summary of TED contents.
func (t *TED) Stats() TEDStats {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TEDStats{
		NodeCount: len(t.nodes),
		LinkCount: len(t.links),
	}
}

// TEDStats is a summary of current TED contents.
type TEDStats struct {
	NodeCount int `json:"node_count"`
	LinkCount int `json:"link_count"`
}

// AllSRv6Locators returns all SRv6 locators across all nodes.
func (t *TED) AllSRv6Locators() []srv6.Locator {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var locs []srv6.Locator
	for _, n := range t.nodes {
		locs = append(locs, n.SRv6Locators...)
	}
	return locs
}

// AllSRv6SIDs returns all SRv6 SIDs (LocalSIDs) across all nodes.
func (t *TED) AllSRv6SIDs() []srv6.SID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var sids []srv6.SID
	for _, n := range t.nodes {
		for _, loc := range n.SRv6Locators {
			sids = append(sids, loc.SIDs...)
		}
	}
	return sids
}
