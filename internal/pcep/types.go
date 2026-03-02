// Package pcep provides PCEP protocol types, codec, and session manager.
// Implements RFC 5440 (PCEP), RFC 8231 (Stateful), RFC 8281 (PCInitiate),
// RFC 8664 (SR-TE), and draft-ietf-pce-segment-routing-ipv6 (SRv6 extensions).
package pcep

import (
	"encoding/binary"
	"fmt"
)

// ============================================================
// Message Types (RFC 5440 §6.1)
// ============================================================

// MessageType identifies the PCEP message type.
type MessageType uint8

const (
	MsgOpen       MessageType = 1
	MsgKeepalive  MessageType = 2
	MsgPCReq      MessageType = 3
	MsgPCRep      MessageType = 4
	MsgPCNtf      MessageType = 5
	MsgPCErr      MessageType = 6
	MsgClose      MessageType = 7
	MsgPCMonReq   MessageType = 8
	MsgPCMonRep   MessageType = 9
	MsgPCRpt      MessageType = 10 // RFC 8231 — PCC → PCE state report
	MsgPCUpd      MessageType = 11 // RFC 8231 — PCE → PCC LSP update
	MsgPCInitiate MessageType = 12 // RFC 8281 — PCE → PCC LSP creation/deletion
)

func (m MessageType) String() string {
	names := map[MessageType]string{
		MsgOpen: "Open", MsgKeepalive: "Keepalive",
		MsgPCReq: "PCReq", MsgPCRep: "PCRep", MsgPCNtf: "PCNtf",
		MsgPCErr: "PCErr", MsgClose: "Close",
		MsgPCRpt: "PCRpt", MsgPCUpd: "PCUpd", MsgPCInitiate: "PCInitiate",
	}
	if n, ok := names[m]; ok {
		return n
	}
	return fmt.Sprintf("Unknown(%d)", uint8(m))
}

// ============================================================
// Object Classes (RFC 5440 §9)
// ============================================================

type ObjectClass uint8

const (
	ObjOpen          ObjectClass = 1
	ObjRP            ObjectClass = 2
	ObjNoPath        ObjectClass = 3
	ObjEndPoints     ObjectClass = 4
	ObjBandwidth     ObjectClass = 5
	ObjMetric        ObjectClass = 6
	ObjERO           ObjectClass = 7
	ObjRRO           ObjectClass = 8
	ObjLSPA          ObjectClass = 9
	ObjIRO           ObjectClass = 10
	ObjReportedRoute ObjectClass = 11
	ObjSVEC          ObjectClass = 12
	ObjNotify        ObjectClass = 13
	ObjPCEPError     ObjectClass = 14
	ObjLoadBalancing ObjectClass = 15
	ObjClose         ObjectClass = 16
	ObjPathKey       ObjectClass = 17
	ObjXRO           ObjectClass = 19
	ObjMonitoring    ObjectClass = 20
	ObjPccReq        ObjectClass = 21
	ObjOF            ObjectClass = 21
	ObjClassSRPolicy ObjectClass = 23 // SR Policy Association (RFC 8697)
	// Stateful extensions (RFC 8231)
	ObjLSP    ObjectClass = 32 // LSP object
	ObjSRP    ObjectClass = 33 // Stateful Request Parameters
	ObjVendor ObjectClass = 34 // Vendor-specific
	ObjAssoc  ObjectClass = 40 // Association object (RFC 8697)
)

// ============================================================
// TLV Types
// ============================================================

type TLVType uint16

const (
	TLVNoPathVector          TLVType = 1
	TLVOverloadDuration      TLVType = 2
	TLVReqMissing            TLVType = 3
	TLVPathSetupType         TLVType = 28 // RFC 8408
	TLVSRPCECapability       TLVType = 26 // SR PCE capability (RFC 8664)
	TLVStatefulPCECapability TLVType = 16 // RFC 8231
	TLVSRv6PCECapability     TLVType = 27 // SRv6 (draft-ietf-pce-segment-routing-ipv6)
	TLVSymbolicPathName      TLVType = 17 // RFC 8231
	TLVIPv4LSPIdentifiers    TLVType = 18 // RFC 8231
	TLVIPv6LSPIdentifiers    TLVType = 19 // RFC 8231
	TLVLSPErrorCode          TLVType = 20 // RFC 8231
	TLVRSVPErrorSpec         TLVType = 21 // RFC 8231
	TLVLSPDBVersion          TLVType = 23 // RFC 8231
	TLVSpeakerEntityID       TLVType = 24 // RFC 8232
	TLVSRv6SIDStructure      TLVType = 35 // draft-ietf-pce-segment-routing-ipv6
	TLVBSID                  TLVType = 58 // Binding SID (SR-MPLS label)
	TLVBSIDv6                TLVType = 59 // Binding SID (SRv6 IPv6 address)
)

// ============================================================
// ERO Subobjects
// ============================================================

// EROSubobjectType identifies the type of ERO subobject.
type EROSubobjectType uint8

