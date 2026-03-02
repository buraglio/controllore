// Package db implements PostgreSQL persistence for the Controllore TED and LSP state.
// It uses pgx v5 (direct driver, no ORM) for maximum control and performance.
package db

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/buraglio/controllore/internal/lsp"
	"github.com/buraglio/controllore/internal/ted"
)

// Store is the PostgreSQL persistence layer.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a pgx connection pool and returns a Store.
func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	log.Info().Str("dsn", maskPassword(dsn)).Msg("Database connected")
	return &Store{pool: pool}, nil
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// ── Schema Migrations ──────────────────────────────────────────────────────

// Migrate applies all required DDL to the database if not already present.
// Idempotent: safe to call on every startup.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	log.Info().Msg("Database schema applied")
	return nil
}

const schema = `
-- TED: nodes
CREATE TABLE IF NOT EXISTS ted_nodes (
    router_id        TEXT PRIMARY KEY,
    hostname         TEXT NOT NULL DEFAULT '',
    asn              BIGINT NOT NULL DEFAULT 0,
    isis_area_ids    JSONB NOT NULL DEFAULT '[]',
    capabilities     JSONB NOT NULL DEFAULT '{}',
    srv6_locators    JSONB NOT NULL DEFAULT '[]',
    flex_algos       JSONB NOT NULL DEFAULT '[]',
    mgmt_ipv4        INET,
    mgmt_ipv6        INET,
    source           TEXT NOT NULL DEFAULT 'bgp-ls',
    last_seen        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- TED: links
CREATE TABLE IF NOT EXISTS ted_links (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    local_node_id    TEXT NOT NULL REFERENCES ted_nodes(router_id) ON DELETE CASCADE,
    remote_node_id   TEXT NOT NULL,
    local_ip         INET,
    remote_ip        INET,
    te_metric        BIGINT NOT NULL DEFAULT 0,
    igp_metric       BIGINT NOT NULL DEFAULT 0,
    max_bandwidth    BIGINT NOT NULL DEFAULT 0,
    reserved_bw      BIGINT NOT NULL DEFAULT 0,
    admin_group      BIGINT NOT NULL DEFAULT 0,
    srlg             JSONB NOT NULL DEFAULT '[]',
    latency_us       BIGINT NOT NULL DEFAULT 0,
    flex_algo_metrics JSONB NOT NULL DEFAULT '{}',
    last_seen        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (local_node_id, remote_node_id, local_ip)
);

-- LSPs
CREATE TABLE IF NOT EXISTS lsps (
    id               UUID PRIMARY KEY,
    name             TEXT NOT NULL DEFAULT '',
    pcc              TEXT NOT NULL DEFAULT '',
    src_router_id    TEXT NOT NULL,
    dst_router_id    TEXT NOT NULL,
    sr_type          TEXT NOT NULL DEFAULT 'srv6',
    status           TEXT NOT NULL DEFAULT 'pending',
    bsid             TEXT NOT NULL DEFAULT '',
    segment_list     JSONB NOT NULL DEFAULT '[]',
    computed_metric  BIGINT NOT NULL DEFAULT 0,
    constraints      JSONB NOT NULL DEFAULT '{}',
    pcep_id          BIGINT NOT NULL DEFAULT 0,
    srp_id           BIGINT NOT NULL DEFAULT 0,
    session_id       UUID,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- LSP history
CREATE TABLE IF NOT EXISTS lsp_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lsp_id      UUID NOT NULL,
    event       TEXT NOT NULL,
    old_status  TEXT NOT NULL DEFAULT '',
    new_status  TEXT NOT NULL DEFAULT '',
    details     TEXT NOT NULL DEFAULT '',
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_lsp_history_lsp_id ON lsp_history(lsp_id);
`

// ── TED Persistence ────────────────────────────────────────────────────────

