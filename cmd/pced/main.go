// Package main is the entry point for the Controllore PCE daemon (pced).
package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"syscall"

	"github.com/buraglio/controllore/internal/api"
	"github.com/buraglio/controllore/internal/bgpls"
	"github.com/buraglio/controllore/internal/config"
	"github.com/buraglio/controllore/internal/cspf"
	"github.com/buraglio/controllore/internal/db"
	"github.com/buraglio/controllore/internal/events"
	"github.com/buraglio/controllore/internal/lsp"
	"github.com/buraglio/controllore/internal/pcep"
	"github.com/buraglio/controllore/internal/ted"
	"github.com/buraglio/controllore/pkg/srv6"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var cfgFile string

func main() {
	root := &cobra.Command{
		Use:   "pced",
		Short: "Controllore PCE Daemon — SRv6/SR-MPLS Stateful Path Computation Element",
		Long: `pced is the core Controllore PCE daemon. It provides:
  - BGP-LS topology discovery and TED maintenance (RFC 9514, RFC 9085)
  - Stateful PCEP server (RFC 8231, RFC 8281, RFC 8664, SRv6 extensions)
  - CSPF path computation engine with SRv6 uSID segment list construction
  - REST API + WebSocket server for CLI and Web UI clients
  - PostgreSQL persistence for TED and LSP state`,
		RunE: runDaemon,
	}

	root.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "Config file (default: ./controllore.yaml)")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemon(cmd *cobra.Command, args []string) error {
	// ── Configuration ──────────────────────────────────────────────────────
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// ── Logging ────────────────────────────────────────────────────────────
	level, err := zerolog.ParseLevel(cfg.Log.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	if cfg.Log.Format == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "15:04:05"})
	}

	log.Info().
		Str("log_level", level.String()).
		Str("api_addr", fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)).
		Str("pcep_addr", fmt.Sprintf("%s:%d", cfg.PCEP.ListenAddr, cfg.PCEP.Port)).
		Msg("Controllore PCE daemon starting")

	// ── Context ────────────────────────────────────────────────────────────
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── Subsystems ─────────────────────────────────────────────────────────

	// 1. Traffic Engineering Database (in-memory)
	tedStore := ted.New()
	log.Info().Msg("TED initialized")

	// 2. Event Bus
	bus := events.New()
	log.Info().Msg("Event bus initialized")

	// 3. LSP Manager
	lspMgr := lsp.NewManager()
	log.Info().Msg("LSP manager initialized")

	// 4. CSPF Engine
	cspfEngine := cspf.New(tedStore)
	log.Info().Msg("CSPF engine initialized")

	// 5. PostgreSQL persistence (optional — skip if no DB configured)
	var dbStore *db.Store
	if cfg.Database.Host != "" && cfg.Database.Name != "" {
		dbStore, err = db.New(ctx, cfg.Database.DSN())
		if err != nil {
			log.Warn().Err(err).Msg("Database unavailable — running without persistence")
			dbStore = nil
		} else {
			if err := dbStore.Migrate(ctx); err != nil {
				log.Warn().Err(err).Msg("DB migration failed — state will not be persisted")
				dbStore = nil
			} else {
				// Restore persisted TED + LSP state
				if err := db.RestoreTED(ctx, dbStore, tedStore); err != nil {
					log.Warn().Err(err).Msg("TED restore failed")
				}
				if err := db.RestoreLSPs(ctx, dbStore, lspMgr); err != nil {
					log.Warn().Err(err).Msg("LSP restore failed")
				}
			}
		}
	}

	// 6. PCEP Server
	pcepSrv := pcep.NewServer(pcep.ServerCfg{
		ListenAddr: cfg.PCEP.ListenAddr,
		Port:       cfg.PCEP.Port,
		TLS:        cfg.PCEP.TLS,
		TLSCert:    cfg.PCEP.TLSCert,
		TLSKey:     cfg.PCEP.TLSKey,
		Keepalive:  cfg.PCEP.Keepalive,
		DeadTimer:  cfg.PCEP.DeadTimer,
	})

	// ── Wire PCEP session events to bus ───────────────────────────────────
	pcepSrv.OnSessionUp = func(s *pcep.Session) {
		bus.PublishSession(events.EvSessionUp, s.ID.String(), map[string]interface{}{
			"peer": s.PeerAddr,
			"srv6": s.Capabilities.SRv6Capable,
			"usid": s.Capabilities.SRv6USIDCapable,
			"msd":  s.Capabilities.SRv6MSD,
		})
	}
	pcepSrv.OnSessionDown = func(s *pcep.Session) {
		bus.PublishSession(events.EvSessionDown, s.ID.String(), map[string]interface{}{
			"peer": s.PeerAddr,
		})
	}

	// ── Wire PCEP message handler ──────────────────────────────────────────
	// This is the central dispatcher for all PCRpt/PCUpd messages received
	// from PCC sessions.
	pcepSrv.OnMessage = makePCEPHandler(lspMgr, cspfEngine, bus, dbStore)

	log.Info().Str("addr", fmt.Sprintf("%s:%d", cfg.PCEP.ListenAddr, cfg.PCEP.Port)).
		Msg("PCEP server configured")

	// 7. API Server
	apiSrv := api.New(tedStore, lspMgr, pcepSrv, cspfEngine, bus)

	// 8. BGP-LS Collector (only if peers are configured)
	var bgplsCollector *bgpls.Collector
	if len(cfg.BGPLS.Peers) > 0 {
		bgplsCollector = bgpls.New(cfg.BGPLS, tedStore, bus)

		// When BGP-LS updates the TED, persist to DB
		if dbStore != nil {
			tedStore.SetOnUpsertNode(func(n *ted.Node) {
				if err := dbStore.SaveNode(context.Background(), n); err != nil {
					log.Error().Err(err).Str("router_id", n.RouterID).Msg("DB: failed to save node")
				}
			})
			tedStore.SetOnUpsertLink(func(l *ted.Link) {
				if err := dbStore.SaveLink(context.Background(), l); err != nil {
					log.Error().Err(err).Str("link", l.LocalNodeID+"|"+l.RemoteNodeID).Msg("DB: failed to save link")
				}
			})
			tedStore.SetOnDeleteNode(func(routerID string) {
				if err := dbStore.DeleteNode(context.Background(), routerID); err != nil {
					log.Error().Err(err).Str("router_id", routerID).Msg("DB: failed to delete node")
				}
			})
		}
	}

	// ── Start services ────────────────────────────────────────────────────
	errCh := make(chan error, 4)

	// PCEP server
	go func() {
		if err := pcepSrv.Serve(ctx); err != nil {
			errCh <- fmt.Errorf("pcep server: %w", err)
		}
	}()

	// API server
	go func() {
		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := apiSrv.Listen(addr); err != nil {
			errCh <- fmt.Errorf("api server: %w", err)
		}
	}()

	// BGP-LS collector (if configured)
	if bgplsCollector != nil {
		go bgplsCollector.RunWithRetry(ctx)
		log.Info().
			Int("peers", len(cfg.BGPLS.Peers)).
			Str("router_id", cfg.BGPLS.RouterID).
			Msg("BGP-LS collector started")
	} else {
		log.Info().Msg("BGP-LS: no peers configured — topology must be injected via API")
	}

	log.Info().Msg("All services started. Controllore is ready.")

	// Wait for shutdown
	select {
	case <-ctx.Done():
		log.Info().Msg("Shutdown signal received, stopping...")
	case err := <-errCh:
		log.Error().Err(err).Msg("Service error, shutting down")
		return err
	}

	if dbStore != nil {
		dbStore.Close()
	}
	return nil
}

