import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import cytoscape, { Core, ElementDefinition } from 'cytoscape'

interface Node {
    router_id: string
    hostname: string
    capabilities?: { srv6_capable?: boolean; srv6_usid_capable?: boolean; srv6_msd?: number }
    srv6_locators?: any[]
    flex_algos?: number[]
}
interface Link {
    local_node_id: string
    remote_node_id: string
    te_metric: number
    igp_metric: number
    bandwidth: number
    latency: number
    local_ip: string
}

type MetricOverlay = 'te' | 'igp' | 'latency'

export default function TopologyPage() {
    const cyRef = useRef<HTMLDivElement>(null)
    const cyInstance = useRef<Core | null>(null)
    const [selected, setSelected] = useState<{ type: 'node' | 'link'; data: any } | null>(null)
    const [overlay, setOverlay] = useState<MetricOverlay>('te')

    const { data: topoData, isLoading } = useQuery({
        queryKey: ['topology'],
        queryFn: () => fetch('/api/v1/topology').then(r => r.json()),
        refetchInterval: 10000,
    })

    useEffect(() => {
        if (!cyRef.current || !topoData) return

        const nodes: Node[] = topoData.nodes || []
        const links: Link[] = topoData.links || []

        const elements: ElementDefinition[] = [
            ...nodes.map(n => ({
                group: 'nodes' as const,
                data: {
                    id: n.router_id,
                    label: n.hostname || n.router_id,
                    srv6: n.capabilities?.srv6_capable,
                    usid: n.capabilities?.srv6_usid_capable,
                    msd: n.capabilities?.srv6_msd,
                    locators: n.srv6_locators?.length || 0,
                    flexAlgos: n.flex_algos || [],
                    raw: n,
                },
            })),
            ...links.map((l, i) => ({
                group: 'edges' as const,
                data: {
                    id: `link-${i}`,
                    source: l.local_node_id,
                    target: l.remote_node_id,
                    te: l.te_metric,
                    igp: l.igp_metric,
                    latency: l.latency,
                    bw: l.bandwidth,
                    localIp: l.local_ip,
                    label: overlay === 'te' ? `${l.te_metric}` : overlay === 'igp' ? `${l.igp_metric}` : `${l.latency}µs`,
                    raw: l,
                },
            })),
        ]

        if (cyInstance.current) {
            cyInstance.current.destroy()
        }

        const cy = cytoscape({
            container: cyRef.current,
            elements,
            style: [
                {
                    selector: 'node',
                    style: {
                        'background-color': '#0f1525',
                        'border-width': 2,
                        'border-color': '#1e2d4a',
                        'label': 'data(label)',
                        'font-family': 'Inter, sans-serif',
                        'font-size': 11,
                        'color': '#94a3b8',
                        'text-valign': 'bottom',
                        'text-margin-y': 6,
                        'width': 32,
                        'height': 32,
                        'text-outline-width': 0,
                    } as any,
                },
                {
                    selector: 'node[?srv6]',
                    style: {
                        'background-color': '#0d2040',
                        'border-color': '#00d4ff',
                        'border-width': 2,
                        'color': '#00d4ff',
                    } as any,
                },
                {
                    selector: 'node[?usid]',
                    style: {
                        'background-color': '#102040',
                        'border-color': '#10d98e',
                        'border-width': 2.5,
                        'color': '#10d98e',
                    } as any,
                },
                {
                    selector: 'node:selected',
                    style: {
                        'border-color': '#fff',
                        'border-width': 3,
                        'background-color': '#1a2d4a',
                    } as any,
                },
                {
                    selector: 'edge',
                    style: {
                        'line-color': '#1e2d4a',
                        'width': 1.5,
                        'curve-style': 'bezier',
                        'target-arrow-shape': 'none',
                        'label': 'data(label)',
                        'font-size': 9,
                        'color': '#475569',
                        'text-background-color': '#0a0e1a',
                        'text-background-opacity': 0.8,
                        'text-background-padding': '2px',
                        'font-family': 'JetBrains Mono, monospace',
                    } as any,
                },
                {
                    selector: 'edge:selected',
                    style: {
                        'line-color': '#00d4ff',
                        'width': 2.5,
                    } as any,
                },
            ],
            layout: {
                name: 'cose',
                animate: false,
                nodeRepulsion: () => 8000,
                idealEdgeLength: () => 120,
                padding: 40,
            } as any,
        })

        cy.on('tap', 'node', (evt) => {
            setSelected({ type: 'node', data: evt.target.data('raw') })
        })
        cy.on('tap', 'edge', (evt) => {
            setSelected({ type: 'link', data: evt.target.data('raw') })
        })
        cy.on('tap', (evt) => {
            if (evt.target === cy) setSelected(null)
        })

        cyInstance.current = cy
    }, [topoData, overlay])

    const handleFit = () => cyInstance.current?.fit()
    const handleZoomIn = () => cyInstance.current?.zoom(cyInstance.current.zoom() * 1.3)
    const handleZoomOut = () => cyInstance.current?.zoom(cyInstance.current.zoom() * 0.77)

    return (
        <>
            <div className="page-header">
                <div>
                    <div className="page-title">Network Topology</div>
                    <div className="page-subtitle">
                        BGP-LS TED · {topoData?.meta?.node_count || 0} nodes · {topoData?.meta?.link_count || 0} links
                    </div>
                </div>
                <div className="page-actions">
                    <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Metric overlay:</span>
                    {(['te', 'igp', 'latency'] as MetricOverlay[]).map(m => (
                        <button
                            key={m}
                            className={`btn btn-sm ${overlay === m ? 'btn-primary' : 'btn-ghost'}`}
                            onClick={() => setOverlay(m)}
                        >
                            {m.toUpperCase()}
                        </button>
                    ))}
                </div>
            </div>

            <div style={{ flex: 1, display: 'flex', overflow: 'hidden', padding: '16px', gap: 12, minHeight: 0 }}>
                <div className="topo-container" style={{ flex: 1 }}>
                    {isLoading && (
                        <div className="loading-center">
                            <div className="spinner" />
                            Loading topology...
                        </div>
                    )}
                    <div ref={cyRef} id="cy" style={{ width: '100%', height: '100%' }} />
                    <div className="topo-controls">
                        <button className="btn btn-ghost btn-sm" onClick={handleZoomIn} title="Zoom in">+</button>
                        <button className="btn btn-ghost btn-sm" onClick={handleZoomOut} title="Zoom out">−</button>
                        <button className="btn btn-ghost btn-sm" onClick={handleFit} title="Fit view">⊡</button>
                    </div>
                    <div className="topo-legend">
                        <div className="legend-item">
                            <div className="legend-dot" style={{ background: '#00d4ff' }} />
                            SRv6 capable
                        </div>
                        <div className="legend-item">
                            <div className="legend-dot" style={{ background: '#10d98e' }} />
                            SRv6 + uSID
                        </div>
                        <div className="legend-item">
                            <div className="legend-dot" style={{ background: '#1e2d4a' }} />
                            Non-SRv6
                        </div>
                    </div>
                </div>

                {selected && (
                    <div className="side-panel">
                        <div className="panel-header">
                            <span className="panel-title">
                                {selected.type === 'node' ? '◈ Node Detail' : '⇌ Link Detail'}
                            </span>
                            <button className="panel-close" onClick={() => setSelected(null)}>✕</button>
                        </div>
                        <div className="panel-body">
                            {selected.type === 'node' ? (
                                <NodeDetail node={selected.data} />
                            ) : (
                                <LinkDetail link={selected.data} />
                            )}
                        </div>
                    </div>
                )}
            </div>
        </>
    )
}

