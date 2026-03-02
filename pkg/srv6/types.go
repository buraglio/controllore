// Package srv6 provides type definitions for Segment Routing over IPv6 (SRv6)
// as defined in RFC 8402, RFC 8754, RFC 8986, and related IETF documents.
package srv6

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
)

// EndpointBehavior defines the SRv6 LocalSID behavior as per RFC 8986.
// Values are from the IANA SRv6 Endpoint Behaviors registry.
type EndpointBehavior uint16

const (
	// BehaviorEnd represents the "End" function (basic node SID).
	BehaviorEnd EndpointBehavior = 1
	// BehaviorEndX represents "End.X" (layer-3 cross-connect, adj SID).
	BehaviorEndX EndpointBehavior = 5
	// BehaviorEndT represents "End.T" (specific table lookup).
	BehaviorEndT EndpointBehavior = 6
	// BehaviorEndDX4 represents "End.DX4" (decapsulate and L3-forward IPv4 to next-hop).
	BehaviorEndDX4 EndpointBehavior = 9
	// BehaviorEndDX6 represents "End.DX6" (decapsulate and L3-forward IPv6).
	BehaviorEndDX6 EndpointBehavior = 8
	// BehaviorEndDT4 represents "End.DT4" (decap and lookup in specific IPv4 table).
	BehaviorEndDT4 EndpointBehavior = 12
	// BehaviorEndDT6 represents "End.DT6" (decap and lookup in specific IPv6 table).
	BehaviorEndDT6 EndpointBehavior = 11
	// BehaviorEndDT46 represents "End.DT46" (decap and lookup in VRF).
	BehaviorEndDT46 EndpointBehavior = 13
	// BehaviorEndB6Encaps represents "End.B6.Encaps" (binding SID with encapsulation).
	BehaviorEndB6Encaps EndpointBehavior = 17
	// BehaviorEndBpf represents "End.BPF" (BPF-defined behavior, experimental).
	BehaviorEndBpf EndpointBehavior = 65535

	// Micro-SID (uSID) behaviors from draft-ietf-spring-srv6-srh-compression
	// BehaviorNextCSID represents µN (uN) — next-CSID.
	BehaviorNextCSID EndpointBehavior = 0x0042
	// BehaviorNextCSIDX represents µA (uA) — next-CSID with adjacency.
	BehaviorNextCSIDX EndpointBehavior = 0x0043
	// BehaviorNextCSIDDT4 represents µDT4 — next-CSID decap IPv4 table-lookup.
	BehaviorNextCSIDDT4 EndpointBehavior = 0x0049
	// BehaviorNextCSIDDT6 represents µDT6 — next-CSID decap IPv6 table-lookup.
	BehaviorNextCSIDDT6 EndpointBehavior = 0x004B
	// BehaviorNextCSIDDT46 represents µDT46 — next-CSID decap dual-stack table-lookup.
	BehaviorNextCSIDDT46 EndpointBehavior = 0x004D
)

// String returns the human-readable name for an EndpointBehavior.
func (b EndpointBehavior) String() string {
	names := map[EndpointBehavior]string{
		BehaviorEnd:          "End",
		BehaviorEndX:         "End.X",
		BehaviorEndT:         "End.T",
		BehaviorEndDX4:       "End.DX4",
		BehaviorEndDX6:       "End.DX6",
		BehaviorEndDT4:       "End.DT4",
		BehaviorEndDT6:       "End.DT6",
		BehaviorEndDT46:      "End.DT46",
		BehaviorEndB6Encaps:  "End.B6.Encaps",
		BehaviorEndBpf:       "End.BPF",
		BehaviorNextCSID:     "uN",
		BehaviorNextCSIDX:    "uA",
		BehaviorNextCSIDDT4:  "uDT4",
		BehaviorNextCSIDDT6:  "uDT6",
		BehaviorNextCSIDDT46: "uDT46",
	}
	if n, ok := names[b]; ok {
		return n
	}
	return fmt.Sprintf("Unknown(%d)", uint16(b))
}

