import { useQuery } from '@tanstack/react-query'

interface HealthData { status: string; ted_nodes: number; ted_links: number }
interface LSP { id: string; name: string; status: string; sr_type: string }
interface Session { id: string; state: string; peer_addr: string }

function StatCard({ label, value, sub, color = '' }: { label: string; value: string | number; sub?: string; color?: string }) {
    return (
        <div className="stat-card">
            <div className="stat-label">{label}</div>
            <div className={`stat-value ${color}`}>{value}</div>
            {sub && <div className="stat-sub">{sub}</div>}
        </div>
    )
}

export default function Dashboard() {
    const { data: health } = useQuery<HealthData>({
        queryKey: ['health'],
        queryFn: () => fetch('/api/v1/health').then(r => r.json()),
        refetchInterval: 3000,
    })
    const { data: lsps = [] } = useQuery<LSP[]>({
        queryKey: ['lsps'],
        queryFn: () => fetch('/api/v1/lsps').then(r => r.json()),
    })
    const { data: sessions = [] } = useQuery<Session[]>({
        queryKey: ['sessions'],
        queryFn: () => fetch('/api/v1/sessions').then(r => r.json()),
    })
    const { data: nodes = [] } = useQuery<any[]>({
        queryKey: ['nodes'],
        queryFn: () => fetch('/api/v1/topology/nodes').then(r => r.json()),
    })

    const activeLSPs = (lsps || []).filter((l: LSP) => l.status === 'active').length
    const srv6LSPs = (lsps || []).filter((l: LSP) => l.sr_type === 'srv6').length
    const upSessions = (sessions || []).filter((s: Session) => s.state === 'UP').length
    const srv6Nodes = (nodes || []).filter((n: any) => n.capabilities?.srv6_capable).length

    return (
        <>
            <div className="page-header">
                <div>
                    <div className="page-title">Dashboard</div>
                    <div className="page-subtitle">SRv6/SR-MPLS Stateful PCE overview</div>
                </div>
                <div className="page-actions">
                    <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
                        Auto-refresh every 5s
                    </span>
                </div>
            </div>
            <div className="page-body">
                <div className="stats-grid">
                    <StatCard label="TED Nodes" value={health?.ted_nodes ?? 0} sub="Topology aware" color="accent" />
                    <StatCard label="TED Links" value={health?.ted_links ?? 0} sub="TE database" />
                    <StatCard label="SRv6 Nodes" value={srv6Nodes} sub="Segment capable" color="green" />
                    <StatCard label="Total LSPs" value={(lsps || []).length} sub={`${srv6LSPs} SRv6, ${(lsps || []).length - srv6LSPs} MPLS`} />
                    <StatCard label="Active LSPs" value={activeLSPs} color={activeLSPs > 0 ? 'green' : ''} />
                    <StatCard label="PCEP Sessions" value={upSessions} sub="Active sessions" color={upSessions > 0 ? 'green' : ''} />
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
                    {/* Recent LSPs */}
                    <div className="card">
                        <div className="card-header">
                            <span className="card-title">Recent LSPs</span>
                            <a href="/lsps" style={{ fontSize: 12, color: 'var(--accent)', textDecoration: 'none' }}>View all →</a>
                        </div>
                        <div className="table-container">
                            <table>
                                <thead>
                                    <tr>
                                        <th>Name</th>
                                        <th>Type</th>
                                        <th>Status</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(lsps || []).slice(0, 6).map((l: LSP) => (
                                        <tr key={l.id}>
                                            <td className="mono">{l.name || l.id.slice(0, 8)}</td>
                                            <td><span className="badge badge-blue">{l.sr_type?.toUpperCase()}</span></td>
                                            <td><StatusBadge status={l.status} /></td>
                                        </tr>
                                    ))}
                                    {(lsps || []).length === 0 && (
                                        <tr><td colSpan={3} style={{ textAlign: 'center', color: 'var(--text-muted)', padding: '20px 0' }}>No LSPs</td></tr>
                                    )}
                                </tbody>
                            </table>
                        </div>
                    </div>

                    {/* PCEP Sessions */}
                    <div className="card">
                        <div className="card-header">
                            <span className="card-title">PCEP Sessions</span>
                            <a href="/sessions" style={{ fontSize: 12, color: 'var(--accent)', textDecoration: 'none' }}>View all →</a>
                        </div>
                        <div className="table-container">
                            <table>
                                <thead>
                                    <tr>
                                        <th>PCC</th>
                                        <th>State</th>
                                        <th>SRv6</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(sessions || []).slice(0, 6).map((s: Session & any) => (
                                        <tr key={s.id}>
                                            <td className="mono">{s.peer_addr}</td>
                                            <td>
                                                <span className={`badge ${s.state === 'UP' ? 'badge-green' : 'badge-red'}`}>
                                                    <span className="status-dot" />{s.state}
                                                </span>
                                            </td>
                                            <td>{s.capabilities?.srv6_capable ? '✓' : '—'}</td>
                                        </tr>
                                    ))}
                                    {(sessions || []).length === 0 && (
                                        <tr><td colSpan={3} style={{ textAlign: 'center', color: 'var(--text-muted)', padding: '20px 0' }}>No sessions</td></tr>
                                    )}
                                </tbody>
                            </table>
                        </div>
                    </div>
                </div>

                {/* SRv6 Node Summary */}
                <div className="card">
                    <div className="card-header">
                        <span className="card-title">SRv6 Node Inventory</span>
                        <a href="/topology" style={{ fontSize: 12, color: 'var(--accent)', textDecoration: 'none' }}>Open topology →</a>
                    </div>
                    <div className="table-container">
                        <table>
                            <thead>
                                <tr>
                                    <th>Router ID</th>
                                    <th>Hostname</th>
                                    <th>SRv6</th>
                                    <th>uSID</th>
                                    <th>SRv6 MSD</th>
                                    <th>Locators</th>
                                    <th>Flex-Algos</th>
                                </tr>
                            </thead>
                            <tbody>
                                {(nodes || []).slice(0, 10).map((n: any) => (
                                    <tr key={n.router_id}>
                                        <td className="mono text-accent">{n.router_id}</td>
                                        <td>{n.hostname || '—'}</td>
                                        <td>{n.capabilities?.srv6_capable ? <span className="badge badge-green">✓ Yes</span> : <span className="badge badge-gray">No</span>}</td>
                                        <td>{n.capabilities?.srv6_usid_capable ? <span className="text-green">✓</span> : <span className="text-muted">—</span>}</td>
                                        <td className="mono">{n.capabilities?.srv6_msd || '—'}</td>
                                        <td className="mono">{n.srv6_locators?.length || 0}</td>
                                        <td>{(n.flex_algos || []).map((a: number) => (
                                            <span key={a} className="chip" style={{ marginRight: 4 }}>{a}</span>
                                        ))}</td>
                                    </tr>
                                ))}
                                {(nodes || []).length === 0 && (
                                    <tr><td colSpan={7} style={{ textAlign: 'center', color: 'var(--text-muted)', padding: '20px 0' }}>No nodes in TED — waiting for BGP-LS</td></tr>
                                )}
                            </tbody>
                        </table>
                    </div>
                </div>
            </div>
        </>
    )
}

function StatusBadge({ status }: { status: string }) {
    const map: Record<string, string> = {
        active: 'badge-green',
        down: 'badge-red',
        delegated: 'badge-blue',
        reported: 'badge-gray',
        pending: 'badge-yellow',
    }
    return (
        <span className={`badge ${map[status] || 'badge-gray'}`}>
            <span className="status-dot" />{status}
        </span>
    )
}