// ── PCEP message handler factory ──────────────────────────────────────────

// makePCEPHandler returns the OnMessage callback wired to the LSP manager and event bus.
// This handles PCRpt (state reports from PCC) and produces appropriate LSP events.
func makePCEPHandler(
	lspMgr *lsp.Manager,
	cspfEngine *cspf.Engine,
	bus *events.Bus,
	dbStore *db.Store,
) func(pcep.Message) {
	return func(msg pcep.Message) {
		switch msg.Header.MessageType {
		case pcep.MsgPCRpt:
			handlePCRpt(msg, lspMgr, bus, dbStore)
		case pcep.MsgPCReq:
			handlePCReq(msg, cspfEngine, bus)
		default:
			log.Debug().
				Str("type", msg.Header.MessageType.String()).
				Msg("PCEP: unhandled message type")
		}
	}
}

// handlePCRpt processes a PCRpt message from a PCC.
// It maps the PLSP-ID to our internal LSP UUID and updates status.
func handlePCRpt(msg pcep.Message, lspMgr *lsp.Manager, bus *events.Bus, dbStore *db.Store) {
	reports, err := pcep.DecodePCRpt(msg.Body)
	if err != nil {
		log.Error().Err(err).Str("peer", msg.Session.PeerAddr).Msg("PCRpt decode error")
		return
	}

	for _, report := range reports {
		lspObj := report.LSP
		if lspObj == nil {
			continue
		}

		name := pcep.ExtractSymbolicName(lspObj)
		bsid, hasBSID := pcep.ExtractBSID(lspObj)

		// Map operational status → LSP status
		var newStatus lsp.LSPStatus
		switch lspObj.OpStatus {
		case pcep.LSPOperUp, pcep.LSPOperActive:
			newStatus = lsp.LSPStatusActive
		case pcep.LSPOperDown:
			newStatus = lsp.LSPStatusDown
		case pcep.LSPOperGoingDown:
			newStatus = lsp.LSPStatusDown
		default:
			newStatus = lsp.LSPStatusReported
		}

		// Find or create LSP by PLSP-ID + session
		peerLSPID := lspObj.PLSPID
		sessID := msg.Session.ID

		// Look for an existing LSP with this PLSP-ID in this session
		existing := findLSPByPLSPID(lspMgr, sessID, peerLSPID)
		if existing != nil {
			// Update status
			oldStatus := existing.Status
			if err := lspMgr.UpdateStatus(existing.ID, newStatus, "PCRpt"); err != nil {
				log.Error().Err(err).Msg("PCRpt: status update failed")
				continue
			}

			// Update BSID if reported
			if hasBSID {
				existing.BSID = bsid.String()
			}

			// Update segment list if ERO is present
			if len(report.ERO) > 0 {
				segs := eroToSIDList(report.ERO)
				if len(segs) > 0 {
					_ = lspMgr.UpdateSegmentList(existing.ID, segs, existing.ComputedMetric)
				}
			}

			// Persist if DB is available
			if dbStore != nil {
				if upd, err := lspMgr.Get(existing.ID); err == nil {
					_ = dbStore.SaveLSP(context.Background(), upd)
				}
			}

			bus.PublishLSP(events.EvLSPStatusChg, existing.ID.String(), map[string]interface{}{
				"old_status": string(oldStatus),
				"new_status": string(newStatus),
				"peer":       msg.Session.PeerAddr,
			})
			log.Info().
				Str("lsp_id", existing.ID.String()).
				Str("name", existing.Name).
				Str("status", string(newStatus)).
				Str("peer", msg.Session.PeerAddr).
				Msg("PCRpt: LSP status updated")
		} else if lspObj.Sync {
			// State sync: PCC is advertising a pre-existing LSP we didn't create.
			// Register it as a "reported" LSP.
			peerAddr := msg.Session.PeerAddr
			if hport := len(peerAddr) - 5; hport > 0 {
				// strip port from "1.2.3.4:4189"
				for i := len(peerAddr) - 1; i >= 0; i-- {
					if peerAddr[i] == ':' {
						peerAddr = peerAddr[:i]
						break
					}
				}
			}
			newLSP := &lsp.LSP{
				ID:        uuid.New(),
				Name:      name,
				PCC:       peerAddr,
				SRType:    lsp.SRTypeSRv6,
				Status:    lsp.LSPStatusReported,
				PCEPID:    peerLSPID,
				SessionID: sessID,
			}
			if hasBSID {
				newLSP.BSID = bsid.String()
			}
			if len(report.ERO) > 0 {
				newLSP.SegmentList = eroToSIDList(report.ERO)
			}
			if _, err := lspMgr.Create(newLSP); err == nil {
				if dbStore != nil {
					_ = dbStore.SaveLSP(context.Background(), newLSP)
				}
				bus.PublishLSP(events.EvLSPCreated, newLSP.ID.String(), map[string]interface{}{
					"name":   name,
					"peer":   msg.Session.PeerAddr,
					"source": "pcrpt-sync",
				})
				log.Info().
					Str("name", name).
					Str("peer", msg.Session.PeerAddr).
					Uint32("plsp_id", peerLSPID).
					Msg("PCRpt: state-sync LSP registered")
			}
		}
	}
}

