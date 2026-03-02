// Package pcep — PCEP message handlers.
// This file implements parsers and dispatchers for PCRpt, PCUpd, PCReq,
// and PCInitiate message bodies.  The handlers are invoked by Server.dispatch()
// and update the LSP manager accordingly.
package pcep

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/rs/zerolog/log"

	"github.com/buraglio/controllore/pkg/srv6"
)

// ============================================================
// Object Decoders
// ============================================================

// DecodedObjects is the result of scanning a list of PCEP objects.
type DecodedObjects struct {
	SRP       *SRPObject
	LSP       *LSPObject
	ERO       []EROSubobject
	Metric    *MetricObject
	Endpoint  *EndpointObject
	LSPA      *LSPAObject
	Bandwidth *BandwidthObject
}

// EROSubobject is a parsed ERO subobject (SR, SRv6, or plain IP).
type EROSubobject struct {
	Type     EROSubobjectType
	Loose    bool
	IPv4Addr netip.Addr // SubobjIPv4Prefix
	IPv6Addr netip.Addr // SubobjIPv6Prefix
	SRv6Sub  *SRv6EROSubobject
	SRLabel  uint32 // SR-MPLS label
	NAIType  NAIType
	NAIData  []byte
}

// MetricObject (RFC 5440 §7.8) carries the path metric.
type MetricObject struct {
	Type     uint8
	Bound    bool // B-flag
	Computed bool // C-flag
	Value    float32
}

// EndpointObject (RFC 5440 §7.6) carries path endpoints.
type EndpointObject struct {
	SrcIPv4 netip.Addr
	DstIPv4 netip.Addr
	SrcIPv6 netip.Addr
	DstIPv6 netip.Addr
}

// LSPAObject (RFC 5440 §7.11) carries LSP attributes.
type LSPAObject struct {
	ExcludeAny uint32
	IncludeAny uint32
	IncludeAll uint32
	SetupPrio  uint8
	HoldPrio   uint8
	LocalProt  bool
}

// BandwidthObject (RFC 5440 §7.7) carries bandwidth constraints.
type BandwidthObject struct {
	BandwidthBPS float32
}

// DecodePCRpt parses a PCRpt message body and returns all decoded objects.
// A PCRpt may carry multiple State Reports (SRP, LSP, ERO, ...).
func DecodePCRpt(body []byte) ([]DecodedObjects, error) {
	var reports []DecodedObjects
	cur := &DecodedObjects{}
	offset := 0
	hasLSP := false

	for offset+4 <= len(body) {
		objHdr, err := DecodeObjectHeader(body[offset : offset+4])
		if err != nil {
			return nil, fmt.Errorf("object header at offset %d: %w", offset, err)
		}
		objLen := int(objHdr.Length)
		if objLen < 4 || offset+objLen > len(body) {
			return nil, fmt.Errorf("object at offset %d: invalid length %d", offset, objLen)
		}
		objBody := body[offset+4 : offset+objLen]

		switch objHdr.ObjectClass {
		case ObjSRP:
			// If we already have an LSP object, this starts a new report
			if hasLSP {
				reports = append(reports, *cur)
				cur = &DecodedObjects{}
				hasLSP = false
			}
			srp, err := decodeSRPObject(objBody)
			if err == nil {
				cur.SRP = srp
			}

		case ObjLSP:
			lspObj, err := decodeLSPObject(objBody)
			if err == nil {
				cur.LSP = lspObj
				hasLSP = true
			}

		case ObjERO:
			cur.ERO, err = decodeEROObjects(objBody)
			if err != nil {
				log.Warn().Err(err).Msg("PCEP: ERO decode error")
			}

		case ObjMetric:
			m, err := decodeMetricObject(objBody)
			if err == nil {
				cur.Metric = m
			}

		case ObjEndPoints:
			ep, err := decodeEndpointObject(objHdr.ObjectType, objBody)
			if err == nil {
				cur.Endpoint = ep
			}

		case ObjLSPA:
			la, err := decodeLSPAObject(objBody)
			if err == nil {
				cur.LSPA = la
			}

		case ObjBandwidth:
			bw, err := decodeBandwidthObject(objBody)
			if err == nil {
				cur.Bandwidth = bw
			}
		}

		// Round up to 4-byte alignment
		aligned := (objLen + 3) &^ 3
		offset += aligned
	}

	if hasLSP {
		reports = append(reports, *cur)
	}
	return reports, nil
}

