# Controllore — Architecture Reference

**SRv6/SR-MPLS Stateful PCE Controller**

> An open-source, API-first stateful Path Computation Element controller focused on SRv6 (full-SID and uSID) with SR-MPLS support, designed for large service provider WANs.

---

## Relevant RFCs & Drafts

| Document | Title |
|---|---|
| RFC 4655 | PCE Architecture |
| RFC 5440 | PCEP |
| RFC 8231 | Stateful PCE Extensions |
| RFC 8281 | PCE-Initiated LSPs (PCInitiate) |
| RFC 8664 | PCEP Extensions for SR Policy |
| RFC 8402 | SR Architecture |
| RFC 8754 | SRv6 Network Encoding |
| RFC 8986 | SRv6 Network Programming |
| RFC 9252 | BGP SRv6 for SRv6-based Services |
| RFC 9514 | BGP-LS Extensions for SRv6 |
| RFC 9085 | BGP-LS Extensions for SR |
| draft-ietf-pce-segment-routing-ipv6 | PCEP Extensions for SRv6 |
| draft-ietf-idr-bgp-ls-srv6-ext | BGP-LS for SRv6 |

---

## System Overview

Controllore is a **stateful PCE** that:

1. **Discovers** the network topology via BGP-LS (from FRR/GoBGP peers) and/or IS-IS-SR TLV parsing
2. **Maintains** a live Traffic Engineering Database (TED) with SRv6 SID tables, link metrics, TE attributes, and node capabilities
3. **Computes** constrained paths (CSPF) for SRv6 Policies and SR-MPLS LSPs
4. **Controls** network devices via PCEP (RFC 8231 stateful, RFC 8281 PCInitiate, RFC 8664 SR, draft SRv6 extensions)
5. **Exposes** all state and operations through a REST+WebSocket API
6. **Provides** a rich web UI and a full-featured CLI, both purely as API clients

---

## Technology Choices

### Core Language: **Go**

Go is the natural fit for this system:

- Excellent concurrency model (goroutines) for handling many simultaneous PCEP sessions and BGP-LS streams
- Strong networking primitives and binary protocol handling
- `gobgp` and `pola-pce` ecosystem libraries available as reference/dependency
- Low-latency, compiled, self-contained binaries
- `protobuf`/`gRPC` first-class support

### Key Libraries & Dependencies

| Component | Library | Notes |
|---|---|---|
| BGP-LS / GoBGP | `github.com/osrg/gobgp/v3` | BGP daemon + BGP-LS TED feed |
| PCEP | Custom (RFC 5440 + exts) | Go implementation, informed by pola-pce |
| REST API | `github.com/gofiber/fiber/v2` | fasthttp-based, performant |
| WebSocket | `fiber/websocket` | Topology push, LSP events |
| Protobuf/gRPC | `google.golang.org/grpc` | Internal service bus |
| Graph/CSPF | `github.com/yourbasic/graph` | Dijkstra/CSPF with constraints |
| Database | PostgreSQL + `pgx` | Persistent TED, LSP history |
| In-memory state | Redis (optional) | Fast LSP state cache |
| Metrics | Prometheus + Grafana | Counters, gauges for PCE operations |
| Config | YAML via `viper` | Operator-friendly configuration |
| CLI | `github.com/spf13/cobra` | Rich CLI, pure API client |
| Web UI | React + TypeScript | Vite, Cytoscape.js topology, shadcn/ui |
| Container | Docker + Docker Compose | Dev and production deployment |
| Open Router | FRRouting (FRR) | BGP-LS source, PCC |

---

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        CONTROLLORE SYSTEM                           │
│                                                                     │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────────────┐   │
│  │   CLI Client │   │   Web UI     │   │   External Systems   │   │
│  │  (Go/Cobra)  │   │ (React/TS)   │   │ (Automation/OSS/BSS) │   │
│  └──────┬───────┘   └──────┬───────┘   └──────────┬───────────┘   │
│         │                  │                       │               │
│         └──────────────────┴───────────────────────┘               │
│                            │  REST + WebSocket API                  │
│  ┌─────────────────────────▼───────────────────────────────────┐   │
│  │                  API Gateway (Fiber/Go)                      │   │
│  │  /api/v1/topology  /api/v1/lsps  /api/v1/paths  /ws/events  │   │
│  └──────┬──────────────────┬─────────────────┬─────────────────┘   │
│         │                  │                 │                     │
│  ┌──────▼──────┐  ┌────────▼──────┐  ┌──────▼──────────────────┐ │
│  │   TED       │  │  PCE Engine   │  │  PCEP Session Manager    │ │
│  │  Manager    │  │  (CSPF/       │  │  (Stateful, Initiated,   │ │
│  │             │  │   Constraints)│  │   Updated, Reported)     │ │
│  └──────┬──────┘  └────────┬──────┘  └──────────────────────────┘ │
│         │                  │                 │                     │
│  ┌──────▼──────────────────▼─────────────────▼─────────────────┐  │
│  │                    Event Bus (gRPC streams / channels)        │  │
│  └──────┬──────────────────────────────────────────────────────┘   │
│         │                                                          │
│  ┌──────▼──────────────────────────────────────────────────────┐   │
│  │              Southbound Adapters                             │   │
│  │  ┌──────────────┐   ┌─────────────────┐  ┌──────────────┐  │   │
│  │  │  BGP-LS      │   │  IS-IS SR       │  │  NETCONF/    │  │   │
│  │  │  Collector   │   │  Parser (future)│  │  gNMI(future)│  │   │
│  │  │  (GoBGP)     │   │                 │  │              │  │   │
│  │  └──────┬───────┘   └─────────────────┘  └──────────────┘  │   │
│  └─────────┼────────────────────────────────────────────────────┘  │
│            │                                                        │
└────────────┼────────────────────────────────────────────────────────┘
             │
    ┌─────────▼───────────────────────────┐
    │         Network Fabric               │
    │  ┌──────────┐  ┌──────────────────┐ │
    │  │  FRR     │  │  FRR / Hardware  │ │
    │  │  (BGP-LS │  │  Routers (PCCs)  │ │
    │  │   peer)  │  │  PCEP sessions   │ │
    │  └──────────┘  └──────────────────┘ │
    └──────────────────────────────────────┘
