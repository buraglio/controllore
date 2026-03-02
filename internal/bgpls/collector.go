// Package bgpls implements the BGP-LS collector for Controllore.
// It embeds a GoBGP server, peers with FRR/hardware routers, and parses
// BGP-LS NLRIs (RFC 7752, RFC 9085, RFC 9514) into the TED.
package bgpls

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	api "github.com/osrg/gobgp/v3/api"
	gobgpapi "github.com/osrg/gobgp/v3/pkg/server"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/buraglio/controllore/internal/config"
	"github.com/buraglio/controllore/internal/events"
	"github.com/buraglio/controllore/internal/ted"
	"github.com/buraglio/controllore/pkg/srv6"
)

// Collector embeds a GoBGP server and translates BGP-LS NLRIs into TED updates.
type Collector struct {
	srv *gobgpapi.BgpServer
	ted *ted.TED
	bus *events.Bus
	cfg config.BGPLSConfig
}

// New creates a BGP-LS collector but does not start it.
func New(cfg config.BGPLSConfig, t *ted.TED, bus *events.Bus) *Collector {
	s := gobgpapi.NewBgpServer()
	return &Collector{
		srv: s,
		ted: t,
		bus: bus,
		cfg: cfg,
	}
}

// Run starts the embedded GoBGP server, adds configured peers, and begins
// watching for BGP-LS path updates. Blocks until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	go c.srv.Serve()

	// Configure global BGP
	err := c.srv.StartBgp(ctx, &api.StartBgpRequest{
		Global: &api.Global{
			Asn:        c.cfg.LocalAS,
			RouterId:   c.cfg.RouterID,
			ListenPort: -1, // passive only — routers connect to us
		},
	})
	if err != nil {
		return fmt.Errorf("bgpls: start bgp: %w", err)
	}
	log.Info().
		Uint32("asn", c.cfg.LocalAS).
		Str("router_id", c.cfg.RouterID).
		Msg("BGP-LS collector started")

	// BGP-LS address family: AFI=16388, SAFI=71
	bgplsFamily := &api.Family{
		Afi:  api.Family_AFI_LS,
		Safi: api.Family_SAFI_LS,
	}

	// Add peers from config
	for _, peer := range c.cfg.Peers {
		if err := c.addPeer(ctx, peer, bgplsFamily); err != nil {
			log.Error().Err(err).Str("peer", peer.Addr).Msg("BGP-LS: failed to add peer")
		}
	}

	// Watch for path updates in best-path table
	err = c.srv.WatchEvent(ctx, &api.WatchEventRequest{
		Table: &api.WatchEventRequest_Table{
			Filters: []*api.WatchEventRequest_Table_Filter{
				{Type: api.WatchEventRequest_Table_Filter_BEST},
			},
		},
	}, func(r *api.WatchEventResponse) {
		if t := r.GetTable(); t != nil {
			for _, path := range t.Paths {
				if path.Family.GetAfi() == api.Family_AFI_LS {
					c.handlePath(path)
				}
			}
		}
	})
	if err != nil {
		return fmt.Errorf("bgpls: watch event: %w", err)
	}

	<-ctx.Done()
	log.Info().Msg("BGP-LS collector stopping")
	return c.srv.StopBgp(context.Background(), &api.StopBgpRequest{})
}

// addPeer configures a single BGP-LS peering session.
func (c *Collector) addPeer(ctx context.Context, peer config.BGPPeerConfig, family *api.Family) error {
	n := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: peer.Addr,
			PeerAsn:         peer.AS,
			Description:     peer.Description,
		},
		Timers: &api.Timers{
			Config: &api.TimersConfig{
				HoldTime: uint64(peer.HoldTime.Seconds()),
			},
		},
		AfiSafis: []*api.AfiSafi{
			{Config: &api.AfiSafiConfig{Family: family}},
		},
	}
	if peer.AuthPassword != "" {
		n.Conf.AuthPassword = peer.AuthPassword
	}
	if err := c.srv.AddPeer(ctx, &api.AddPeerRequest{Peer: n}); err != nil {
		return err
	}
	log.Info().Str("peer", peer.Addr).Uint32("as", peer.AS).Msg("BGP-LS peer added")
	return nil
}