// DecodePCUpd parses a PCUpd message body.
func DecodePCUpd(body []byte) ([]DecodedObjects, error) {
	return DecodePCRpt(body) // same object ordering
}

// DecodePCInitiate parses a PCInitiate message body (RFC 8281).
func DecodePCInitiate(body []byte) ([]DecodedObjects, error) {
	return DecodePCRpt(body) // same object ordering
}

// ============================================================
// Individual Object Decoders
// ============================================================

func decodeSRPObject(body []byte) (*SRPObject, error) {
	if len(body) < 8 {
		return nil, fmt.Errorf("SRP object too short: %d", len(body))
	}
	flags := binary.BigEndian.Uint32(body[0:4])
	srpid := binary.BigEndian.Uint32(body[4:8])
	srp := &SRPObject{
		SRPID:  srpid,
		Remove: flags&0x01 != 0,
	}
	if len(body) > 8 {
		tlvs, _ := DecodeTLVs(body[8:])
		srp.TLVs = tlvs
	}
	return srp, nil
}

func decodeLSPObject(body []byte) (*LSPObject, error) {
	if len(body) < 4 {
		return nil, fmt.Errorf("LSP object too short: %d", len(body))
	}
	// LSP object: 4 bytes flags+PLSP-ID, then TLVs
	// Bits 31-12: PLSP-ID (20 bits)
	// Bits 11-8: O (operational) (3 bits at 11-9)
	// Bits 7: A-bit
	// Bits 6: R-bit
	// Bits 5: S-bit
	// Bits 4: D-bit
	// Bits 3-0: reserved
	word := binary.BigEndian.Uint32(body[0:4])
	plspID := word >> 12
	opStatus := LSPOperationalStatus((word >> 9) & 0x07)
	lsp := &LSPObject{
		PLSPID:   plspID,
		OpStatus: opStatus,
		Admin:    word&0x100 != 0, // A-bit bit 8
		Remove:   word&0x080 != 0, // R-bit bit 7
		Sync:     word&0x040 != 0, // S-bit bit 6
		Delegate: word&0x010 != 0, // D-bit bit 4
		Create:   word&0x008 != 0, // C-bit bit 3 (RFC 8281)
	}
	if len(body) > 4 {
		tlvs, _ := DecodeTLVs(body[4:])
		lsp.TLVs = tlvs
	}
	return lsp, nil
}

func decodeEROObjects(body []byte) ([]EROSubobject, error) {
	var subobjs []EROSubobject
	offset := 0
	for offset+2 <= len(body) {
		typeByte := body[offset]
		subLen := int(body[offset+1])
		if subLen < 2 || offset+subLen > len(body) {
			break
		}
		loose := typeByte&0x80 != 0
		soType := EROSubobjectType(typeByte & 0x7F)
		soBody := body[offset+2 : offset+subLen]

		sub := EROSubobject{Type: soType, Loose: loose}
		switch soType {
		case SubobjIPv4Prefix:
			if len(soBody) >= 5 {
				addr, _ := netip.AddrFromSlice(soBody[0:4])
				sub.IPv4Addr = addr.Unmap()
			}
		case SubobjIPv6Prefix:
			if len(soBody) >= 17 {
				addr, _ := netip.AddrFromSlice(soBody[0:16])
				sub.IPv6Addr = addr
			}
		case SubobjSRv6:
			srv6sub, err := decodeSRv6EROSubobject(soBody)
			if err == nil {
				sub.SRv6Sub = srv6sub
			}
		case SubobjSR:
			// SR-MPLS: 4 bytes flags+NT+reserved, then optional NAI and label
			if len(soBody) >= 4 {
				naiType := NAIType((soBody[0] >> 4) & 0x0F)
				sub.NAIType = naiType
				// Label stack entry (20-bit label) in bytes 4-7
				if len(soBody) >= 8 {
					lse := binary.BigEndian.Uint32(soBody[4:8])
					sub.SRLabel = lse >> 12 // top 20 bits
				}
			}
		}
		subobjs = append(subobjs, sub)
		// ERO subobjects don't pad, length is exact
		offset += subLen
	}
	return subobjs, nil
}

