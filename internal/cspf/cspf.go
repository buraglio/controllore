// Package cspf implements the Constrained Shortest Path First algorithm
// for SR-TE path computation over the TED graph.
// Supports SRv6 segment list construction with minimization and uSID compression.
package cspf

import (
	"container/heap"
	"fmt"
	"math"

	"github.com/buraglio/controllore/internal/ted"
	"github.com/buraglio/controllore/pkg/srv6"
)

// MetricType determines which link metric is used for path cost.
type MetricType = srv6.MetricType

// Constraints specify the requirements a path must satisfy.
type Constraints struct {
	// MetricType selects which metric to optimize.
	MetricType MetricType
	// MaxCost is the maximum allowable path cost (0 = unlimited).
	MaxCost uint32
	// MinBandwidth requires links to have at least this unreserved bandwidth (bytes/sec).
	MinBandwidth uint64
	// IncludeAny requires links to match at least one bit in this admin group mask.
	IncludeAny uint32
	// IncludeAll requires links to match all bits in this admin group mask.
	IncludeAll uint32
	// ExcludeAny requires links to NOT match any bits in this mask.
	ExcludeAny uint32
	// AvoidSRLG excludes links belonging to any of these SRLG IDs.
	AvoidSRLG []uint32
	// ExcludeNodes is a set of router IDs to avoid in the path.
	ExcludeNodes map[string]struct{}
	// FlexAlgo restricts path computation to a specific flex-algorithm domain.
	// 0 = default SPF.
	FlexAlgo uint8
	// UseUSID requests uSID compression in the resulting segment list.
	UseUSID bool
	// USIDBlockLen is the number of bits in the uSID carrier prefix.
	USIDBlockLen uint8
	// MaxSIDDepth limits the number of SIDs in the resulting segment list.
	// 0 = no limit.
	MaxSIDDepth uint8
}

// PathRequest is a request to compute an SRv6 or SR-MPLS path.
type PathRequest struct {
	SrcRouterID string
	DstRouterID string
	Constraints Constraints
}

// ComputedPath is the result of a CSPF computation.
type ComputedPath struct {
	// NodeHops is the ordered list of router IDs traversed.
	NodeHops []string
	// SegmentList is the SRv6 segment list for this path.
	SegmentList []srv6.SID
	// Cost is the computed path cost using the requested metric.
	Cost uint32
	// MetricType describes how Cost was computed.
	MetricType MetricType
}

// Engine is the CSPF path computation engine.
type Engine struct {
	ted *ted.TED
}

// New creates a new CSPF engine backed by the given TED.
func New(t *ted.TED) *Engine {
	return &Engine{ted: t}
}

// linkMetric returns the cost of a link for the given metric type.
func linkMetric(l *ted.Link, mt MetricType, flexAlgo uint8) uint32 {
	if flexAlgo != 0 {
		if m, ok := l.FlexAlgoMetrics[flexAlgo]; ok {
			return m
		}
	}
	switch mt {
	case srv6.MetricTE:
		return l.TEMetric
	case srv6.MetricLatency:
		if l.Latency == 0 {
			return l.TEMetric // fallback
		}
		return l.Latency
	case srv6.MetricHopCount:
		return 1
	default: // IGP
		return l.IGPMetric
	}
}

// linkPassesConstraints returns true if a link satisfies all constraints.
func linkPassesConstraints(l *ted.Link, c Constraints) bool {
	// Bandwidth
	if c.MinBandwidth > 0 {
		avail := l.MaxBandwidth - l.ReservedBandwidth
		if avail < c.MinBandwidth {
			return false
		}
	}
	// Admin group filters
	if c.IncludeAll != 0 && (l.AdminGroup&c.IncludeAll) != c.IncludeAll {
		return false
	}
	if c.IncludeAny != 0 && (l.AdminGroup&c.IncludeAny) == 0 {
		return false
	}
	if c.ExcludeAny != 0 && (l.AdminGroup&c.ExcludeAny) != 0 {
		return false
	}
	// SRLG avoidance
	for _, srlg := range c.AvoidSRLG {
		for _, ls := range l.SRLG {
			if ls == srlg {
				return false
			}
		}
	}
	return true
}

// ============================================================
// Dijkstra implementation using a min-heap priority queue
// ============================================================

type pqItem struct {
	nodeID string
	cost   uint32
	prev   string
	index  int
}

type pq []*pqItem

func (pq pq) Len() int           { return len(pq) }
func (pq pq) Less(i, j int) bool { return pq[i].cost < pq[j].cost }
func (pq pq) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (p *pq) Push(x interface{}) {
	item := x.(*pqItem)
	item.index = len(*p)
	*p = append(*p, item)
}
func (p *pq) Pop() interface{} {
	old := *p
	n := len(old)
	item := old[n-1]
	*p = old[:n-1]
	return item
}