// SaveNode upserts a node into ted_nodes.
func (s *Store) SaveNode(ctx context.Context, n *ted.Node) error {
	areaJSON, _ := json.Marshal(n.ISISAreaIDs)
	capsJSON, _ := json.Marshal(n.Capabilities)
	locsJSON, _ := json.Marshal(n.SRv6Locators)
	algosJSON, _ := json.Marshal(n.FlexAlgos)

	var mgmtV4, mgmtV6 *string
	if n.ManagementIPv4.IsValid() {
		s := n.ManagementIPv4.String()
		mgmtV4 = &s
	}
	if n.ManagementIPv6.IsValid() {
		s := n.ManagementIPv6.String()
		mgmtV6 = &s
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO ted_nodes
			(router_id, hostname, asn, isis_area_ids, capabilities, srv6_locators,
			 flex_algos, mgmt_ipv4, mgmt_ipv6, source, last_seen)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (router_id) DO UPDATE SET
			hostname      = EXCLUDED.hostname,
			asn           = EXCLUDED.asn,
			isis_area_ids = EXCLUDED.isis_area_ids,
			capabilities  = EXCLUDED.capabilities,
			srv6_locators = EXCLUDED.srv6_locators,
			flex_algos    = EXCLUDED.flex_algos,
			mgmt_ipv4     = EXCLUDED.mgmt_ipv4,
			mgmt_ipv6     = EXCLUDED.mgmt_ipv6,
			source        = EXCLUDED.source,
			last_seen     = EXCLUDED.last_seen`,
		n.RouterID, n.Hostname, n.ASN,
		areaJSON, capsJSON, locsJSON,
		algosJSON, mgmtV4, mgmtV6,
		n.Source, n.LastSeen,
	)
	return err
}

// DeleteNode removes a node from ted_nodes.
func (s *Store) DeleteNode(ctx context.Context, routerID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ted_nodes WHERE router_id = $1`, routerID)
	return err
}