// decodeSRv6EROSubobject decodes an SRv6 ERO subobject body.
// Format (draft-ietf-pce-segment-routing-ipv6 §5.1):
//
//	2 bytes flags (NAI-type[3:0] | F|S|C|M bits), 1 reserved, 2 endpoint behavior, 16 SID, NAI
func decodeSRv6EROSubobject(body []byte) (*SRv6EROSubobject, error) {
	if len(body) < 20 {
		return nil, fmt.Errorf("SRv6 ERO subobject too short: %d", len(body))
	}
	flags := binary.BigEndian.Uint16(body[0:2])
	naiType := NAIType(flags >> 12)
	sidAbsent := flags&0x0800 != 0
	naiAbsent := flags&0x0400 != 0
	computed := flags&0x0200 != 0
	isUSID := flags&0x0100 != 0
	behavior := binary.BigEndian.Uint16(body[3:5])

	sub := &SRv6EROSubobject{
		NAIType:          naiType,
		SIDAbsent:        sidAbsent,
		NAIAbsent:        naiAbsent,
		Computed:         computed,
		IsUSID:           isUSID,
		EndpointBehavior: behavior,
	}
	copy(sub.SID[:], body[5:21])

	// NAI follows SID if not absent
	if !naiAbsent && len(body) > 21 {
		naiLen := naiLength(naiType)
		if len(body) >= 21+naiLen {
			sub.NAI = body[21 : 21+naiLen]
		}
	}
	return sub, nil
}

// naiLength returns the byte length of the NAI for a given type.
func naiLength(t NAIType) int {
	switch t {
	case NAIIPv4NodeID:
		return 4
	case NAIIPv6NodeID:
		return 16
	case NAIIPv4AdjacencyID:
		return 8 // src(4) + dst(4)
	case NAIIPv6AdjacencyID:
		return 32
	case NAIIPv6LocalRemote:
		return 32
	default:
		return 0
	}
}

func decodeMetricObject(body []byte) (*MetricObject, error) {
	if len(body) < 8 {
		return nil, fmt.Errorf("metric object too short")
	}
	// bytes 0-1: reserved, byte 2: flags, byte 3: T (type)
	// bytes 4-7: value (IEEE 754 float)
	flags := body[2]
	metricType := body[3]
	bits := binary.BigEndian.Uint32(body[4:8])
	val := float32frombits(bits)
	return &MetricObject{
		Type:     metricType,
		Bound:    flags&0x02 != 0,
		Computed: flags&0x01 != 0,
		Value:    val,
	}, nil
}

func decodeEndpointObject(objType uint8, body []byte) (*EndpointObject, error) {
	ep := &EndpointObject{}
	switch objType {
	case 1: // IPv4
		if len(body) < 8 {
			return nil, fmt.Errorf("endpoint IPv4 too short")
		}
		src, _ := netip.AddrFromSlice(body[0:4])
		dst, _ := netip.AddrFromSlice(body[4:8])
		ep.SrcIPv4 = src.Unmap()
		ep.DstIPv4 = dst.Unmap()
	case 2: // IPv6
		if len(body) < 32 {
			return nil, fmt.Errorf("endpoint IPv6 too short")
		}
		src, _ := netip.AddrFromSlice(body[0:16])
		dst, _ := netip.AddrFromSlice(body[16:32])
		ep.SrcIPv6 = src
		ep.DstIPv6 = dst
	}
	return ep, nil
}

func decodeLSPAObject(body []byte) (*LSPAObject, error) {
	if len(body) < 16 {
		return nil, fmt.Errorf("LSPA object too short")
	}
	return &LSPAObject{
		ExcludeAny: binary.BigEndian.Uint32(body[0:4]),
		IncludeAny: binary.BigEndian.Uint32(body[4:8]),
		IncludeAll: binary.BigEndian.Uint32(body[8:12]),
		SetupPrio:  body[12],
		HoldPrio:   body[13],
		LocalProt:  body[14]&0x01 != 0,
	}, nil
}