const (
	SubobjIPv4Prefix EROSubobjectType = 1
	SubobjIPv6Prefix EROSubobjectType = 2
	SubobjASNumber   EROSubobjectType = 32
	SubobjSRv6       EROSubobjectType = 40 // draft-ietf-pce-segment-routing-ipv6
	SubobjSR         EROSubobjectType = 36 // RFC 8664 (SR-MPLS)
)

// NAIType for SR/SRv6 ERO subobjects.
type NAIType uint8

const (
	NAIAbsent          NAIType = 0
	NAIIPv4NodeID      NAIType = 1
	NAIIPv6NodeID      NAIType = 2
	NAIIPv4AdjacencyID NAIType = 3
	NAIIPv6AdjacencyID NAIType = 4
	NAIUnnumberedAdjID NAIType = 5
	NAIIPv6LocalRemote NAIType = 6 // RFC 8664
)

// ============================================================
// Wire Format Structures
// ============================================================

// CommonHeader is the 4-byte PCEP common header (RFC 5440 §6.1).
type CommonHeader struct {
	Version     uint8 // Must be 1
	Flags       uint8 // Reserved, MUST be 0
	MessageType MessageType
	Length      uint16 // Total length including header
}

// Encode serializes the common header to wire format.
func (h CommonHeader) Encode() []byte {
	b := make([]byte, 4)
	b[0] = (h.Version << 5) | h.Flags
	b[1] = uint8(h.MessageType)
	binary.BigEndian.PutUint16(b[2:], h.Length)
	return b
}

// DecodeCommonHeader parses a 4-byte PCEP header.
func DecodeCommonHeader(b []byte) (CommonHeader, error) {
	if len(b) < 4 {
		return CommonHeader{}, fmt.Errorf("header too short: %d bytes", len(b))
	}
	return CommonHeader{
		Version:     b[0] >> 5,
		Flags:       b[0] & 0x1F,
		MessageType: MessageType(b[1]),
		Length:      binary.BigEndian.Uint16(b[2:4]),
	}, nil
}

// CommonObjectHeader is the 4-byte object header (RFC 5440 §7.2).
type CommonObjectHeader struct {
	ObjectClass ObjectClass
	ObjectType  uint8
	Reserved    bool
	Processed   bool // P-bit: must be processed
	Ignored     bool // I-bit: ignored if not understood
	Length      uint16
}

// Encode serializes the object header.
func (h CommonObjectHeader) Encode() []byte {
	b := make([]byte, 4)
	b[0] = uint8(h.ObjectClass)
	b[1] = (h.ObjectType << 4)
	if h.Reserved {
		b[1] |= 0x04
	}
	if h.Processed {
		b[1] |= 0x02
	}
	if h.Ignored {
		b[1] |= 0x01
	}
	binary.BigEndian.PutUint16(b[2:], h.Length)
	return b
}

// DecodeObjectHeader parses a 4-byte PCEP object header.
func DecodeObjectHeader(b []byte) (CommonObjectHeader, error) {
	if len(b) < 4 {
		return CommonObjectHeader{}, fmt.Errorf("object header too short")
	}
	return CommonObjectHeader{
		ObjectClass: ObjectClass(b[0]),
		ObjectType:  b[1] >> 4,
		Reserved:    b[1]&0x04 != 0,
		Processed:   b[1]&0x02 != 0,
		Ignored:     b[1]&0x01 != 0,
		Length:      binary.BigEndian.Uint16(b[2:4]),
	}, nil
}

// TLV is a generic PCEP TLV (Type-Length-Value) structure.
type TLV struct {
	Type  TLVType
	Value []byte
}

// Encode serializes a TLV, padding to 4-byte alignment.
func (t TLV) Encode() []byte {
	valLen := len(t.Value)
	padded := (valLen + 3) &^ 3
	b := make([]byte, 4+padded)
	binary.BigEndian.PutUint16(b[0:2], uint16(t.Type))
	binary.BigEndian.PutUint16(b[2:4], uint16(valLen))
	copy(b[4:], t.Value)
	return b
}

// DecodeTLVs parses a byte slice into a list of TLVs.
func DecodeTLVs(data []byte) ([]TLV, error) {
	var tlvs []TLV
	for len(data) >= 4 {
		t := TLVType(binary.BigEndian.Uint16(data[0:2]))
		l := binary.BigEndian.Uint16(data[2:4])
		padded := (int(l) + 3) &^ 3
		if len(data) < 4+padded {
			return nil, fmt.Errorf("TLV truncated: need %d bytes, have %d", 4+padded, len(data))
		}
		tlvs = append(tlvs, TLV{Type: t, Value: data[4 : 4+l]})
		data = data[4+padded:]
	}
	return tlvs, nil
}

// ============================================================
// Open Object (RFC 5440 §7.3)
// ============================================================

// OpenObject contains PCEP session parameters.
type OpenObject struct {
	Version   uint8
	Flags     uint8
	Keepalive uint8
	DeadTimer uint8
	PSID      uint8 // PCEP Session ID
	TLVs      []TLV
}

// ============================================================
// LSP Object (RFC 8231 §7.3)
// ============================================================