// handlePath processes a single BGP-LS path update.
func (c *Collector) handlePath(path *api.Path) {
	if path == nil || path.Nlri == nil {
		return
	}

	// Probe NLRI type via TypeUrl
	var probe anypb.Any
	if err := path.Nlri.UnmarshalTo(&probe); err != nil {
		// Not an Any — try direct unmarshal into each known type
	}

	// Try node NLRI
	lsNode := &api.LsNodeNLRI{}
	if err := path.Nlri.UnmarshalTo(lsNode); err == nil {
		c.handleNodeNLRI(lsNode, path)
		return
	}

	// Try link NLRI
	lsLink := &api.LsLinkNLRI{}
	if err := path.Nlri.UnmarshalTo(lsLink); err == nil {
		c.handleLinkNLRI(lsLink, path)
		return
	}

	// Try v6 prefix NLRI (SRv6 locators appear here)
	lsV6 := &api.LsPrefixV6NLRI{}
	if err := path.Nlri.UnmarshalTo(lsV6); err == nil {
		c.handlePrefixV6NLRI(lsV6, path)
		return
	}

	// v4 prefix (SR-MPLS node SIDs — logged at debug)
	lsV4 := &api.LsPrefixV4NLRI{}
	if err := path.Nlri.UnmarshalTo(lsV4); err == nil {
		c.handlePrefixV4NLRI(lsV4, path)
		return
	}

	log.Debug().Msg("BGP-LS: unrecognised NLRI type")
}

// ── Node NLRI ─────────────────────────────────────────────────────────────

func (c *Collector) handleNodeNLRI(lsNode *api.LsNodeNLRI, path *api.Path) {
	routerID := extractNodeRouterID(lsNode.GetLocalNode())
	if routerID == "" {
		return
	}

	if path.IsWithdraw {
		c.ted.DeleteNode(routerID)
		c.bus.PublishTopology(events.EvNodeDown, routerID, nil)
		log.Info().Str("router_id", routerID).Msg("BGP-LS: node withdrawn")
		return
	}

	// Fetch or create node
	node := c.ted.GetNode(routerID)
	if node == nil {
		node = &ted.Node{RouterID: routerID, Source: "bgp-ls"}
	}

	// Local node descriptor
	if desc := lsNode.GetLocalNode(); desc != nil {
		if desc.GetAsn() != 0 {
			node.ASN = desc.GetAsn()
		}
	}

	// Parse path attributes for node attributes
	for _, pattr := range path.Pattrs {
		lsAttr := &api.LsAttribute{}
		if err := pattr.UnmarshalTo(lsAttr); err != nil {
			continue
		}
		if n := lsAttr.GetNode(); n != nil {
			if n.GetName() != "" {
				node.Hostname = n.GetName()
			}
			// IS-IS area bytes → hex string
			if area := n.GetIsisArea(); len(area) > 0 {
				node.ISISAreaIDs = []string{fmt.Sprintf("%X", area)}
			}
			// SR capabilities
			if n.GetSrCapabilities() != nil {
				node.Capabilities.SRMPLSCapable = true
			}
			// SR algorithms encode flex-algo participation
			for _, algo := range n.GetSrAlgorithms() {
				if algo >= 128 {
					node.FlexAlgos = append(node.FlexAlgos, algo)
				}
			}
			// Router ID for management
			if v4 := n.GetLocalRouterId(); v4 != "" {
				if addr, err := netip.ParseAddr(v4); err == nil {
					node.ManagementIPv4 = addr
				}
			}
			if v6 := n.GetLocalRouterIdV6(); v6 != "" {
				if addr, err := netip.ParseAddr(v6); err == nil {
					node.ManagementIPv6 = addr
				}
			}
		}
	}

	c.ted.UpsertNode(node)
	c.bus.PublishTopology(events.EvNodeUp, routerID, map[string]interface{}{
		"hostname": node.Hostname,
		"srv6":     node.Capabilities.SRv6Capable,
		"usid":     node.Capabilities.SRv6USIDCapable,
	})
	log.Info().
		Str("router_id", routerID).
		Str("hostname", node.Hostname).
		Bool("srv6", node.Capabilities.SRv6Capable).
		Msg("BGP-LS: node upserted")
}