// SIDType classifies what kind of SID this is within the SRv6 policy context.
type SIDType uint8

const (
	SIDTypeNode    SIDType = iota // Node SID — identifies a node
	SIDTypeAdj                    // Adjacency SID — identifies a specific link
	SIDTypeService                // Service SID — VPN, DT, DX behaviors
	SIDTypeBinding                // Binding SID (BSID) for SR policy
	SIDTypeUSID                   // uSID (micro-SID)
)

// SID represents a single Segment Routing over IPv6 Segment Identifier.
type SID struct {
	// Addr is the 128-bit SRv6 SID address.
	Addr netip.Addr
	// Behavior describes the local behavior at the node owning this SID.
	Behavior EndpointBehavior
	// Type classifies the SID within a policy.
	Type SIDType
	// Structure describes the bit lengths of the SID components (RFC 9252).
	Structure SIDStructure
	// NAI is the optional Node Adjacency Identifier (used in PCEP ERO).
	NAI string
	// Owner is the router-id of the node advertising this SID.
	Owner string
}

// String returns the string representation of the SID.
func (s SID) String() string {
	return fmt.Sprintf("%s(%s)", s.Addr, s.Behavior)
}

// SIDStructure represents the bit-field layout of an SRv6 SID as per RFC 9252.
// Total of LB+LN+Fun+Arg must be <= 128.
type SIDStructure struct {
	// LBLen is the Locator Block length in bits.
	LBLen uint8
	// LNLen is the Locator Node length in bits.
	LNLen uint8
	// FunLen is the Function length in bits.
	FunLen uint8
	// ArgLen is the Arguments length in bits.
	ArgLen uint8
}

// Valid returns true if the structure lengths sum to <= 128.
func (s SIDStructure) Valid() bool {
	return int(s.LBLen)+int(s.LNLen)+int(s.FunLen)+int(s.ArgLen) <= 128
}

// Locator represents an SRv6 SID Locator as advertised by ISIS or BGP-LS.
type Locator struct {
	// Prefix is the locator prefix, e.g., 2001:db8:100::/48.
	Prefix netip.Prefix
	// Metric is the IS-IS metric for the locator.
	Metric uint32
	// Algorithm is the flex-algo number (0 = default SPF, 128-255 = flex-algo).
	Algorithm uint8
	// Structure is the SID structure TLV for this locator.
	Structure SIDStructure
	// SIDs are the SRv6 SIDs (LocalSIDs) advertised within this locator.
	SIDs []SID
	// IsUSID indicates whether this locator uses uSID (µSID) compression.
	IsUSID bool
	// USIDBlock is the carrier prefix + uSID block (the first LBLen bits).
	USIDBlock netip.Prefix
	// Owner is the router advertising this locator.
	Owner string
}

// SegmentList is an ordered set of SRv6 SIDs forming an explicit path.
type SegmentList struct {
	Segments []SID
	// Metric is the computed cost of this path.
	Metric uint32
	// MetricType describes how Metric was computed.
	MetricType MetricType
}

// MetricType indicates which metric was used for path computation.
type MetricType uint8

const (
	MetricIGP      MetricType = 0
	MetricTE       MetricType = 1
	MetricLatency  MetricType = 2
	MetricHopCount MetricType = 3
)

func (m MetricType) String() string {
	switch m {
	case MetricIGP:
		return "igp"
	case MetricTE:
		return "te"
	case MetricLatency:
		return "latency"
	case MetricHopCount:
		return "hopcount"
	default:
		return "unknown"
	}
}

