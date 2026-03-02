// Package api implements the Controllore REST API and WebSocket server
// using the Fiber v2 framework. All clients (CLI and Web UI) are purely
// API consumers — no server-side rendering.
package api

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/buraglio/controllore/internal/cspf"
	"github.com/buraglio/controllore/internal/events"
	"github.com/buraglio/controllore/internal/lsp"
	"github.com/buraglio/controllore/internal/pcep"
	"github.com/buraglio/controllore/internal/ted"
	srv6types "github.com/buraglio/controllore/pkg/srv6"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Server is the Controllore API server.
type Server struct {
	app        *fiber.App
	ted        *ted.TED
	lspMgr     *lsp.Manager
	pcepSrv    *pcep.Server
	cspfEngine *cspf.Engine
	eventBus   *events.Bus
}

// New creates a new API server wiring all subsystems together.
func New(
	t *ted.TED,
	lspMgr *lsp.Manager,
	pcepSrv *pcep.Server,
	cspfEngine *cspf.Engine,
	bus *events.Bus,
) *Server {
	s := &Server{
		ted:        t,
		lspMgr:     lspMgr,
		pcepSrv:    pcepSrv,
		cspfEngine: cspfEngine,
		eventBus:   bus,
	}

	app := fiber.New(fiber.Config{
		AppName:           "Controllore PCE API v1",
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		DisableKeepalive:  false,
		EnablePrintRoutes: false,
		ErrorHandler:      s.errorHandler,
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} | ${status} | ${latency} | ${method} ${path}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
	}))

	// Routes
	v1 := app.Group("/api/v1")

	// Health & version
	v1.Get("/health", s.handleHealth)
	v1.Get("/version", s.handleVersion)

	// Topology
	topo := v1.Group("/topology")
	topo.Get("/", s.handleTopology)
	topo.Get("/nodes", s.handleNodes)
	topo.Get("/nodes/:id", s.handleNode)
	topo.Get("/links", s.handleLinks)
	topo.Get("/segments", s.handleSegments)
	topo.Get("/export", s.handleTopologyExport)

	// LSPs
	lsps := v1.Group("/lsps")
	lsps.Get("/", s.handleLSPList)
	lsps.Post("/", s.handleLSPCreate)
	lsps.Get("/:id", s.handleLSPGet)
	lsps.Patch("/:id", s.handleLSPUpdate)
	lsps.Delete("/:id", s.handleLSPDelete)
	lsps.Get("/:id/history", s.handleLSPHistory)

	// Path Computation
	paths := v1.Group("/paths")
	paths.Post("/compute", s.handlePathCompute)
	paths.Post("/disjoint", s.handlePathDisjoint)

	// PCEP Sessions
	sessions := v1.Group("/sessions")
	sessions.Get("/", s.handleSessionList)
	sessions.Get("/:id", s.handleSessionGet)

	// Nodes (PCCs)
	nodes := v1.Group("/nodes")
	nodes.Get("/", s.handleNodeList)
	nodes.Get("/:id/lsps", s.handleNodeLSPs)

	// Metrics (Prometheus)
	app.Get("/metrics", s.handleMetrics)

	// WebSocket — upgrade middleware must come before the handler
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/ws/events", websocket.New(s.handleWSEvents))
	app.Get("/ws/topology", websocket.New(s.handleWSTopology))

	s.app = app
	return s
}

// Listen starts the HTTP server on the given address.
func (s *Server) Listen(addr string) error {
	log.Info().Str("addr", addr).Msg("Controllore API server starting")
	return s.app.Listen(addr)
}

// ============================================================
// Health & Version
// ============================================================