function NodeDetail({ node }: { node: Node }) {
    return (
        <div>
            <div className="prop-row">
                <span className="prop-label">Router ID</span>
                <span className="prop-value">{node.router_id}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">Hostname</span>
                <span className="prop-value">{node.hostname || '—'}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">SRv6</span>
                <span className="prop-value">
                    {node.capabilities?.srv6_capable
                        ? <span className="text-green">✓ Capable</span>
                        : <span className="text-muted">Not capable</span>}
                </span>
            </div>
            <div className="prop-row">
                <span className="prop-label">uSID</span>
                <span className="prop-value">
                    {node.capabilities?.srv6_usid_capable
                        ? <span className="text-green">✓ Supported</span>
                        : <span className="text-muted">—</span>}
                </span>
            </div>
            <div className="prop-row">
                <span className="prop-label">SRv6 MSD</span>
                <span className="prop-value">{node.capabilities?.srv6_msd || '—'}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">Flex-Algos</span>
                <span className="prop-value">
                    {node.flex_algos?.map(a => (
                        <span key={a} className="chip" style={{ marginLeft: 4 }}>{a}</span>
                    )) || '—'}
                </span>
            </div>

            {node.srv6_locators && node.srv6_locators.length > 0 && (
                <div style={{ marginTop: 16 }}>
                    <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 8 }}>
                        SRv6 Locators
                    </div>
                    {node.srv6_locators.map((loc: any, i: number) => (
                        <div key={i} style={{
                            background: 'var(--bg-base)', border: '1px solid var(--border)',
                            borderRadius: 8, padding: '10px 12px', marginBottom: 8
                        }}>
                            <div className="mono" style={{ color: 'var(--accent)', fontSize: 12, marginBottom: 4 }}>
                                {loc.prefix}
                            </div>
                            <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                                algo={loc.algorithm || 0} · metric={loc.metric || 0}
                                {loc.is_usid && <span className="badge badge-green" style={{ marginLeft: 6, fontSize: 10 }}>uSID</span>}
                            </div>
                        </div>
                    ))}
                </div>
            )}
        </div>
    )
}

function LinkDetail({ link }: { link: Link }) {
    return (
        <div>
            <div className="prop-row">
                <span className="prop-label">Local</span>
                <span className="prop-value">{link.local_node_id}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">Local IP</span>
                <span className="prop-value">{link.local_ip || '—'}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">Remote</span>
                <span className="prop-value">{link.remote_node_id}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">TE Metric</span>
                <span className="prop-value text-accent">{link.te_metric}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">IGP Metric</span>
                <span className="prop-value">{link.igp_metric}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">Latency</span>
                <span className="prop-value">{link.latency ? `${link.latency} µs` : '—'}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">Max BW</span>
                <span className="prop-value">{formatBW(link.bandwidth)}</span>
            </div>
        </div>
    )
}

function formatBW(bw: number): string {
    if (!bw) return '—'
    if (bw >= 1e9) return `${(bw / 1e9).toFixed(1)} Gbps`
    if (bw >= 1e6) return `${(bw / 1e6).toFixed(1)} Mbps`
    return `${bw} bps`
}
