import { useQuery } from '@tanstack/react-query'

export default function SessionsPage() {
    const { data: sessions = [], isLoading } = useQuery<any[]>({
        queryKey: ['sessions'],
        queryFn: () => fetch('/api/v1/sessions').then(r => r.json()),
        refetchInterval: 5000,
    })

    return (
        <>
            <div className="page-header">
                <div>
                    <div className="page-title">PCEP Sessions</div>
                    <div className="page-subtitle">{(sessions || []).length} PCC session(s) · RFC 8231 Stateful</div>
                </div>
            </div>
            <div className="page-body">
                {isLoading ? (
                    <div className="loading-center"><div className="spinner" />Loading...</div>
                ) : (
                    <div className="card">
                        <div className="table-container">
                            <table>
                                <thead>
                                    <tr>
                                        <th>Session ID</th>
                                        <th>PCC Address</th>
                                        <th>State</th>
                                        <th>SRv6</th>
                                        <th>uSID</th>
                                        <th>SRv6 MSD</th>
                                        <th>Stateful</th>
                                        <th>PCE-Initiate</th>
                                        <th>Keepalive</th>
                                        <th>Msg RX</th>
                                        <th>Msg TX</th>
                                        <th>Up Since</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(sessions || []).map((s: any) => (
                                        <tr key={s.id}>
                                            <td className="mono text-muted" style={{ fontSize: 10 }}>{s.id?.slice(0, 12)}…</td>
                                            <td className="mono text-accent">{s.peer_addr}</td>
                                            <td>
                                                <span className={`badge ${s.state === 'UP' ? 'badge-green' : s.state === 'OPENING' ? 'badge-yellow' : 'badge-red'}`}>
                                                    <span className="status-dot" />{s.state}
                                                </span>
                                            </td>
                                            <td>{s.capabilities?.srv6_capable ? <span className="text-green">✓</span> : '—'}</td>
                                            <td>{s.capabilities?.srv6_usid_capable ? <span className="text-green">✓</span> : '—'}</td>
                                            <td className="mono">{s.capabilities?.srv6_msd || '—'}</td>
                                            <td>{s.capabilities?.stateful ? <span className="text-green">✓</span> : '—'}</td>
                                            <td>{s.capabilities?.lsp_instantiate ? <span className="text-green">✓</span> : '—'}</td>
                                            <td className="mono">{s.keepalive}s</td>
                                            <td className="mono">{s.msgs_rx}</td>
                                            <td className="mono">{s.msgs_tx}</td>
                                            <td style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                                                {s.established_at ? new Date(s.established_at).toLocaleString() : '—'}
                                            </td>
                                        </tr>
                                    ))}
                                    {(sessions || []).length === 0 && (
                                        <tr>
                                            <td colSpan={12} style={{ textAlign: 'center', color: 'var(--text-muted)', padding: '40px 0' }}>
                                                No PCEP sessions — waiting for PCC connections on port 4189
                                            </td>
                                        </tr>
                                    )}
                                </tbody>
                            </table>
                        </div>
                    </div>
                )}

                {/* Protocol info */}
                <div className="card">
                    <div className="card-header"><span className="card-title">PCEP Protocol Reference</span></div>
                    <div className="card-body">
                        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
                            {[
                                { title: 'RFC 5440', desc: 'PCEP base protocol' },
                                { title: 'RFC 8231', desc: 'Stateful PCE extensions' },
                                { title: 'RFC 8281', desc: 'PCE-Initiated LSPs (PCInitiate)' },
                                { title: 'RFC 8664', desc: 'SR-TE PCEP extensions' },
                                { title: 'RFC 8253', desc: 'PCEP-TLS (optional)' },
                                { title: 'draft SRv6', desc: 'SRv6 ERO subobject, SRv6 MSD' },
                            ].map(r => (
                                <div key={r.title} style={{ background: 'var(--bg-base)', borderRadius: 8, padding: '10px 14px', border: '1px solid var(--border)' }}>
                                    <div style={{ fontWeight: 700, color: 'var(--accent)', fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>{r.title}</div>
                                    <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 3 }}>{r.desc}</div>
                                </div>
                            ))}
                        </div>
                    </div>
                </div>
            </div>
        </>
    )
}