// handlePCReq handles a PCReq message from a PCC (on-demand path request).
// In stateful PCE mode this is uncommon, but we support it for interop.
func handlePCReq(msg pcep.Message, cspfEngine *cspf.Engine, bus *events.Bus) {
	reports, err := pcep.DecodePCRpt(msg.Body) // PCReq has similar object structure
	if err != nil {
		log.Error().Err(err).Str("peer", msg.Session.PeerAddr).Msg("PCReq decode error")
		return
	}
	for _, r := range reports {
		if r.Endpoint == nil {
			continue
		}
		src := r.Endpoint.SrcIPv4.String()
		dst := r.Endpoint.DstIPv4.String()
		if !r.Endpoint.SrcIPv4.IsValid() {
			src = r.Endpoint.SrcIPv6.String()
			dst = r.Endpoint.DstIPv6.String()
		}
		log.Info().
			Str("src", src).
			Str("dst", dst).
			Str("peer", msg.Session.PeerAddr).
			Msg("PCReq: on-demand path request received")
		// Path computation happens via the API; log for now
		bus.Publish(events.Event{
			Type: "pce.path_requested",
			ID:   msg.Session.PeerAddr,
			Data: map[string]interface{}{"src": src, "dst": dst},
		})
	}
}

// ── PCEP helper utilities ──────────────────────────────────────────────────