func (s *Server) handleHealth(c *fiber.Ctx) error {
	stats := s.ted.Stats()
	return c.JSON(fiber.Map{
		"status":    "ok",
		"ted_nodes": stats.NodeCount,
		"ted_links": stats.LinkCount,
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleVersion(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"name":    "Controllore",
		"version": "0.1.0",
		"commit":  "dev",
	})
}

// ============================================================
// Topology Handlers
// ============================================================

func (s *Server) handleTopology(c *fiber.Ctx) error {
	nodes := s.ted.AllNodes()
	links := s.ted.AllLinks()
	return c.JSON(fiber.Map{
		"nodes": nodes,
		"links": links,
		"meta":  s.ted.Stats(),
	})
}

func (s *Server) handleNodes(c *fiber.Ctx) error {
	return c.JSON(s.ted.AllNodes())
}

func (s *Server) handleNode(c *fiber.Ctx) error {
	routerID := c.Params("id")
	node := s.ted.GetNode(routerID)
	if node == nil {
		return fiber.NewError(fiber.StatusNotFound, "node not found: "+routerID)
	}
	return c.JSON(node)
}

func (s *Server) handleLinks(c *fiber.Ctx) error {
	return c.JSON(s.ted.AllLinks())
}

func (s *Server) handleSegments(c *fiber.Ctx) error {
	return c.JSON(s.ted.AllSRv6SIDs())
}

func (s *Server) handleTopologyExport(c *fiber.Ctx) error {
	format := c.Query("fmt", "json")
	nodes := s.ted.AllNodes()
	links := s.ted.AllLinks()

	switch format {
	case "dot":
		dot := "digraph topology {\n  rankdir=LR;\n"
		for _, n := range nodes {
			dot += "  \"" + n.RouterID + "\" [label=\"" + n.Hostname + "\\n" + n.RouterID + "\"];\n"
		}
		for _, l := range links {
			dot += "  \"" + l.LocalNodeID + "\" -> \"" + l.RemoteNodeID +
				"\" [label=\"te=" + string(rune(l.TEMetric+'0')) + "\"];\n"
		}
		dot += "}\n"
		c.Set("Content-Type", "text/plain")
		return c.SendString(dot)
	default:
		return c.JSON(fiber.Map{"nodes": nodes, "links": links})
	}
}

// ============================================================
// LSP Handlers
// ============================================================

// CreateLSPRequest is the API request body for LSP creation.
type CreateLSPRequest struct {
	Name        string          `json:"name"`
	PCC         string          `json:"pcc"`
	Src         string          `json:"src"`
	Dst         string          `json:"dst"`
	SRType      lsp.SRType      `json:"sr_type"`
	Constraints lsp.Constraints `json:"constraints"`
	// ExplicitSIDs allows operator-specified explicit segment list (bypasses CSPF).
	ExplicitSIDs []string `json:"explicit_sids,omitempty"`
}

func (s *Server) handleLSPList(c *fiber.Ctx) error {
	// Optional filters
	pccFilter := c.Query("pcc", "")
	typeFilter := c.Query("type", "")

	all := s.lspMgr.All()
	var result []*lsp.LSP
	for _, l := range all {
		if pccFilter != "" && l.PCC != pccFilter {
			continue
		}
		if typeFilter != "" && string(l.SRType) != typeFilter {
			continue
		}
		result = append(result, l)
	}
	return c.JSON(result)
}

func (s *Server) handleLSPCreate(c *fiber.Ctx) error {
	var req CreateLSPRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body: "+err.Error())
	}
	if req.Src == "" || req.Dst == "" {
		return fiber.NewError(fiber.StatusBadRequest, "src and dst are required")
	}
	if req.SRType == "" {
		req.SRType = lsp.SRTypeSRv6
	}

	newLSP := &lsp.LSP{
		Name:        req.Name,
		PCC:         req.PCC,
		SrcRouterID: req.Src,
		DstRouterID: req.Dst,
		SRType:      req.SRType,
		Constraints: req.Constraints,
		Status:      lsp.LSPStatusPending,
	}

	// Compute path via CSPF (unless explicit SIDs provided)
	if len(req.ExplicitSIDs) == 0 {
		excludeNodes := make(map[string]struct{})
		for _, n := range req.Constraints.ExcludeNodes {
			excludeNodes[n] = struct{}{}
		}
		pathReq := cspf.PathRequest{
			SrcRouterID: req.Src,
			DstRouterID: req.Dst,
			Constraints: cspf.Constraints{
				MetricType:   req.Constraints.MetricType,
				MaxCost:      req.Constraints.MaxCost,
				MinBandwidth: req.Constraints.MinBandwidth,
				IncludeAny:   req.Constraints.IncludeAny,
				IncludeAll:   req.Constraints.IncludeAll,
				ExcludeAny:   req.Constraints.ExcludeAny,
				AvoidSRLG:    req.Constraints.AvoidSRLG,
				ExcludeNodes: excludeNodes,
				FlexAlgo:     req.Constraints.FlexAlgo,
				UseUSID:      req.Constraints.UseUSID,
				USIDBlockLen: req.Constraints.USIDBlockLen,
				MaxSIDDepth:  req.Constraints.MaxSIDDepth,
			},
		}
		path, err := s.cspfEngine.Compute(pathReq)
		if err != nil {
			return fiber.NewError(fiber.StatusUnprocessableEntity, "path computation failed: "+err.Error())
		}
		newLSP.SegmentList = path.SegmentList
		newLSP.ComputedMetric = path.Cost
	}

	created, err := s.lspMgr.Create(newLSP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	// Publish event
	s.eventBus.PublishLSP(events.EvLSPCreated, created.ID.String(), created)

	return c.Status(fiber.StatusCreated).JSON(created)
}

func (s *Server) handleLSPGet(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid LSP ID")
	}
	l, err := s.lspMgr.Get(id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, err.Error())
	}
	return c.JSON(l)
}