func decodeBandwidthObject(body []byte) (*BandwidthObject, error) {
	if len(body) < 4 {
		return nil, fmt.Errorf("bandwidth object too short")
	}
	bits := binary.BigEndian.Uint32(body[0:4])
	return &BandwidthObject{BandwidthBPS: float32frombits(bits)}, nil
}

// ============================================================
// Message Builders (PCInitiate / PCUpdate)
// ============================================================

// BuildPCInitiate builds a PCInitiate message body for creating an LSP.
// All SRv6 SID segments are encoded as SRv6 ERO subobjects.
func BuildPCInitiate(srpID uint32, plspID uint32, name string,
	src, dst netip.Addr, segs []srv6.SID, isUSID bool) []byte {

	var body []byte

	// ── SRP object (ObjSRP, class=33) ─────────────────────────
	srpFlags := make([]byte, 8)
	binary.BigEndian.PutUint32(srpFlags[4:], srpID)
	// Path-Setup-Type TLV (PST=3 for SRv6)
	pstTLV := TLV{Type: TLVPathSetupType, Value: []byte{0, 0, 0, PathSetupSRv6}}
	srpBody := append(srpFlags, pstTLV.Encode()...)
	body = append(body, encodeObject(ObjSRP, 1, true, srpBody)...)

	// ── LSP object (ObjLSP, class=32) ──────────────────────────
	// PLSP-ID 0 = new (PCE-initiated)
	lspWord := plspID << 12
	lspWord |= uint32(LSPOperDown) << 9 // O=0 (down)
	lspWord |= 0x0C                     // A-bit + C-bit (PCE-initiated)
	lspBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lspBytes, lspWord)
	// Add Symbolic-Path-Name TLV
	if name != "" {
		nameTLV := TLV{Type: TLVSymbolicPathName, Value: []byte(name)}
		lspBytes = append(lspBytes, nameTLV.Encode()...)
	}
	body = append(body, encodeObject(ObjLSP, 1, true, lspBytes)...)

	// ── Endpoints object (ObjEndPoints) ───────────────────────
	var epBody []byte
	if src.Is6() || dst.Is6() {
		// IPv6 endpoints (type 2)
		s6 := src.As16()
		d6 := dst.As16()
		epBody = append(s6[:], d6[:]...)
		body = append(body, encodeObjectType(ObjEndPoints, 2, true, epBody)...)
	} else {
		s4 := src.As4()
		d4 := dst.As4()
		epBody = append(s4[:], d4[:]...)
		body = append(body, encodeObjectType(ObjEndPoints, 1, true, epBody)...)
	}

	// ── ERO object ─────────────────────────────────────────────
	var eroBody []byte
	for _, seg := range segs {
		sidBytes := seg.Addr.As16()
		sub := &SRv6EROSubobject{
			NAIType:          NAIAbsent,
			NAIAbsent:        true,
			IsUSID:           isUSID,
			EndpointBehavior: uint16(seg.Behavior),
		}
		copy(sub.SID[:], sidBytes[:])
		if seg.Structure.LBLen != 0 {
			sub.SIDStructure = &SIDStructureSubTLV{
				LBLen:  seg.Structure.LBLen,
				LNLen:  seg.Structure.LNLen,
				FunLen: seg.Structure.FunLen,
				ArgLen: seg.Structure.ArgLen,
			}
		}
		eroBody = append(eroBody, sub.Encode(false)...)
	}
	body = append(body, encodeObject(ObjERO, 1, true, eroBody)...)

	// Wrap in PCEP message header
	msgHdr := CommonHeader{
		Version:     1,
		MessageType: MsgPCInitiate,
		Length:      uint16(4 + len(body)),
	}
	return append(msgHdr.Encode(), body...)
}