// findLSPByPLSPID searches the LSP manager for an LSP with the given PCEP PLSP-ID
// and session UUID.
func findLSPByPLSPID(mgr *lsp.Manager, sessID uuid.UUID, plspID uint32) *lsp.LSP {
	for _, l := range mgr.All() {
		if l.PCEPID == plspID && l.SessionID == sessID {
			return l
		}
	}
	return nil
}

// eroToSIDList converts decoded ERO subobjects into an srv6.SID slice.
func eroToSIDList(ero []pcep.EROSubobject) []srv6.SID {
	var sids []srv6.SID
	for _, sub := range ero {
		if sub.Type == pcep.SubobjSRv6 && sub.SRv6Sub != nil {
			raw := sub.SRv6Sub.SID
			addr, ok := netip.AddrFromSlice(raw[:])
			if !ok {
				continue
			}
			behavior := srv6.EndpointBehavior(sub.SRv6Sub.EndpointBehavior)
			sidType := srv6.SIDTypeNode
			if behavior == srv6.BehaviorEndX {
				sidType = srv6.SIDTypeAdj
			}
			// uSID carrier: mark as uSID type
			if sub.SRv6Sub.IsUSID {
				sidType = srv6.SIDTypeUSID
			}
			sid := srv6.SID{
				Addr:     addr,
				Behavior: behavior,
				Type:     sidType,
			}
			if ss := sub.SRv6Sub.SIDStructure; ss != nil {
				sid.Structure = srv6.SIDStructure{
					LBLen:  ss.LBLen,
					LNLen:  ss.LNLen,
					FunLen: ss.FunLen,
					ArgLen: ss.ArgLen,
				}
			}
			sids = append(sids, sid)
		}
	}
	return sids
}