// LoadNodes returns all nodes from ted_nodes, used on startup to restore TED.
func (s *Store) LoadNodes(ctx context.Context) ([]*ted.Node, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT router_id, hostname, asn, isis_area_ids, capabilities, srv6_locators,
		       flex_algos, mgmt_ipv4, mgmt_ipv6, source, last_seen
		FROM ted_nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*ted.Node
	for rows.Next() {
		var (
			n                            ted.Node
			areaJSON, capsJSON, locsJSON []byte
			algosJSON                    []byte
			mgmtV4, mgmtV6               *string
		)
		if err := rows.Scan(
			&n.RouterID, &n.Hostname, &n.ASN,
			&areaJSON, &capsJSON, &locsJSON,
			&algosJSON, &mgmtV4, &mgmtV6,
			&n.Source, &n.LastSeen,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(areaJSON, &n.ISISAreaIDs)
		_ = json.Unmarshal(capsJSON, &n.Capabilities)
		_ = json.Unmarshal(locsJSON, &n.SRv6Locators)
		_ = json.Unmarshal(algosJSON, &n.FlexAlgos)
		if mgmtV4 != nil {
			n.ManagementIPv4, _ = netip.ParseAddr(*mgmtV4)
		}
		if mgmtV6 != nil {
			n.ManagementIPv6, _ = netip.ParseAddr(*mgmtV6)
		}
		n.ID = uuid.New()
		nodes = append(nodes, &n)
	}
	return nodes, rows.Err()
}

// SaveLink upserts a link into ted_links.
func (s *Store) SaveLink(ctx context.Context, l *ted.Link) error {
	srlgJSON, _ := json.Marshal(l.SRLG)
	flexJSON, _ := json.Marshal(l.FlexAlgoMetrics)

	var localIP, remoteIP *string
	if l.LocalIP.IsValid() {
		s := l.LocalIP.String()
		localIP = &s
	}
	if l.RemoteIP.IsValid() {
		s := l.RemoteIP.String()
		remoteIP = &s
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO ted_links
			(id, local_node_id, remote_node_id, local_ip, remote_ip,
			 te_metric, igp_metric, max_bandwidth, reserved_bw,
			 admin_group, srlg, latency_us, flex_algo_metrics, last_seen)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (local_node_id, remote_node_id, local_ip) DO UPDATE SET
			remote_ip         = EXCLUDED.remote_ip,
			te_metric         = EXCLUDED.te_metric,
			igp_metric        = EXCLUDED.igp_metric,
			max_bandwidth     = EXCLUDED.max_bandwidth,
			reserved_bw       = EXCLUDED.reserved_bw,
			admin_group       = EXCLUDED.admin_group,
			srlg              = EXCLUDED.srlg,
			latency_us        = EXCLUDED.latency_us,
			flex_algo_metrics = EXCLUDED.flex_algo_metrics,
			last_seen         = EXCLUDED.last_seen`,
		l.ID, l.LocalNodeID, l.RemoteNodeID, localIP, remoteIP,
		l.TEMetric, l.IGPMetric, l.MaxBandwidth, l.ReservedBandwidth,
		l.AdminGroup, srlgJSON, l.Latency, flexJSON, l.LastSeen,
	)
	return err
}

// DeleteLink removes a link by its endpoint pair.
func (s *Store) DeleteLink(ctx context.Context, localNodeID, remoteNodeID string, localIP netip.Addr) error {
	ipStr := ""
	if localIP.IsValid() {
		ipStr = localIP.String()
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM ted_links WHERE local_node_id=$1 AND remote_node_id=$2 AND local_ip=$3`,
		localNodeID, remoteNodeID, ipStr,
	)
	return err
}

// LoadLinks restores all links from ted_links, typically called on startup.
func (s *Store) LoadLinks(ctx context.Context) ([]*ted.Link, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, local_node_id, remote_node_id, local_ip, remote_ip,
		       te_metric, igp_metric, max_bandwidth, reserved_bw,
		       admin_group, srlg, latency_us, flex_algo_metrics, last_seen
		FROM ted_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []*ted.Link
	for rows.Next() {
		var (
			l                  ted.Link
			localIP, remoteIP  *string
			srlgJSON, flexJSON []byte
		)
		if err := rows.Scan(
			&l.ID, &l.LocalNodeID, &l.RemoteNodeID,
			&localIP, &remoteIP,
			&l.TEMetric, &l.IGPMetric, &l.MaxBandwidth, &l.ReservedBandwidth,
			&l.AdminGroup, &srlgJSON, &l.Latency, &flexJSON, &l.LastSeen,
		); err != nil {
			return nil, err
		}
		if localIP != nil {
			l.LocalIP, _ = netip.ParseAddr(*localIP)
		}
		if remoteIP != nil {
			l.RemoteIP, _ = netip.ParseAddr(*remoteIP)
		}
		_ = json.Unmarshal(srlgJSON, &l.SRLG)
		_ = json.Unmarshal(flexJSON, &l.FlexAlgoMetrics)
		links = append(links, &l)
	}
	return links, rows.Err()
}

// ── LSP Persistence ────────────────────────────────────────────────────────

// SaveLSP upserts an LSP into the lsps table.
func (s *Store) SaveLSP(ctx context.Context, l *lsp.LSP) error {
	segsJSON, _ := json.Marshal(l.SegmentList)
	constJSON, _ := json.Marshal(l.Constraints)

	var sessID *uuid.UUID
	if l.SessionID != uuid.Nil {
		sessID = &l.SessionID
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO lsps
			(id, name, pcc, src_router_id, dst_router_id, sr_type, status,
			 bsid, segment_list, computed_metric, constraints,
			 pcep_id, srp_id, session_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
			name            = EXCLUDED.name,
			pcc             = EXCLUDED.pcc,
			status          = EXCLUDED.status,
			bsid            = EXCLUDED.bsid,
			segment_list    = EXCLUDED.segment_list,
			computed_metric = EXCLUDED.computed_metric,
			constraints     = EXCLUDED.constraints,
			pcep_id         = EXCLUDED.pcep_id,
			srp_id          = EXCLUDED.srp_id,
			session_id      = EXCLUDED.session_id,
			updated_at      = EXCLUDED.updated_at`,
		l.ID, l.Name, l.PCC, l.SrcRouterID, l.DstRouterID,
		string(l.SRType), string(l.Status),
		l.BSID, segsJSON, l.ComputedMetric, constJSON,
		l.PCEPID, l.SRPID, sessID,
		l.CreatedAt, l.UpdatedAt,
	)
	return err
}

// DeleteLSP removes an LSP from the database.
func (s *Store) DeleteLSP(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM lsps WHERE id = $1`, id)
	return err
}

// LoadLSPs returns all LSPs from the database, used on startup.
func (s *Store) LoadLSPs(ctx context.Context) ([]*lsp.LSP, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, pcc, src_router_id, dst_router_id, sr_type, status,
		       bsid, segment_list, computed_metric, constraints,
		       pcep_id, srp_id, session_id, created_at, updated_at
		FROM lsps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lsps []*lsp.LSP
	for rows.Next() {
		var (
			l                   lsp.LSP
			srType, status      string
			segsJSON, constJSON []byte
			sessID              *uuid.UUID
		)
		if err := rows.Scan(
			&l.ID, &l.Name, &l.PCC, &l.SrcRouterID, &l.DstRouterID,
			&srType, &status,
			&l.BSID, &segsJSON, &l.ComputedMetric, &constJSON,
			&l.PCEPID, &l.SRPID, &sessID,
			&l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			return nil, err
		}
		l.SRType = lsp.SRType(srType)
		l.Status = lsp.LSPStatus(status)
		if sessID != nil {
			l.SessionID = *sessID
		}
		_ = json.Unmarshal(segsJSON, &l.SegmentList)
		_ = json.Unmarshal(constJSON, &l.Constraints)
		lsps = append(lsps, &l)
	}
	return lsps, rows.Err()
}

// SaveLSPHistory appends a history entry for an LSP.
func (s *Store) SaveLSPHistory(ctx context.Context, lspID uuid.UUID, entry lsp.HistoryEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO lsp_history (lsp_id, event, old_status, new_status, details, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		lspID,
		string(entry.Event),
		string(entry.OldStatus),
		string(entry.NewStatus),
		entry.Details,
		entry.Timestamp,
	)
	return err
}

// LoadLSPHistory returns the change history for a specific LSP.
func (s *Store) LoadLSPHistory(ctx context.Context, lspID uuid.UUID) ([]lsp.HistoryEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event, old_status, new_status, details, timestamp
		FROM lsp_history WHERE lsp_id = $1 ORDER BY timestamp ASC`, lspID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []lsp.HistoryEntry
	for rows.Next() {
		var (
			h                                    lsp.HistoryEntry
			event, oldStatus, newStatus, details string
		)
		if err := rows.Scan(&event, &oldStatus, &newStatus, &details, &h.Timestamp); err != nil {
			return nil, err
		}
		h.Event = lsp.ChangeEventType(event)
		h.OldStatus = lsp.LSPStatus(oldStatus)
		h.NewStatus = lsp.LSPStatus(newStatus)
		h.Details = details
		history = append(history, h)
	}
	return history, rows.Err()
}

// ── Restore Helpers ────────────────────────────────────────────────────────

// RestoreTED loads the persisted TED state into the provided in-memory TED.
// Called once at daemon startup after BGP-LS is started.
func RestoreTED(ctx context.Context, store *Store, t *ted.TED) error {
	nodes, err := store.LoadNodes(ctx)
	if err != nil {
		return fmt.Errorf("db: restore nodes: %w", err)
	}
	for _, n := range nodes {
		t.UpsertNode(n)
	}
	log.Info().Int("nodes", len(nodes)).Msg("TED nodes restored from database")

	links, err := store.LoadLinks(ctx)
	if err != nil {
		return fmt.Errorf("db: restore links: %w", err)
	}
	for _, l := range links {
		t.UpsertLink(l)
	}
	log.Info().Int("links", len(links)).Msg("TED links restored from database")
	return nil
}

// RestoreLSPs loads persisted LSPs into the given LSP manager.
func RestoreLSPs(ctx context.Context, store *Store, mgr *lsp.Manager) error {
	lsps, err := store.LoadLSPs(ctx)
	if err != nil {
		return fmt.Errorf("db: restore lsps: %w", err)
	}
	for _, l := range lsps {
		if _, err := mgr.Create(l); err != nil {
			log.Warn().Err(err).Str("lsp_id", l.ID.String()).Msg("db: could not restore LSP")
		}
	}
	log.Info().Int("lsps", len(lsps)).Msg("LSPs restored from database")
	return nil
}

// maskPassword replaces the password in a DSN for safe logging.
func maskPassword(dsn string) string {
	// postgres://user:password@host:port/db → postgres://user:***@host:port/db
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == ':' && i+1 < len(dsn) {
			// find the @ after this colon
			for j := i + 1; j < len(dsn); j++ {
				if dsn[j] == '@' {
					return dsn[:i+1] + "***" + dsn[j:]
				}
			}
		}
	}
	return dsn
}
