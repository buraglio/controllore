// Package pcep implements the PCEP session manager and wire codec.
// Handles TCP connections from PCCs, manages session state machines,
// and dispatches messages to/from the PCE engine.
package pcep

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// SessionState describes the state of a PCEP session.
type SessionState int

const (
	SessionIdle       SessionState = iota
	SessionTCPPending              // TCP connected, waiting for Open
	SessionOpening                 // Open sent, waiting for peer Open & Keepalive
	SessionUp                      // Session established
	SessionGoingDown               // Teardown in progress
)

func (s SessionState) String() string {
	switch s {
	case SessionIdle:
		return "IDLE"
	case SessionTCPPending:
		return "TCP-PENDING"
	case SessionOpening:
		return "OPENING"
	case SessionUp:
		return "UP"
	case SessionGoingDown:
		return "GOING-DOWN"
	default:
		return "UNKNOWN"
	}
}

// SessionCapabilities are the negotiated capabilities for a PCEP session.
type SessionCapabilities struct {
	Stateful        bool
	LSPInstantiate  bool // RFC 8281 PCE-Initiated
	SRMPLSCapable   bool
	SRv6Capable     bool
	SRv6USIDCapable bool
	MaxSIDDepth     uint8 // SR-MPLS MSD
	SRv6MSD         uint8 // SRv6 MSD
}

// Session represents a single PCEP session with a PCC.
type Session struct {
	ID           uuid.UUID
	PeerAddr     string // PCC address (host:port)
	State        SessionState
	Capabilities SessionCapabilities
	// PSID is negotiated PCEP Session ID
	LocalPSID  uint8
	RemotePSID uint8
	// Negotiated timers
	Keepalive uint8
	DeadTimer uint8
	// Counters
	MsgsRx uint64
	MsgsTx uint64
	// when the session came up
	EstablishedAt time.Time
	// LSPs reported by this PCC
	mu   sync.RWMutex
	lsps map[uint32]string // PLSP-ID → LSP UUID

	conn   net.Conn
	sendCh chan []byte
	recvCh chan Message
}

// Message is a decoded PCEP message.
type Message struct {
	Header  CommonHeader
	Body    []byte
	Session *Session
}

// Server is the PCEP server, listening for PCC connections.
type Server struct {
	cfg      ServerCfg
	mu       sync.RWMutex
	sessions map[uuid.UUID]*Session

	// OnMessage is called for every received PCEP message.
	OnMessage func(msg Message)
	// OnSessionUp is called when a session transitions to UP.
	OnSessionUp func(s *Session)
	// OnSessionDown is called when a session drops.
	OnSessionDown func(s *Session)
}

// ServerCfg configures the PCEP server.
type ServerCfg struct {
	ListenAddr string
	Port       int
	TLS        bool
	TLSCert    string
	TLSKey     string
	Keepalive  uint8
	DeadTimer  uint8
}

// NewServer creates a new PCEP server.
func NewServer(cfg ServerCfg) *Server {
	return &Server{
		cfg:      cfg,
		sessions: make(map[uuid.UUID]*Session),
	}
}

// AllSessions returns a snapshot of all active sessions.
func (s *Server) AllSessions() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		result = append(result, sess)
	}
	return result
}

// Serve starts the PCEP TCP listener. Blocks until ctx is cancelled.
func (srv *Server) Serve(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", srv.cfg.ListenAddr, srv.cfg.Port)
	var listener net.Listener
	var err error

	if srv.cfg.TLS {
		cert, err := tls.LoadX509KeyPair(srv.cfg.TLSCert, srv.cfg.TLSKey)
		if err != nil {
			return fmt.Errorf("loading TLS certs: %w", err)
		}
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
		listener, err = tls.Listen("tcp", addr, tlsCfg)
	} else {
		listener, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("pcep listen on %s: %w", addr, err)
	}

	log.Info().Str("addr", addr).Bool("tls", srv.cfg.TLS).Msg("PCEP server listening")

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Error().Err(err).Msg("accepting PCEP connection")
				continue
			}
		}
		go srv.handleConn(ctx, conn)
	}
}