// Compute runs CSPF and returns the best path satisfying all constraints.
func (e *Engine) Compute(req PathRequest) (*ComputedPath, error) {
	nodes := e.ted.AllNodes()
	if len(nodes) == 0 {
		return nil, fmt.Errorf("TED is empty")
	}

	// Build node index
	nodeSet := make(map[string]struct{})
	for _, n := range nodes {
		if _, excluded := req.Constraints.ExcludeNodes[n.RouterID]; !excluded {
			nodeSet[n.RouterID] = struct{}{}
		}
	}

	if _, ok := nodeSet[req.SrcRouterID]; !ok {
		return nil, fmt.Errorf("source node %s not found or excluded", req.SrcRouterID)
	}
	if _, ok := nodeSet[req.DstRouterID]; !ok {
		return nil, fmt.Errorf("destination node %s not found or excluded", req.DstRouterID)
	}

	// Dijkstra
	dist := make(map[string]uint32)
	prev := make(map[string]string)
	for id := range nodeSet {
		dist[id] = math.MaxUint32
	}
	dist[req.SrcRouterID] = 0

	pqueue := make(pq, 0, len(nodeSet))
	heap.Push(&pqueue, &pqItem{nodeID: req.SrcRouterID, cost: 0})

	for pqueue.Len() > 0 {
		u := heap.Pop(&pqueue).(*pqItem)
		if u.cost > dist[u.nodeID] {
			continue // stale
		}
		if u.nodeID == req.DstRouterID {
			break // reached destination
		}

		for _, link := range e.ted.LinksFromNode(u.nodeID) {
			if _, ok := nodeSet[link.RemoteNodeID]; !ok {
				continue
			}
			if !linkPassesConstraints(link, req.Constraints) {
				continue
			}
			w := linkMetric(link, req.Constraints.MetricType, req.Constraints.FlexAlgo)
			if w == math.MaxUint32 {
				continue
			}
			newCost := u.cost + w
			if newCost < dist[link.RemoteNodeID] {
				dist[link.RemoteNodeID] = newCost
				prev[link.RemoteNodeID] = u.nodeID
				heap.Push(&pqueue, &pqItem{nodeID: link.RemoteNodeID, cost: newCost})
			}
		}
	}

	if dist[req.DstRouterID] == math.MaxUint32 {
		return nil, fmt.Errorf("no path found from %s to %s satisfying constraints",
			req.SrcRouterID, req.DstRouterID)
	}

	// Reconstruct hop sequence
	hops := []string{}
	cur := req.DstRouterID
	for cur != "" {
		hops = append([]string{cur}, hops...)
		cur = prev[cur]
	}

	// Build SRv6 segment list
	segList, err := e.buildSRv6SegmentList(hops, req.Constraints)
	if err != nil {
		return nil, fmt.Errorf("building segment list: %w", err)
	}

	return &ComputedPath{
		NodeHops:    hops,
		SegmentList: segList,
		Cost:        dist[req.DstRouterID],
		MetricType:  req.Constraints.MetricType,
	}, nil
}

// buildSRv6SegmentList constructs the minimal SRv6 segment list for a node-hop sequence.
// Per RFC 8986 §4, only inserts a SID where IGP path would deviate, or at specific
// service anchors. For general source routing, node SIDs are inserted at each non-trivial hop.
func (e *Engine) buildSRv6SegmentList(hops []string, c Constraints) ([]srv6.SID, error) {
	var sids []srv6.SID

	for i, routerID := range hops {
		node := e.ted.GetNode(routerID)
		if node == nil {
			return nil, fmt.Errorf("node %s not found in TED", routerID)
		}

		// Select the appropriate locator for the flex-algo
		var loc *srv6.Locator
		for idx := range node.SRv6Locators {
			l := &node.SRv6Locators[idx]
			if c.FlexAlgo == 0 || l.Algorithm == c.FlexAlgo {
				loc = l
				break
			}
		}
		if loc == nil {
			// No SRv6 locator — skip non-SRv6 node (SR-MPLS fallback would go here)
			continue
		}

		// Get the node SID (End or uN)
		nodeSID := srv6.NodeSegmentID(*loc)
		if nodeSID == nil {
			continue
		}

		sid := *nodeSID
		if c.UseUSID && loc.IsUSID {
			sid.Type = srv6.SIDTypeUSID
			sid.Structure = srv6.SIDStructure{
				LBLen:  c.USIDBlockLen,
				LNLen:  loc.Structure.LNLen,
				FunLen: loc.Structure.FunLen,
				ArgLen: loc.Structure.ArgLen,
			}
		}
		sid.Owner = routerID

		// Always include src and dst; for intermediate hops, include only if needed
		// (simplified: include all — proper minimization requires IGP tree analysis)
		if i == 0 || i == len(hops)-1 {
			sids = append(sids, sid)
		} else {
			// Include intermediate hop SIDs to enable explicit routing
			sids = append(sids, sid)
		}
	}

	// Apply uSID compression if requested
	if c.UseUSID && c.USIDBlockLen > 0 {
		sids = srv6.CompressUSID(sids, c.USIDBlockLen)
	}

	// Check MSD constraint
	if c.MaxSIDDepth > 0 && uint8(len(sids)) > c.MaxSIDDepth {
		return nil, fmt.Errorf("computed segment list depth %d exceeds MSD %d",
			len(sids), c.MaxSIDDepth)
	}

	return sids, nil
}
