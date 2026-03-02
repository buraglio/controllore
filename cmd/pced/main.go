// Package main is the entry point for the Controllore PCE daemon (pced).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/buraglio/controllore/internal/api"
	"github.com/buraglio/controllore/internal/config"
	"github.com/buraglio/controllore/internal/cspf"
	"github.com/buraglio/controllore/internal/events"
	"github.com/buraglio/controllore/internal/lsp"
	"github.com/buraglio/controllore/internal/pcep"
	"github.com/buraglio/controllore/internal/ted"
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
  - BGP-LS topology discovery and TED maintenance
  - Stateful PCEP server (RFC 8231, RFC 8281, RFC 8664, SRv6 extensions)
  - CSPF path computation engine with SRv6 segment list construction
  - REST API + WebSocket server for CLI and Web UI clients`,
		RunE: runDaemon,
	}

	root.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "Config file (default: ./controllore.yaml)")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemon(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Configure structured logging
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

	// Build the system context (cancelled on signal)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── Construct subsystems ──────────────────────────────────────────────────

	// 1. Traffic Engineering Database
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

	// 5. PCEP Server
	pcepSrv := pcep.NewServer(pcep.ServerCfg{
		ListenAddr: cfg.PCEP.ListenAddr,
		Port:       cfg.PCEP.Port,
		TLS:        cfg.PCEP.TLS,
		TLSCert:    cfg.PCEP.TLSCert,
		TLSKey:     cfg.PCEP.TLSKey,
		Keepalive:  cfg.PCEP.Keepalive,
		DeadTimer:  cfg.PCEP.DeadTimer,
	})
	// Wire PCEP session events to the event bus
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
	log.Info().Str("addr", fmt.Sprintf("%s:%d", cfg.PCEP.ListenAddr, cfg.PCEP.Port)).
		Msg("PCEP server configured")

	// 6. API Server
	apiSrv := api.New(tedStore, lspMgr, pcepSrv, cspfEngine, bus)

	// ── Start services ────────────────────────────────────────────────────────
	errCh := make(chan error, 4)

	// Start PCEP server in background
	go func() {
		if err := pcepSrv.Serve(ctx); err != nil {
			errCh <- fmt.Errorf("pcep server: %w", err)
		}
	}()

	// Start API server in background
	go func() {
		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := apiSrv.Listen(addr); err != nil {
			errCh <- fmt.Errorf("api server: %w", err)
		}
	}()

	log.Info().Msg("All services started. Controllore is ready.")

	// Wait for signal or error
	select {
	case <-ctx.Done():
		log.Info().Msg("Shutdown signal received, stopping...")
	case err := <-errCh:
		log.Error().Err(err).Msg("Service error, shutting down")
		return err
	}

	return nil
}