// handleConn manages a single PCC TCP connection through its session lifecycle.
func (srv *Server) handleConn(ctx context.Context, conn net.Conn) {
	sess := &Session{
		ID:       uuid.New(),
		PeerAddr: conn.RemoteAddr().String(),
		State:    SessionTCPPending,
		lsps:     make(map[uint32]string),
		conn:     conn,
		sendCh:   make(chan []byte, 64),
		recvCh:   make(chan Message, 64),
	}

	log.Info().Str("peer", sess.PeerAddr).Str("session_id", sess.ID.String()).
		Msg("New PCEP TCP connection")

	srv.mu.Lock()
	srv.sessions[sess.ID] = sess
	srv.mu.Unlock()

	defer func() {
		conn.Close()
		srv.mu.Lock()
		delete(srv.sessions, sess.ID)
		srv.mu.Unlock()
		if srv.OnSessionDown != nil {
			srv.OnSessionDown(sess)
		}
		log.Info().Str("peer", sess.PeerAddr).Msg("PCEP session removed")
	}()

	// Run sender goroutine
	go func() {
		for {
			select {
			case data, ok := <-sess.sendCh:
				if !ok {
					return
				}
				if _, err := conn.Write(data); err != nil {
					log.Error().Err(err).Str("peer", sess.PeerAddr).Msg("PCEP send error")
					return
				}
				sess.MsgsTx++
			case <-ctx.Done():
				return
			}
		}
	}()

	// Run receiver loop
	buf := make([]byte, 65536)
	for {
		// Read common header (4 bytes)
		conn.SetReadDeadline(time.Now().Add(time.Duration(sess.DeadTimer+30) * time.Second))
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn().Err(err).Str("peer", sess.PeerAddr).Msg("PCEP read error")
			return
		}

		hdr, err := DecodeCommonHeader(buf[:4])
		if err != nil {
			log.Error().Err(err).Str("peer", sess.PeerAddr).Msg("invalid PCEP header")
			return
		}

		bodyLen := int(hdr.Length) - 4
		if bodyLen < 0 || bodyLen > cap(buf) {
			log.Error().Str("peer", sess.PeerAddr).Int("body_len", bodyLen).Msg("invalid PCEP message length")
			return
		}

		if bodyLen > 0 {
			if _, err := io.ReadFull(conn, buf[:bodyLen]); err != nil {
				log.Warn().Err(err).Str("peer", sess.PeerAddr).Msg("PCEP body read error")
				return
			}
		}

		sess.MsgsRx++
		body := make([]byte, bodyLen)
		copy(body, buf[:bodyLen])

		msg := Message{Header: hdr, Body: body, Session: sess}
		srv.dispatch(ctx, msg)
	}
}

// dispatch handles state machine transitions and calls OnMessage.
func (srv *Server) dispatch(ctx context.Context, msg Message) {
	sess := msg.Session

	log.Debug().
		Str("peer", sess.PeerAddr).
		Str("type", msg.Header.MessageType.String()).
		Str("state", sess.State.String()).
		Msg("PCEP message received")

	switch msg.Header.MessageType {
	case MsgOpen:
		srv.handleOpen(ctx, msg)
	case MsgKeepalive:
		// Reset dead timer; if we're in Opening state, transition to Up
		if sess.State == SessionOpening {
			sess.State = SessionUp
			sess.EstablishedAt = time.Now()
			log.Info().Str("peer", sess.PeerAddr).Msg("PCEP session UP")
			if srv.OnSessionUp != nil {
				srv.OnSessionUp(sess)
			}
		}
	case MsgPCRpt:
		if sess.State == SessionUp && srv.OnMessage != nil {
			srv.OnMessage(msg)
		}
	case MsgPCReq:
		if sess.State == SessionUp && srv.OnMessage != nil {
			srv.OnMessage(msg)
		}
	case MsgPCErr:
		log.Warn().Str("peer", sess.PeerAddr).Msg("PCEP error received")
	case MsgClose:
		log.Info().Str("peer", sess.PeerAddr).Msg("PCEP Close received")
		sess.State = SessionGoingDown
		sess.conn.Close()
	default:
		if srv.OnMessage != nil {
			srv.OnMessage(msg)
		}
	}
}

