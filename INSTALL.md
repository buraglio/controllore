# Controllore — Installation Guide

This document covers every supported installation path: building from source, Docker Compose (recommended for evaluation), and bare-metal / production deployment.

---

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Build from Source](#build-from-source)
3. [Docker Compose (Full Stack)](#docker-compose-full-stack)
4. [Manual / Production Deployment](#manual--production-deployment)
5. [Configuration Reference](#configuration-reference)
6. [FRR PCC Configuration](#frr-pcc-configuration)
7. [Verifying the Installation](#verifying-the-installation)
8. [Web UI Setup](#web-ui-setup)
9. [Upgrading](#upgrading)
10. [Troubleshooting](#troubleshooting)

---

## Prerequisites

### Operating System

Controllore runs on any Linux distribution or macOS. The PCEP server typically runs on Linux in production. The following have been tested:

- Ubuntu 22.04 / 24.04
- Debian 12
- Rocky Linux / RHEL 9
- macOS 13+ (development)

### System Packages

#### macOS

```bash
# Install Homebrew if not present
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

brew install go node git docker docker-compose postgresql redis
```

#### Ubuntu / Debian

```bash
sudo apt-get update
sudo apt-get install -y \
    git curl wget \
    build-essential \
    ca-certificates \
    postgresql-client \
    redis-tools
```

### Go (≥ 1.21)

```bash
# Download the latest Go release from https://golang.org/dl/
# Example for Linux amd64:
wget https://go.dev/dl/go1.23.4.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.23.4.linux-amd64.tar.gz

# Add to PATH (add to ~/.bashrc or ~/.zshrc)
export PATH=$PATH:/usr/local/go/bin
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin

# Verify
go version
```

### Node.js (≥ 20) — for the Web UI only

```bash
# Using the Node Version Manager (recommended)
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
source ~/.bashrc
nvm install 20
nvm use 20

# Verify
node --version  # v20.x.x
npm --version   # 10.x.x
```

### Docker + Docker Compose — for the full-stack option

```bash
# Ubuntu/Debian
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER
newgrp docker

# Verify
docker version
docker compose version
```

---

## Build from Source

### 1. Clone the Repository

```bash
git clone https://github.com/buraglio/controllore.git
cd controllore
```

### 2. Download Go Dependencies

```bash
go mod download
```

### 3. Build Binaries

```bash
# PCE daemon
go build -o pced ./cmd/pced

# CLI client
go build -o controllore ./cmd/cli

# Optional: install system-wide
sudo mv pced /usr/local/bin/controllore-pced
sudo mv controllore /usr/local/bin/controllore
```

For a stripped production binary (smaller, no debug symbols):

```bash
go build -ldflags="-s -w" -o pced ./cmd/pced
go build -ldflags="-s -w" -o controllore ./cmd/cli
```

Cross-compile for Linux from macOS:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -ldflags="-s -w" -o pced-linux-amd64 ./cmd/pced
```

### 4. Set Up Configuration

```bash
cp deploy/controllore.yaml ./controllore.yaml
```

Edit `controllore.yaml` to match your environment. See [Configuration Reference](#configuration-reference) below.

### 5. Provision PostgreSQL (Optional — in-memory TED works without it)

```bash
# Create the database and user
psql -U postgres <<'EOF'
CREATE USER controllore WITH PASSWORD 'controllore';
CREATE DATABASE controllore OWNER controllore;
GRANT ALL PRIVILEGES ON DATABASE controllore TO controllore;
EOF
```

### 6. Start the Daemon

```bash
./pced --config ./controllore.yaml
```

Expected output:

```
INF Controllore PCE daemon starting
INF API server starting addr=0.0.0.0:8080
INF PCEP server listening addr=0.0.0.0:4189
```

### 7. Verify

```bash
curl -s http://localhost:8080/api/v1/health | python3 -m json.tool
```

```json
{
    "status": "ok",
    "ted_nodes": 0,
    "ted_links": 0,
    "timestamp": "2026-03-02T19:34:00Z"
}
```

---

## Docker Compose (Full Stack)

The Docker Compose stack brings up the complete environment in one command: PCE daemon, PostgreSQL, Redis, FRR (reference PCC), Prometheus, Grafana, and the Web UI dev server.

### 1. Clone and Enter the Deploy Directory

```bash
git clone https://github.com/buraglio/controllore.git
cd controllore/deploy
```

### 2. Review and Edit the Configuration

```bash
# Edit BGP-LS peer addresses, ASN, etc.
vi controllore.yaml
```

### 3. Start the Stack

```bash
docker compose up -d
```

Watch the logs:

```bash
docker compose logs -f pced
```

### 4. Verify Services

| Service | URL | Default Credentials |
|---------|-----|---------------------|
| PCE API | `http://localhost:8080/api/v1/health` | — |
| PCEP server | `tcp://localhost:4189` | — |
| Web UI | `http://localhost:5173` | — |
| Prometheus | `http://localhost:9090` | — |
| Grafana | `http://localhost:3001` | `admin` / `controllore` |

### 5. Stop the Stack

```bash
docker compose down

# To also destroy data volumes:
docker compose down -v
```

### 6. Rebuild after Code Changes

```bash
docker compose build pced
docker compose up -d pced
```

---

## Manual / Production Deployment

### Systemd Service

After building the binary and placing it in `/usr/local/bin/`:

```bash
sudo tee /etc/systemd/system/controllore-pced.service <<'EOF'
[Unit]
Description=Controllore SRv6 Stateful PCE Daemon
Documentation=https://github.com/buraglio/controllore
After=network-online.target postgresql.service redis.service
Wants=network-online.target

[Service]
Type=simple
User=controllore
Group=controllore
ExecStart=/usr/local/bin/controllore-pced --config /etc/controllore/controllore.yaml
Restart=on-failure
RestartSec=5s
LimitNOFILE=65535

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/controllore /var/log/controllore

[Install]
WantedBy=multi-user.target
EOF

# Create required directories and user
sudo useradd -r -s /bin/false controllore
sudo mkdir -p /etc/controllore /var/lib/controllore /var/log/controllore
sudo chown controllore: /var/lib/controllore /var/log/controllore
sudo cp /path/to/controllore.yaml /etc/controllore/controllore.yaml
sudo chmod 640 /etc/controllore/controllore.yaml
sudo chown root:controllore /etc/controllore/controllore.yaml

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable --now controllore-pced

# Check status
sudo systemctl status controllore-pced
sudo journalctl -u controllore-pced -f
```

### Firewall Rules

```bash
# PCEP (TCP) — open to PCC routers only
sudo ufw allow from <router-subnet> to any port 4189 proto tcp

# API (TCP) — open to management hosts or behind a reverse proxy
sudo ufw allow from <mgmt-subnet> to any port 8080 proto tcp

# BGP-LS (TCP) — for BGP session with FRR/routers
sudo ufw allow from <router-subnet> to any port 179 proto tcp
```

### Reverse Proxy (nginx)

```nginx
server {
    listen 443 ssl;
    server_name pce.example.com;

    ssl_certificate     /etc/ssl/certs/controllore.crt;
    ssl_certificate_key /etc/ssl/private/controllore.key;

    # REST API
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }

    # WebSocket — requires upgrade headers
    location /ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 86400s;
    }

    # Prometheus metrics (restrict to monitoring subnet)
    location /metrics {
        proxy_pass http://127.0.0.1:8080;
        allow 10.0.0.0/8;
        deny all;
    }
}
```

---

## Configuration Reference

The default configuration file is `controllore.yaml`. All values can be overridden with environment variables prefixed `CONTROLLORE_` (e.g., `CONTROLLORE_LOG_LEVEL=debug`).

```yaml
# ── API Server ────────────────────────────────────────────────────────────
server:
  host: "0.0.0.0"        # Bind address for the REST/WS API
  port: 8080             # HTTP port

# ── PostgreSQL (TED + LSP persistence) ───────────────────────────────────
database:
  host: "localhost"      # PostgreSQL hostname
  port: 5432
  name: "controllore"
  user: "controllore"
  password: "changeme"
  ssl_mode: "disable"    # disable | require | verify-full

# ── Redis (optional LSP state cache) ─────────────────────────────────────
redis:
  addr: "localhost:6379"
  password: ""
  db: 0

# ── BGP-LS Collector ─────────────────────────────────────────────────────
bgpls:
  local_as: 65000        # Local BGP AS number
  local_addr: "0.0.0.0"  # Source address for BGP sessions
  router_id: "192.0.2.254"
  peers:
    - addr: "192.0.2.1"  # FRR / router BGP-LS peer address
      as: 65000          # Peer AS (iBGP typical for BGP-LS)
      description: "frr-core-1"
      hold_time: "90s"
    - addr: "192.0.2.2"
      as: 65000
      description: "frr-core-2"
      hold_time: "90s"

# ── PCEP Server ───────────────────────────────────────────────────────────
pcep:
  listen_addr: "0.0.0.0"
  port: 4189             # IANA assigned PCEP port
  keepalive: 30          # Keepalive interval (seconds)
  dead_timer: 120        # Dead timer (seconds; must be > 4x keepalive)
  tls: false             # Enable PCEP-TLS (RFC 8253)
  tls_cert: ""           # Path to TLS certificate (PEM)
  tls_key: ""            # Path to TLS private key (PEM)
  tls_ca: ""             # Path to CA cert for client verification

# ── Authentication ────────────────────────────────────────────────────────
auth:
  enabled: false         # Enable JWT authentication for the API
  jwt_secret: "change-me-in-production"
  token_ttl: "24h"

# ── Logging ───────────────────────────────────────────────────────────────
log:
  level: "info"          # debug | info | warn | error
  format: "console"      # console | json
```

### Environment Variable Overrides

| Environment Variable | Config Key | Example |
|----------------------|------------|---------|
| `CONTROLLORE_API_URL` | *(CLI only)* | `http://pce:8080` |
| `CONTROLLORE_LOG_LEVEL` | `log.level` | `debug` |
| `CONTROLLORE_LOG_FORMAT` | `log.format` | `json` |
| `CONTROLLORE_SERVER_PORT` | `server.port` | `9090` |
| `CONTROLLORE_DATABASE_PASSWORD` | `database.password` | `secret` |
| `CONTROLLORE_PCEP_PORT` | `pcep.port` | `4189` |
| `CONTROLLORE_AUTH_ENABLED` | `auth.enabled` | `true` |
| `CONTROLLORE_AUTH_JWT_SECRET` | `auth.jwt_secret` | `mysecret` |

---

## FRR PCC Configuration

FRRouting must be configured with:

1. **IS-IS** advertising SRv6 locators and SID behaviors (or OSPF-SR for SR-MPLS)
2. **bgpd** with the BGP-LS address family exporting the IS-IS topology
3. **pathd** as the PCEP PCC connecting to Controllore

A complete reference configuration is provided in [`deploy/frr/frr.conf`](deploy/frr/frr.conf).

### Minimum Required Daemons

Edit `/etc/frr/daemons`:

```
bgpd=yes
isisd=yes
pathd=yes
```

### Required FRR Version

FRR **8.4 or later** is required for `pathd` SRv6 PCEP support. FRR 10.x is recommended.

```bash
# Check FRR version
vtysh -c "show version"
```

### BGP-LS Peering (FRR → Controllore)

In `frr.conf`:

```
router bgp 65000
 bgp router-id 192.0.2.1
 neighbor 192.0.2.254 remote-as 65000
 neighbor 192.0.2.254 description controllore-pce
 !
 address-family link-state
  neighbor 192.0.2.254 activate
 !
!
```

Where `192.0.2.254` is the Controllore PCE daemon's address.

### PCEP PCC (FRR pathd → Controllore)

```
segment-routing
 traffic-eng
  pcep
   pce CONTROLLORE
    address ip 192.0.2.254
    port 4189
   !
   pcc
    peer CONTROLLORE
    flag-delegation
   !
  !
 !
!
```

### SRv6 Locator (IS-IS)

```
router isis CORE
 segment-routing on
 segment-routing srv6
  locator CORE
 !
!

segment-routing
 srv6
  locators
   locator CORE
    prefix 2001:db8:1::/48 block-len 32 node-len 16
    behavior usid
   !
  !
 !
!
```

---

## Verifying the Installation

### Health Check

```bash
curl -s http://localhost:8080/api/v1/health
# Expected: {"status":"ok","ted_nodes":N,"ted_links":N,"timestamp":"..."}
```

### After BGP-LS Peering Establishes

```bash
# Should show nodes populated from BGP-LS
curl -s http://localhost:8080/api/v1/topology/nodes | python3 -m json.tool

# Using the CLI
./controllore topology nodes
```

### After a PCC Connects via PCEP

```bash
curl -s http://localhost:8080/api/v1/sessions
./controllore session list
```

### Compute a Test Path

```bash
./controllore path compute \
    --src 192.0.2.1 \
    --dst 192.0.2.5 \
    --metric te \
    --usid

# Expected output:
# Cost:       40 (te)
# SID Count:  3
# Node Hops:
#   1. 192.0.2.1
#   2. 192.0.2.3
#   3. 192.0.2.5
# Segment List:
#   1. [uN] 2001:db8:1:0100::
#   2. [uN] 2001:db8:3:0100::
#   3. [uDT46] 2001:db8:5:0100::
```

### Prometheus Metrics

```bash
curl -s http://localhost:8080/metrics | grep controllore_
```

---

## Web UI Setup

### Development Server

```bash
cd ui
npm install
npm run dev
# Open http://localhost:5173
```

The dev server proxies `/api/*` and `/ws/*` to `http://localhost:8080` automatically via `vite.config.ts`.

### Production Build

```bash
cd ui
npm run build
# Output in ui/dist/

# Serve with any static file server, e.g.:
npx serve -s dist -l 5173
# or with nginx (see the nginx config above, add: root /var/www/controllore/dist;)
```

### Pointing the UI at a Remote API

When serving the built UI from nginx (no Vite proxy), set the API URL via the backend's CORS configuration and configure the UI environment:

```bash
# Create ui/.env.production
echo "VITE_API_URL=https://pce.example.com" > ui/.env.production
npm run build
```

Then update the API client in `ui/src/main.tsx` or use the `CONTROLLORE_API_URL` documented approach for the CLI client.

---

## Upgrading

### From Source

```bash
cd controllore
git pull origin main
go build -o pced ./cmd/pced
go build -o controllore ./cmd/cli
sudo systemctl restart controllore-pced
```

### Docker Compose

```bash
cd deploy
docker compose pull
docker compose build --no-cache pced
docker compose up -d
```

---

## Troubleshooting

### PCEP Sessions Not Establishing

**Check PCEP port is reachable from the PCC:**

```bash
# From the router (or a host in the same network)
nc -zv <pce-ip> 4189
```

**Check pced logs for connection attempts:**

```bash
journalctl -u controllore-pced -f | grep pcep
```

**Verify FRR pathd is configured and enabled:**

```bash
vtysh -c "show pathd"
vtysh -c "show pcep session"
```

---

### BGP-LS TED Not Populating

**Verify the BGP session is up:**

```bash
./controllore session list
vtysh -c "show bgp summary"
```

**Check BGP-LS neighbor activation on FRR:**

```bash
vtysh -c "show bgp link-state summary"
```

**Confirm local AS and router-id match BGP session:**

```bash
curl -s http://localhost:8080/api/v1/health
```

---

### API Returns 500

```bash
# Enable debug logging
CONTROLLORE_LOG_LEVEL=debug ./pced --config controllore.yaml
```

---

### Path Computation Returns "no path found"

1. Check TED has nodes: `./controllore topology nodes`
2. Verify both source and destination router-IDs exist in the TED
3. If using `--flex-algo`, verify both endpoints participate in that algorithm
4. Try without constraints first: `./controllore path compute --src X --dst Y --metric igp`

---

### Port Conflicts

| Default Port | Service | Change Via |
|-------------|---------|-----------|
| `8080` | REST API | `server.port` in config |
| `4189` | PCEP | `pcep.port` in config |
| `5173` | UI dev server | `npm run dev -- --port XXXX` |

---

### Docker Compose: PostgreSQL Not Ready

```bash
docker compose logs postgres
# If "database system is ready to accept connections" is visible, it is healthy
# pced will retry the connection automatically
```

---

## Support

Please open a GitHub issue at `https://github.com/buraglio/controllore/issues` with:

- Controllore version (`./pced --version`)
- Go version (`go version`)
- FRR version (`vtysh -c "show version"`)
- Sanitized configuration file
- Relevant log output