// UpdateLSPRequest describes which fields can be updated.
type UpdateLSPRequest struct {
	Constraints *lsp.Constraints `json:"constraints,omitempty"`
	// Recompute triggers a new CSPF computation with the updated constraints.
	Recompute bool `json:"recompute,omitempty"`
}

func (s *Server) handleLSPUpdate(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid LSP ID")
	}

	var req UpdateLSPRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	current, err := s.lspMgr.Get(id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, err.Error())
	}

	if req.Constraints != nil {
		current.Constraints = *req.Constraints
	}

	if req.Recompute {
		excludeNodes := make(map[string]struct{})
		for _, n := range current.Constraints.ExcludeNodes {
			excludeNodes[n] = struct{}{}
		}
		path, err := s.cspfEngine.Compute(cspf.PathRequest{
			SrcRouterID: current.SrcRouterID,
			DstRouterID: current.DstRouterID,
			Constraints: cspf.Constraints{
				MetricType:   current.Constraints.MetricType,
				MinBandwidth: current.Constraints.MinBandwidth,
				FlexAlgo:     current.Constraints.FlexAlgo,
				UseUSID:      current.Constraints.UseUSID,
				USIDBlockLen: current.Constraints.USIDBlockLen,
				ExcludeNodes: excludeNodes,
			},
		})
		if err != nil {
			return fiber.NewError(fiber.StatusUnprocessableEntity, "recompute failed: "+err.Error())
		}
		s.lspMgr.UpdateSegmentList(id, path.SegmentList, path.Cost)
	}

	s.eventBus.PublishLSP(events.EvLSPUpdated, id.String(), current)
	l, _ := s.lspMgr.Get(id)
	return c.JSON(l)
}

func (s *Server) handleLSPDelete(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid LSP ID")
	}
	if err := s.lspMgr.Delete(id); err != nil {
		return fiber.NewError(fiber.StatusNotFound, err.Error())
	}
	s.eventBus.PublishLSP(events.EvLSPDeleted, id.String(), nil)
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) handleLSPHistory(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid LSP ID")
	}
	h, err := s.lspMgr.History(id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, err.Error())
	}
	return c.JSON(h)
}