// BuildPCUpdate builds a PCUpd message to update an LSP's segment list.
func BuildPCUpdate(srpID uint32, plspID uint32, segs []srv6.SID, isUSID bool) []byte {
	var body []byte

	// SRP object
	srpFlags := make([]byte, 8)
	binary.BigEndian.PutUint32(srpFlags[4:], srpID)
	pstTLV := TLV{Type: TLVPathSetupType, Value: []byte{0, 0, 0, PathSetupSRv6}}
	srpBody := append(srpFlags, pstTLV.Encode()...)
	body = append(body, encodeObject(ObjSRP, 1, true, srpBody)...)

	// LSP object
	lspWord := plspID << 12
	lspWord |= 0x10 // D-bit: delegate
	lspBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lspBytes, lspWord)
	body = append(body, encodeObject(ObjLSP, 1, true, lspBytes)...)

	// ERO object
	var eroBody []byte
	for _, seg := range segs {
		sidBytes := seg.Addr.As16()
		sub := &SRv6EROSubobject{
			NAIType:          NAIAbsent,
			NAIAbsent:        true,
			IsUSID:           isUSID,
			EndpointBehavior: uint16(seg.Behavior),
		}
		copy(sub.SID[:], sidBytes[:])
		eroBody = append(eroBody, sub.Encode(false)...)
	}
	body = append(body, encodeObject(ObjERO, 1, true, eroBody)...)

	msgHdr := CommonHeader{
		Version:     1,
		MessageType: MsgPCUpd,
		Length:      uint16(4 + len(body)),
	}
	return append(msgHdr.Encode(), body...)
}

// ============================================================
// LSP Object TLV Helpers
// ============================================================

// ExtractSymbolicName returns the symbolic path name from an LSP object's TLVs.
func ExtractSymbolicName(lsp *LSPObject) string {
	for _, tlv := range lsp.TLVs {
		if tlv.Type == TLVSymbolicPathName {
			return string(tlv.Value)
		}
	}
	return ""
}

// ExtractBSID returns the Binding SID IPv6 address from an LSP object's TLVs.
func ExtractBSID(lsp *LSPObject) (netip.Addr, bool) {
	for _, tlv := range lsp.TLVs {
		if tlv.Type == TLVBSIDv6 && len(tlv.Value) >= 16 {
			addr, ok := netip.AddrFromSlice(tlv.Value[:16])
			return addr, ok
		}
	}
	return netip.Addr{}, false
}

// ExtractIPv4LSPIdentifiers extracts the LSP identifiers TLV (RFC 8231 §7.3.1).
func ExtractIPv4LSPIdentifiers(lsp *LSPObject) (senderAddr netip.Addr, lspID uint16, tunnelID uint16) {
	for _, tlv := range lsp.TLVs {
		if tlv.Type == TLVIPv4LSPIdentifiers && len(tlv.Value) >= 12 {
			addr, _ := netip.AddrFromSlice(tlv.Value[0:4])
			senderAddr = addr.Unmap()
			lspID = binary.BigEndian.Uint16(tlv.Value[8:10])
			tunnelID = binary.BigEndian.Uint16(tlv.Value[10:12])
			return
		}
	}
	return
}

// ============================================================
// Encoder helpers (private)
// ============================================================

// encodeObject encodes an object with P-bit set, type=1.
func encodeObject(class ObjectClass, objType uint8, p bool, body []byte) []byte {
	return encodeObjectType(class, objType, p, body)
}

func encodeObjectType(class ObjectClass, objType uint8, p bool, body []byte) []byte {
	hdr := CommonObjectHeader{
		ObjectClass: class,
		ObjectType:  objType,
		Processed:   p,
		Length:      uint16(4 + len(body)),
	}
	return append(hdr.Encode(), body...)
}

// float32frombits reinterprets a uint32 as an IEEE 754 float32.
func float32frombits(b uint32) float32 {
	// Use math/bits-free conversion via unsafe trick avoided — use manual decode.
	// Since we can't import math, we implement a safe version:
	sign := (b >> 31) != 0
	exp := int((b>>23)&0xFF) - 127
	mant := b & 0x7FFFFF

	if b == 0 {
		return 0
	}
	// For normal values, value = (-1)^sign * 2^exp * (1 + mant/2^23)
	f := float32(1) + float32(mant)/float32(1<<23)
	for exp > 0 {
		f *= 2
		exp--
	}
	for exp < 0 {
		f /= 2
		exp++
	}
	if sign {
		f = -f
	}
	return f
}