// ── Link NLRI ─────────────────────────────────────────────────────────────

func (c *Collector) handleLinkNLRI(lsLink *api.LsLinkNLRI, path *api.Path) {
	localID := extractNodeRouterID(lsLink.GetLocalNode())
	remoteID := extractNodeRouterID(lsLink.GetRemoteNode())
	if localID == "" || remoteID == "" {
		return
	}

	// Extract local interface IP from link descriptor (best-effort)
	localIP := netip.Addr{}
	if ld := lsLink.GetLinkDescriptor(); ld != nil {
		if v4 := ld.GetInterfaceAddrIpv4(); v4 != "" {
			localIP = parseAddr(v4)
		} else if v6 := ld.GetInterfaceAddrIpv6(); v6 != "" {
			localIP = parseAddr(v6)
		}
	}

	if path.IsWithdraw {
		c.ted.DeleteLink(localID, remoteID, localIP)
		c.bus.PublishTopology(events.EvLinkDown, localID+"|"+remoteID, nil)
		log.Debug().Str("local", localID).Str("remote", remoteID).Msg("BGP-LS: link withdrawn")
		return
	}

	link := &ted.Link{
		LocalNodeID:     localID,
		RemoteNodeID:    remoteID,
		LocalIP:         localIP,
		FlexAlgoMetrics: make(map[uint8]uint32),
	}

	for _, pattr := range path.Pattrs {
		lsAttr := &api.LsAttribute{}
		if err := pattr.UnmarshalTo(lsAttr); err != nil {
			continue
		}
		if l := lsAttr.GetLink(); l != nil {
			if m := l.GetDefaultTeMetric(); m != 0 {
				link.TEMetric = m
			}
			if m := l.GetIgpMetric(); m != 0 {
				link.IGPMetric = m
			}
			if bw := l.GetBandwidth(); bw != 0 {
				link.MaxBandwidth = uint64(bw)
			}
			if rbw := l.GetReservableBandwidth(); rbw != 0 {
				link.ReservedBandwidth = link.MaxBandwidth - uint64(rbw)
			}
			if ag := l.GetAdminGroup(); ag != 0 {
				link.AdminGroup = ag
			}
			link.SRLG = l.GetSrlgs()

			// Remote IP from attribute
			if r := l.GetRemoteRouterId(); r != "" {
				link.RemoteIP = parseAddr(r)
			} else if r := l.GetRemoteRouterIdV6(); r != "" {
				link.RemoteIP = parseAddr(r)
			}

			// SR adjacency SID (Adj-SID, will be SRv6 End.X in newer SRv6-capable peers)
			if adjSID := l.GetSrAdjacencySid(); adjSID != 0 {
				// SR-MPLS adj label — store as reference
				_ = adjSID
			}
		}
	}

	c.ted.UpsertLink(link)
	c.bus.PublishTopology(events.EvLinkUpdated, localID+"|"+remoteID, map[string]interface{}{
		"te_metric": link.TEMetric,
		"bandwidth": link.MaxBandwidth,
	})
	log.Debug().
		Str("local", localID).
		Str("remote", remoteID).
		Uint32("te_metric", link.TEMetric).
		Msg("BGP-LS: link upserted")
}

// ── Prefix NLRIs ──────────────────────────────────────────────────────────