// ============================================================
// Path Computation Handlers
// ============================================================

type PathComputeRequest struct {
	Src         string           `json:"src"`
	Dst         string           `json:"dst"`
	Constraints cspf.Constraints `json:"constraints"`
}

type PathComputeResponse struct {
	NodeHops    []string        `json:"node_hops"`
	SegmentList []srv6types.SID `json:"segment_list"`
	Cost        uint32          `json:"cost"`
	MetricType  string          `json:"metric_type"`
	SIDCount    int             `json:"sid_count"`
}

func (s *Server) handlePathCompute(c *fiber.Ctx) error {
	var req PathComputeRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if req.Src == "" || req.Dst == "" {
		return fiber.NewError(fiber.StatusBadRequest, "src and dst are required")
	}

	path, err := s.cspfEngine.Compute(cspf.PathRequest{
		SrcRouterID: req.Src,
		DstRouterID: req.Dst,
		Constraints: req.Constraints,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	}

	return c.JSON(PathComputeResponse{
		NodeHops:    path.NodeHops,
		SegmentList: path.SegmentList,
		Cost:        path.Cost,
		MetricType:  path.MetricType.String(),
		SIDCount:    len(path.SegmentList),
	})
}

type DisjointPathResponse struct {
	Primary  PathComputeResponse `json:"primary"`
	Backup   PathComputeResponse `json:"backup"`
	Disjoint bool                `json:"disjoint"`
}

func (s *Server) handlePathDisjoint(c *fiber.Ctx) error {
	var req PathComputeRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// Primary path
	primary, err := s.cspfEngine.Compute(cspf.PathRequest{
		SrcRouterID: req.Src,
		DstRouterID: req.Dst,
		Constraints: req.Constraints,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "primary: "+err.Error())
	}

	// Backup: exclude all intermediate nodes of primary path
	excludeNodes := make(map[string]struct{})
	for i := 1; i < len(primary.NodeHops)-1; i++ {
		excludeNodes[primary.NodeHops[i]] = struct{}{}
	}
	backupConstraints := req.Constraints
	backupConstraints.ExcludeNodes = excludeNodes

	backup, err := s.cspfEngine.Compute(cspf.PathRequest{
		SrcRouterID: req.Src,
		DstRouterID: req.Dst,
		Constraints: backupConstraints,
	})

	resp := DisjointPathResponse{
		Primary: PathComputeResponse{
			NodeHops:    primary.NodeHops,
			SegmentList: primary.SegmentList,
			Cost:        primary.Cost,
			MetricType:  primary.MetricType.String(),
			SIDCount:    len(primary.SegmentList),
		},
		Disjoint: err == nil,
	}
	if err == nil {
		resp.Backup = PathComputeResponse{
			NodeHops:    backup.NodeHops,
			SegmentList: backup.SegmentList,
			Cost:        backup.Cost,
			MetricType:  backup.MetricType.String(),
			SIDCount:    len(backup.SegmentList),
		}
	}
	return c.JSON(resp)
}

// ============================================================
// PCEP Session Handlers
// ============================================================

func (s *Server) handleSessionList(c *fiber.Ctx) error {
	return c.JSON(s.pcepSrv.AllSessions())
}

func (s *Server) handleSessionGet(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid session ID")
	}
	for _, sess := range s.pcepSrv.AllSessions() {
		if sess.ID == id {
			return c.JSON(sess)
		}
	}
	return fiber.NewError(fiber.StatusNotFound, "session not found")
}

// ============================================================
// Node (PCC) Handlers
// ============================================================

func (s *Server) handleNodeList(c *fiber.Ctx) error {
	return c.JSON(s.ted.AllNodes())
}