// LSPOperationalStatus represents the O-field in the LSP object.
type LSPOperationalStatus uint8

const (
	LSPOperDown      LSPOperationalStatus = 0
	LSPOperUp        LSPOperationalStatus = 1
	LSPOperActive    LSPOperationalStatus = 2
	LSPOperGoingDown LSPOperationalStatus = 3
	LSPOperGoingUp   LSPOperationalStatus = 4
)

// LSPObject represents the LSP object (RFC 8231).
type LSPObject struct {
	PLSPID   uint32 // 20-bit PCE LSP identifier
	Delegate bool   // D-bit: PCC delegates control to PCE
	Sync     bool   // S-bit: state synchronization in progress
	Remove   bool   // R-bit: PCE requests LSP removal
	Admin    bool   // A-bit: administratively controlled
	Create   bool   // C-bit: PCE-created LSP (RFC 8281)
	OpStatus LSPOperationalStatus
	TLVs     []TLV
}

// SRPObject is the Stateful Request Parameters object (RFC 8231).
type SRPObject struct {
	SRPID  uint32 // PCE request identifier
	Remove bool   // R-bit for PCInitiate
	TLVs   []TLV
}

// ============================================================
// SRv6 ERO Subobject (draft-ietf-pce-segment-routing-ipv6)
// ============================================================

// SRv6EROSubobject is an ERO subobject for SRv6 segment list entries.
// Encodes a 128-bit SRv6 SID plus optional NAI and behavior.
type SRv6EROSubobject struct {
	// NAIType identifies what addressing follows the SID.
	NAIType NAIType
	// F-bit: SID is absent (NAI is used for forwarding).
	SIDAbsent bool
	// S-bit: NAI is absent.
	NAIAbsent bool
	// C-bit: PCE computed the SID.
	Computed bool
	// M-bit: SID is a uSID (micro-SID).
	IsUSID bool
	// SID is the 128-bit SRv6 Segment ID.
	SID [16]byte
	// EndpointBehavior is the intended behavior at the terminating node.
	EndpointBehavior uint16
	// SIDStructure encodes LB/LN/Fun/Arg lengths if present.
	SIDStructure *SIDStructureSubTLV
	// NAI data (depends on NAIType)
	NAI []byte
}

// SIDStructureSubTLV encodes the SID structure TLV within an SRv6 ERO subobject.
type SIDStructureSubTLV struct {
	LBLen  uint8
	LNLen  uint8
	FunLen uint8
	ArgLen uint8
}

// Encode serializes the SRv6 ERO subobject to wire format.
func (s *SRv6EROSubobject) Encode(loose bool) []byte {
	// Base: 2 (Type+Len) + 2 (flags) + 16 (SID) + 2 (behavior) = 22 bytes + NAI + subTLVs
	flags := uint16(s.NAIType) << 12
	if s.SIDAbsent {
		flags |= 0x0800
	}
	if s.NAIAbsent {
		flags |= 0x0400
	}
	if s.Computed {
		flags |= 0x0200
	}
	if s.IsUSID {
		flags |= 0x0100
	}

	var buf []byte
	// Type byte (L-bit for loose hop)
	typeByte := uint8(SubobjSRv6)
	if loose {
		typeByte |= 0x80
	}
	buf = append(buf, typeByte)
	// Placeholder for length
	buf = append(buf, 0)
	// Flags (2 bytes)
	buf = append(buf, byte(flags>>8), byte(flags))
	// Reserved (1 byte) + Endpoint Behavior (2 bytes)
	buf = append(buf, 0, byte(s.EndpointBehavior>>8), byte(s.EndpointBehavior))
	// SID (16 bytes)
	buf = append(buf, s.SID[:]...)
	// NAI
	buf = append(buf, s.NAI...)
	// SID Structure sub-TLV if present
	if s.SIDStructure != nil {
		buf = append(buf,
			s.SIDStructure.LBLen,
			s.SIDStructure.LNLen,
			s.SIDStructure.FunLen,
			s.SIDStructure.ArgLen,
		)
	}
	// Fill in length
	buf[1] = uint8(len(buf))
	return buf
}

// ============================================================
// Path Setup Type values (RFC 8408)
// ============================================================

const (
	PathSetupRSVP   = 0
	PathSetupSRMPLS = 1
	PathSetupSRv6   = 3 // draft-ietf-pce-segment-routing-ipv6
)

// ============================================================
// Error Codes
// ============================================================

// ErrorType groups related PCEP error codes (RFC 5440 §9.12).
type ErrorType uint8

const (
	ErrSessionFailure         ErrorType = 1
	ErrCapabilityNotSupported ErrorType = 2
	ErrUnknownObject          ErrorType = 3
	ErrNotSupportedObject     ErrorType = 4
	ErrPolicyViolation        ErrorType = 5
	ErrMandatoryObjectMissing ErrorType = 6
	ErrSyncError              ErrorType = 20
	ErrInvalidOperation       ErrorType = 19
	ErrAssocError             ErrorType = 26
)