// handlePrefixV6NLRI handles IPv6 prefixes. In IS-IS SRv6 networks these
// carry SRv6 locator prefixes with SRv6 TLVs in the opaque field.
// GoBGP v3.37 does not yet decode SRv6 locator TLVs into structured fields,
// so we parse the opaque bytes manually.
func (c *Collector) handlePrefixV6NLRI(nlri *api.LsPrefixV6NLRI, path *api.Path) {
	localID := extractNodeRouterID(nlri.GetLocalNode())
	if localID == "" {
		return
	}

	// The reachability prefixes from the prefix descriptor
	prefixes := []string{}
	if pd := nlri.GetPrefixDescriptor(); pd != nil {
		prefixes = pd.GetIpReachability()
	}
	if len(prefixes) == 0 {
		return
	}

	for _, pfxStr := range prefixes {
		prefix, err := netip.ParsePrefix(pfxStr)
		if err != nil {
			continue
		}
		// Only 128-bit masked prefixes that look like SRv6 locators (/24–/64)
		if !prefix.Addr().Is6() || prefix.Bits() > 64 {
			continue
		}

		if path.IsWithdraw {
			c.removeSRv6Locator(localID, prefix)
			continue
		}

		// Check path attributes for SRv6 annotation in opaque data
		// and SR prefix SID TLV
		loc := srv6.Locator{
			Prefix: prefix,
			Owner:  localID,
		}

		for _, pattr := range path.Pattrs {
			lsAttr := &api.LsAttribute{}
			if err := pattr.UnmarshalTo(lsAttr); err != nil {
				continue
			}
			if p := lsAttr.GetPrefix(); p != nil {
				// Opaque: may contain raw SRv6 locator TLV bytes
				if opaque := p.GetOpaque(); len(opaque) > 0 {
					c.parseSRv6LocatorOpaque(opaque, &loc)
				}
			}
		}

		// Mark SRv6 locator
		loc.IsUSID = loc.Structure.LBLen > 0 // uSID if structure is advertised
		c.upsertSRv6Locator(localID, loc)
	}
}

// handlePrefixV4NLRI processes SR-MPLS node SID prefixes (informational).
func (c *Collector) handlePrefixV4NLRI(nlri *api.LsPrefixV4NLRI, path *api.Path) {
	localID := extractNodeRouterID(nlri.GetLocalNode())
	if localID == "" {
		return
	}
	for _, pattr := range path.Pattrs {
		lsAttr := &api.LsAttribute{}
		if err := pattr.UnmarshalTo(lsAttr); err != nil {
			continue
		}
		if p := lsAttr.GetPrefix(); p != nil {
			if sid := p.GetSrPrefixSid(); sid != 0 {
				node := c.ted.GetNode(localID)
				if node == nil {
					node = &ted.Node{RouterID: localID, Source: "bgp-ls"}
				}
				node.Capabilities.SRMPLSCapable = true
				c.ted.UpsertNode(node)
				log.Debug().
					Str("router_id", localID).
					Uint32("node_sid", sid).
					Msg("BGP-LS: SR-MPLS node SID")
			}
		}
	}
}

// parseSRv6LocatorOpaque parses raw opaque bytes for SRv6 locator TLVs.
// IS-IS SRv6 Locator TLV (type 27, RFC 9252) — structure:
//
//	2 byte type, 1 byte length, 1 byte algorithm, 4 byte metric, 1 byte flags,
//	followed by sub-TLVs (SRv6 Locator structure, type 1),
//	which contain 4 bytes: LB-len, LN-len, Fun-len, Arg-len.
func (c *Collector) parseSRv6LocatorOpaque(data []byte, loc *srv6.Locator) {
	const (
		isisSRv6LocatorTLV   = 27 // IS-IS SRv6 Locator TLV type
		isisSRv6StructSubTLV = 1  // SRv6 Locator Structure sub-TLV type
	)
	offset := 0
	for offset+3 <= len(data) {
		tlvType := uint16(data[offset])<<8 | uint16(data[offset+1])
		tlvLen := int(data[offset+2])
		if offset+3+tlvLen > len(data) {
			break
		}
		body := data[offset+3 : offset+3+tlvLen]

		if tlvType == isisSRv6LocatorTLV && len(body) >= 6 {
			loc.Algorithm = body[0]
			// body[1-4]: metric
			if len(body) >= 5 {
				loc.Metric = uint32(body[1])<<24 | uint32(body[2])<<16 | uint32(body[3])<<8 | uint32(body[4])
			}
			flags := byte(0)
			if len(body) >= 6 {
				flags = body[5]
			}
			// B-flag (bit 7 of flags byte) = uSID locator
			if flags&0x80 != 0 {
				loc.IsUSID = true
				loc.USIDBlock = loc.Prefix
			}
			// Parse sub-TLVs
			subOffset := 6
			for subOffset+2 <= len(body) {
				subType := body[subOffset]
				subLen := int(body[subOffset+1])
				if subOffset+2+subLen > len(body) {
					break
				}
				if subType == isisSRv6StructSubTLV && subLen >= 4 {
					sb := body[subOffset+2:]
					loc.Structure = srv6.SIDStructure{
						LBLen:  sb[0],
						LNLen:  sb[1],
						FunLen: sb[2],
						ArgLen: sb[3],
					}
					loc.IsUSID = true
				}
				subOffset += 2 + subLen
			}
		}
		offset += 3 + tlvLen
	}
}