// handleOpen processes an incoming Open message and completes the handshake.
func (srv *Server) handleOpen(ctx context.Context, msg Message) {
	sess := msg.Session

	// Parse Open object
	if len(msg.Body) < 8 {
		log.Error().Str("peer", sess.PeerAddr).Msg("Open message too short")
		return
	}
	// byte 4: (ver<<5|flags), byte 5: keepalive, byte 6: dead_timer, byte 7: PSID
	sess.RemotePSID = msg.Body[3] // PSID is byte 7 of full message, byte 3 of body (after obj header)
	keepalive := msg.Body[1]
	deadTimer := msg.Body[2]

	// Negotiate timers: take the maximum (more conservative)
	if srv.cfg.Keepalive > keepalive {
		sess.Keepalive = srv.cfg.Keepalive
	} else {
		sess.Keepalive = keepalive
	}
	if srv.cfg.DeadTimer > deadTimer {
		sess.DeadTimer = srv.cfg.DeadTimer
	} else {
		sess.DeadTimer = deadTimer
	}

	// Parse TLVs for capabilities
	if len(msg.Body) > 8 {
		tlvs, _ := DecodeTLVs(msg.Body[4:])
		for _, tlv := range tlvs {
			switch tlv.Type {
			case TLVStatefulPCECapability:
				if len(tlv.Value) >= 4 {
					flags := uint32(tlv.Value[0])<<24 | uint32(tlv.Value[1])<<16 |
						uint32(tlv.Value[2])<<8 | uint32(tlv.Value[3])
					sess.Capabilities.Stateful = true
					sess.Capabilities.LSPInstantiate = flags&0x04 != 0 // I-bit
				}
			case TLVSRv6PCECapability:
				sess.Capabilities.SRv6Capable = true
				if len(tlv.Value) >= 4 {
					sess.Capabilities.SRv6MSD = tlv.Value[2]
					// Check uSID support flag
					sess.Capabilities.SRv6USIDCapable = tlv.Value[3]&0x01 != 0
				}
			case TLVSRPCECapability:
				sess.Capabilities.SRMPLSCapable = true
				if len(tlv.Value) >= 4 {
					sess.Capabilities.MaxSIDDepth = tlv.Value[3]
				}
			}
		}
	}

	log.Info().
		Str("peer", sess.PeerAddr).
		Bool("stateful", sess.Capabilities.Stateful).
		Bool("srv6", sess.Capabilities.SRv6Capable).
		Bool("usid", sess.Capabilities.SRv6USIDCapable).
		Uint8("srv6_msd", sess.Capabilities.SRv6MSD).
		Msg("PCEP capabilities negotiated")

	// Send our Open in response
	sess.State = SessionOpening
	srv.sendOpen(sess)
	// Send Keepalive to complete handshake
	srv.sendKeepalive(sess)
}

// sendOpen sends an Open message to the PCC.
func (srv *Server) sendOpen(sess *Session) {
	// Build Stateful PCE Capability TLV (RFC 8231)
	// Flags: U=0x01 (update), S=0x02 (state-sync), I=0x04 (instantiate)
	statefulFlags := []byte{0, 0, 0, 0x07}
	statefulTLV := TLV{Type: TLVStatefulPCECapability, Value: statefulFlags}

	// Build SRv6 PCE Capability TLV
	srv6TLV := TLV{Type: TLVSRv6PCECapability, Value: []byte{
		0, 0, // Reserved
		20,   // SRv6 MSD = 20
		0x01, // flags: N=uSID support
	}}

	tlvBytes := append(statefulTLV.Encode(), srv6TLV.Encode()...)

	// Open object body: ver/flags byte, keepalive, dead_timer, PSID, TLVs
	objectBody := []byte{
		0x20, // ver=1, flags=0
		sess.Keepalive,
		sess.DeadTimer,
		1, // PSID = 1 (local)
	}
	objectBody = append(objectBody, tlvBytes...)

	// Object header: class=1 (OPEN), type=1
	objHdr := CommonObjectHeader{
		ObjectClass: ObjOpen,
		ObjectType:  1,
		Processed:   true,
		Length:      uint16(4 + len(objectBody)),
	}

	msgBody := append(objHdr.Encode(), objectBody...)
	msgHdr := CommonHeader{
		Version:     1,
		MessageType: MsgOpen,
		Length:      uint16(4 + len(msgBody)),
	}
	wire := append(msgHdr.Encode(), msgBody...)
	sess.sendCh <- wire
}

// sendKeepalive sends a PCEP Keepalive message.
func (srv *Server) sendKeepalive(sess *Session) {
	hdr := CommonHeader{Version: 1, MessageType: MsgKeepalive, Length: 4}
	sess.sendCh <- hdr.Encode()
}

// SendPCInitiate sends a PCInitiate message to create or delete an LSP.
func (srv *Server) SendPCInitiate(sessID uuid.UUID, data []byte) error {
	srv.mu.RLock()
	sess, ok := srv.sessions[sessID]
	srv.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessID)
	}
	if sess.State != SessionUp {
		return fmt.Errorf("session %s not UP (state: %s)", sessID, sess.State)
	}
	sess.sendCh <- data
	return nil
}

// SendPCUpdate sends a PCUpd message to update an LSP's attributes.
func (srv *Server) SendPCUpdate(sessID uuid.UUID, data []byte) error {
	return srv.SendPCInitiate(sessID, data) // same mechanism
}