```

---

## Service Decomposition

### 1. `controllore-pced` — Core PCE Daemon (Go)

The central process containing:

- **TED Manager**: Maintains graph of nodes, links, SRv6 SID locators, NodeSIDs, AdjSIDs, flex-algos, TE metrics, admin groups, SRLG, MSD
- **BGP-LS Collector**: Embeds or peers with GoBGP; parses BGP-LS NLRIs (RFC 7752, RFC 9085, RFC 9514)
- **PCEP Server**: RFC 5440 + RFC 8231 stateful + RFC 8281 PCInitiate + RFC 8664 SR + draft SRv6
- **PCE Engine**: CSPF with constraints (bandwidth, TE metric, hop count, disjointness, affinity, SRLG-avoidance)
- **API Server**: Fiber-based REST + WebSocket server
- **gRPC Internal Bus**: Decouples subsystems, allows future scaling

### 2. `controllore-cli` — CLI Client (Go/Cobra)

Pure API client. Ships as a single binary:

```
controllore topology show [--format json|table|dot]
controllore lsp list [--node <name>] [--type srv6|mpls]
controllore lsp create --src <node> --dst <node> --type srv6 [--metric te|igp|latency] [--bw <bps>]
controllore lsp delete <lsp-id>
controllore lsp update <lsp-id> --metric latency
controllore path compute --src <node> --dst <node> [--constraints ...]
controllore node show <node-router-id>
controllore segment show [--node <name>]
controllore events watch  (streaming)
controllore config set api-url http://localhost:8080
```

### 3. `controllore-ui` — Web UI (React + TypeScript)

Pure API client. Vite-based SPA:

- **Topology View**: Cytoscape.js graph with SRv6 node/link annotations, TE metric overlays
- **LSP Dashboard**: Table of active PCE-controlled paths with SID stacks, metric, BW, state
- **LSP Detail**: Hop-by-hop SRv6 segment list, SID type (Node/Adj/Service), BSID, and PCC state
- **Path Studio**: Compute and optionally instantiate new paths with constraint form
- **Events Feed**: Live WebSocket stream of PCE events (LSP up/down, compute requests)
- **Metrics Panel**: Prometheus-sourced (via API) charts for PCE operations

---

## TED Data Model

```
Node {
  router_id     string       // 32-bit or full IPv6
  hostname      string
  isis_area     []string
  asn           uint32
  capabilities  NodeCaps
  srv6_locators []SRv6Locator
  flex_algos    []uint8
  igp_metric    uint32
  last_updated  time.Time
}

SRv6Locator {
  prefix      net.IPNet    // e.g. 2001:db8:100::/48
  algorithm   uint8
  metric      uint32
  sid_behaviors []SRv6SIDBehavior  // End, End.X, End.DT4, etc.
}

Link {
  local_node    string
  remote_node   string
  local_ip      net.IP
  remote_ip     net.IP
  local_adj_sid SRv6AdjSID
  te_metric     uint32
  igp_metric    uint32
  bandwidth     uint64     // bytes/sec
  reserved_bw   uint64
  admin_group   uint32
  srlg          []uint32
  latency       uint32     // microseconds
  last_updated  time.Time
}

LSP {
  id            uuid.UUID
  name          string
  pcc           string      // router-id of PCC
  src           string
  dst           string
  sr_type       enum        // SRv6 | SR_MPLS
  status        enum        // Active | Down | Delegated | Reported
  bsid          net.IP      // SRv6 BSID (IPv6 addr)
  segment_list  []Segment
  bandwidth     uint64
  metric_type   enum        // IGP | TE | Latency | Hopcount
  computed_metric uint32
  constraints   Constraints
  pcep_lspid    uint32
  created_at    time.Time
  updated_at    time.Time
}