// CompressUSID converts a standard SRv6 segment list into a uSID carrier+list form.
// It groups consecutive SIDs sharing the same uSID block into a single carrier SID
// (RFC-draft-ietf-spring-srv6-srh-compression).
func CompressUSID(segs []SID, blockLen uint8) []SID {
	if len(segs) == 0 {
		return segs
	}

	var compressed []SID
	var carrierBytes [16]byte
	inCarrier := false
	offset := int(blockLen / 8) // byte offset of first uSID slot

	flushCarrier := func() {
		if inCarrier {
			addr, _ := netip.AddrFromSlice(carrierBytes[:])
			compressed = append(compressed, SID{
				Addr:     addr.Unmap(),
				Behavior: BehaviorNextCSID,
				Type:     SIDTypeUSID,
			})
			carrierBytes = [16]byte{}
			inCarrier = false
			offset = int(blockLen / 8)
		}
	}

	for _, s := range segs {
		if s.Type != SIDTypeUSID {
			flushCarrier()
			compressed = append(compressed, s)
			continue
		}

		raw := s.Addr.As16()
		blockBytes := int(blockLen / 8)
		if !inCarrier {
			// Start a new carrier: copy the block prefix from this SID
			copy(carrierBytes[:blockBytes], raw[:blockBytes])
			inCarrier = true
		}

		// Copy the uSID (node + function bits) into the next slot
		nodeLen := s.Structure.LNLen + s.Structure.FunLen
		nodeBytes := int((nodeLen + 7) / 8)
		if offset+nodeBytes <= 16 {
			copy(carrierBytes[offset:offset+nodeBytes], raw[blockBytes:blockBytes+nodeBytes])
			offset += nodeBytes
		} else {
			// Carrier full — flush and start new
			flushCarrier()
			inCarrier = true
			copy(carrierBytes[:blockBytes], raw[:blockBytes])
			copy(carrierBytes[offset:offset+nodeBytes], raw[blockBytes:blockBytes+nodeBytes])
			offset += nodeBytes
		}
	}
	flushCarrier()

	return compressed
}

// ParseSIDFromBytes parses a 16-byte slice into a netip.Addr SRv6 SID.
func ParseSIDFromBytes(b []byte) (netip.Addr, error) {
	if len(b) != 16 {
		return netip.Addr{}, fmt.Errorf("SRv6 SID must be 16 bytes, got %d", len(b))
	}
	var raw [16]byte
	copy(raw[:], b)
	return netip.AddrFrom16(raw), nil
}

// SIDToBytes returns the 16-byte wire encoding of an SRv6 SID.
func SIDToBytes(addr netip.Addr) []byte {
	raw := addr.As16()
	result := make([]byte, 16)
	copy(result, raw[:])
	return result
}

// EncodeLocatorPrefix encodes the locator prefix as a BGP-LS NLRI prefix.
func EncodeLocatorPrefix(loc Locator) []byte {
	prefixBytes := loc.Prefix.Addr().As16()
	bits := loc.Prefix.Bits()
	buf := make([]byte, 2+((bits+7)/8))
	binary.BigEndian.PutUint16(buf[0:2], uint16(bits))
	copy(buf[2:], prefixBytes[:(bits+7)/8])
	return buf
}

// NodeSegmentID is the advertised SRv6 Node SID for a given node (End behavior).
func NodeSegmentID(locator Locator) *SID {
	for _, s := range locator.SIDs {
		if s.Behavior == BehaviorEnd || s.Behavior == BehaviorNextCSID {
			return &s
		}
	}
	return nil
}

// AdjacencySID returns the SRv6 Adjacency SID on a link from the locator SIDs.
func AdjacencySID(locator Locator) *SID {
	for _, s := range locator.SIDs {
		if s.Behavior == BehaviorEndX || s.Behavior == BehaviorNextCSIDX {
			return &s
		}
	}
	return nil
}

// IsGlobalUnicast checks whether a SID addr is a valid global unicast IPv6 address.
func IsGlobalUnicast(addr netip.Addr) bool {
	raw := addr.As16()
	ip := net.IP(raw[:])
	return ip.IsGlobalUnicast()
}