func (s *Server) handleNodeLSPs(c *fiber.Ctx) error {
	nodeID := c.Params("id")
	return c.JSON(s.lspMgr.ByPCC(nodeID))
}

// ============================================================
// Metrics Handler (Prometheus)
// ============================================================

func (s *Server) handleMetrics(c *fiber.Ctx) error {
	stats := s.ted.Stats()
	lsps := s.lspMgr.All()
	activeLSPs := 0
	for _, l := range lsps {
		if l.Status == lsp.LSPStatusActive {
			activeLSPs++
		}
	}
	sessions := s.pcepSrv.AllSessions()

	// Simple Prometheus text format
	metrics := ""
	metrics += "# HELP controllore_ted_nodes_total Total nodes in TED\n"
	metrics += "# TYPE controllore_ted_nodes_total gauge\n"
	metrics += formatGauge("controllore_ted_nodes_total", float64(stats.NodeCount))
	metrics += "# HELP controllore_ted_links_total Total links in TED\n"
	metrics += "# TYPE controllore_ted_links_total gauge\n"
	metrics += formatGauge("controllore_ted_links_total", float64(stats.LinkCount))
	metrics += "# HELP controllore_lsps_total Total LSPs\n"
	metrics += "# TYPE controllore_lsps_total gauge\n"
	metrics += formatGauge("controllore_lsps_total", float64(len(lsps)))
	metrics += "# HELP controllore_lsps_active Active LSPs\n"
	metrics += "# TYPE controllore_lsps_active gauge\n"
	metrics += formatGauge("controllore_lsps_active", float64(activeLSPs))
	metrics += "# HELP controllore_pcep_sessions_active Active PCEP sessions\n"
	metrics += "# TYPE controllore_pcep_sessions_active gauge\n"
	metrics += formatGauge("controllore_pcep_sessions_active", float64(len(sessions)))

	c.Set("Content-Type", "text/plain; version=0.0.4")
	return c.SendString(metrics)
}

func formatGauge(name string, val float64) string {
	return name + " " + formatFloat(val) + "\n"
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ============================================================
// WebSocket Handlers
// ============================================================

// handleWSEvents streams real-time PCE events to connected clients.
func (s *Server) handleWSEvents(c *websocket.Conn) {
	subID := "ws-events-" + c.LocalAddr().String() + "-" + c.RemoteAddr().String()
	ch := s.eventBus.Subscribe(subID, 128)
	defer s.eventBus.Unsubscribe(subID)

	log.Info().Str("client", c.RemoteAddr().String()).Msg("WebSocket events client connected")

	for evt := range ch {
		data, err := json.Marshal(evt)
		if err != nil {
			continue
		}
		if err := c.WriteMessage(1, data); err != nil {
			break
		}
	}
}

// handleWSTopology streams topology delta events.
func (s *Server) handleWSTopology(c *websocket.Conn) {
	subID := "ws-topo-" + c.RemoteAddr().String()
	ch := s.eventBus.Subscribe(subID, 64)
	defer s.eventBus.Unsubscribe(subID)

	// Send full topology snapshot on connect
	snapshot := fiber.Map{
		"type":  "topology.snapshot",
		"nodes": s.ted.AllNodes(),
		"links": s.ted.AllLinks(),
	}
	if data, err := json.Marshal(snapshot); err == nil {
		c.WriteMessage(1, data)
	}

	// Stream topology delta events
	for evt := range ch {
		evtType := string(evt.Type)
		if len(evtType) < 8 || evtType[:8] != "topology" {
			continue
		}
		data, err := json.Marshal(evt)
		if err != nil {
			continue
		}
		if err := c.WriteMessage(1, data); err != nil {
			break
		}
	}
}

// ============================================================
// Error Handler
// ============================================================

func (s *Server) errorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	msg := "internal server error"
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		msg = e.Message
	}
	return c.Status(code).JSON(fiber.Map{
		"error":  msg,
		"status": code,
	})
}