Segment {
  type   enum    // SRv6_NODE | SRv6_ADJ | SRv6_SERVICE | MPLS_LABEL
  sid    string  // IPv6 address for SRv6 or label int for MPLS
  nai    string  // Node Adjacency Identifier (optional)
}
```

---

## API Design (REST + WebSocket)

### Base: `/api/v1`

#### Topology

| Method | Endpoint | Description |
|---|---|---|
| GET | `/topology` | Full topology snapshot (nodes + links) |
| GET | `/topology/nodes` | All nodes with SRv6 capability info |
| GET | `/topology/nodes/{id}` | Single node detail |
| GET | `/topology/links` | All links with TE attributes |
| GET | `/topology/segments` | SRv6 SIDs / locators / behaviors |

#### LSPs

| Method | Endpoint | Description |
|---|---|---|
| GET | `/lsps` | All LSPs |
| POST | `/lsps` | Create/initiate LSP |
| GET | `/lsps/{id}` | Single LSP detail |
| PATCH | `/lsps/{id}` | Update LSP (delegate, update constraints) |
| DELETE | `/lsps/{id}` | Delete/teardown LSP |

#### Path Computation

| Method | Endpoint | Description |
|---|---|---|
| POST | `/paths/compute` | Compute path, return segment list (no instantiation) |

#### PCEP Sessions

| Method | Endpoint | Description |
|---|---|---|
| GET | `/sessions` | All PCEP sessions |
| GET | `/sessions/{id}` | Session detail, state machine |

#### Events (WebSocket)

| Endpoint | Description |
|---|---|
| `WS /ws/events` | Subscribe to real-time topology/LSP change events |

---

## SRv6-Specific Design Considerations

### SID Types Handled
- `End` — Node SID, basic routing
- `End.X` — Adjacency SID, link steering
- `End.DX4`, `End.DT4`, `End.DT6`, `End.DT46` — VPN behaviors
- `End.B6.Encaps` — SRv6 policy binding SID
- `uN`, `uA`, `uDT4/6` — uSID behaviors (NEXT-C-SID, draft-ietf-spring-srv6-srh-compression)

### uSID Support
The TED tracks uSID-capable nodes (carrier prefix, uSID block). The CSPF path computation can produce compressed uSID lists using the uSID block format, dramatically reducing SRH overhead.

### Flex-Algorithm
Nodes advertise participation in flex-algo (128-255). The PCE tracks per-algo topology views and can compute paths within a specific flex-algo domain (e.g., algo 128 = latency-optimized, algo 129 = TE-disjoint).

### PCEP SRv6 Extensions
Implements `draft-ietf-pce-segment-routing-ipv6`:
- `SRv6-ERO` subobject (NAI + SID + Endpoint Behavior + Structure)
- SRv6 PCCap capability negotiation
- MSD type `SRv6-MSD` for SID depth enforcement

---

## Open Router Integration (FRR)

FRRouting is the reference PCC and BGP-LS source:

```
frr.conf highlights:
  - bgpd: neighbor controllore route-map BGP-LS import
  - bgpd: address-family link-state / neighbor <pce> activate
  - isisd: segment-routing srv6 / locator <name>
  - isisd: flex-algo 128 metric-type delay
  - pathd: pcep / pce <name> address <ip>
  - pathd: sr-te policy <name> / candidate-path preference 100 computed
```

FRR `pathd` acts as PCEP client (PCC), receiving PCInitiate/PCUpdate from Controllore.

---

## Deployment

### Docker Compose (Development)

```
services:
  pced:        # Controllore core daemon
  postgres:    # TED persistence
  redis:       # LSP state cache
  gobgp:       # BGP-LS peer (optional sidecar)
  prometheus:  # Metrics
  grafana:     # Dashboards
  ui:          # React SPA dev server
  frr:         # Reference PCC/BGP-LS source
```

### Production

- `controllore-pced` as a systemd service or Kubernetes Deployment
- PostgreSQL for durability
- Prometheus/Grafana for observability
- Load-balanced API tier (stateless web layer, stateful PCE goroutines)

---

## Repository Structure

```
controllore/
├── cmd/
│   ├── pced/           # PCE daemon entry point
│   └── cli/            # CLI binary entry point
├── internal/
│   ├── api/            # Fiber REST + WebSocket handlers
│   ├── bgpls/          # BGP-LS collector (GoBGP integration)
│   ├── pcep/           # PCEP server + message codec
│   ├── ted/            # TED data model + graph
│   ├── cspf/           # Path computation engine
│   ├── lsp/            # LSP lifecycle manager
│   ├── events/         # Internal event bus
│   └── config/         # Configuration loader
├── pkg/
│   ├── pcep/           # Exportable PCEP library
│   └── srv6/           # SRv6 SID/NLRI types
├── ui/                 # React + TypeScript UI
│   ├── src/
│   │   ├── components/ # Topology, LSPTable, etc.
│   │   ├── pages/
│   │   └── api/        # API client hooks
│   └── vite.config.ts
├── deploy/
│   ├── docker-compose.yml
│   ├── frr/            # Reference FRR configs
│   └── prometheus/
├── docs/
│   ├── api/            # OpenAPI spec
│   └── architecture/
├── go.mod
└── README.md
```