// upsertSRv6Locator adds or replaces a locator on a TED node.
func (c *Collector) upsertSRv6Locator(routerID string, loc srv6.Locator) {
	node := c.ted.GetNode(routerID)
	if node == nil {
		node = &ted.Node{RouterID: routerID, Source: "bgp-ls"}
	}
	node.Capabilities.SRv6Capable = true
	if loc.IsUSID {
		node.Capabilities.SRv6USIDCapable = true
	}
	found := false
	for i, existing := range node.SRv6Locators {
		if existing.Prefix == loc.Prefix {
			node.SRv6Locators[i] = loc
			found = true
			break
		}
	}
	if !found {
		node.SRv6Locators = append(node.SRv6Locators, loc)
	}
	c.ted.UpsertNode(node)
	log.Debug().
		Str("router_id", routerID).
		Str("locator", loc.Prefix.String()).
		Bool("usid", loc.IsUSID).
		Uint8("algo", loc.Algorithm).
		Msg("BGP-LS: SRv6 locator upserted")
}

// removeSRv6Locator removes a locator from its TED node.
func (c *Collector) removeSRv6Locator(routerID string, prefix netip.Prefix) {
	node := c.ted.GetNode(routerID)
	if node == nil {
		return
	}
	locs := node.SRv6Locators[:0]
	for _, l := range node.SRv6Locators {
		if l.Prefix != prefix {
			locs = append(locs, l)
		}
	}
	node.SRv6Locators = locs
	c.ted.UpsertNode(node)
}

// ── Helpers ────────────────────────────────────────────────────────────────

// extractNodeRouterID returns the most specific router identifier from a BGP-LS
// node descriptor. IS-IS peers use IgpRouterId; BGP peers use BgpRouterId.
func extractNodeRouterID(desc *api.LsNodeDescriptor) string {
	if desc == nil {
		return ""
	}
	if id := desc.GetIgpRouterId(); id != "" {
		return id
	}
	if id := desc.GetBgpRouterId(); id != "" {
		return id
	}
	return ""
}

// parseAddr converts a dotted-decimal or colon-hex string to a netip.Addr.
func parseAddr(s string) netip.Addr {
	if s == "" {
		return netip.Addr{}
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return addr
}

// ── Retry / Reconnect ──────────────────────────────────────────────────────

// RunWithRetry wraps Run, restarting on transient failure with exponential backoff.
func (c *Collector) RunWithRetry(ctx context.Context) {
	backoff := 5 * time.Second
	for {
		log.Info().Msg("BGP-LS collector starting")
		if err := c.Run(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error().Err(err).Dur("retry_in", backoff).Msg("BGP-LS collector failed, restarting")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				if backoff < 60*time.Second {
					backoff *= 2
				}
				// re-create the server for restart
				c.srv = gobgpapi.NewBgpServer()
			}
		}
	}
}
