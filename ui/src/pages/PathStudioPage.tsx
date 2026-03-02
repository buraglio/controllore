import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'

interface ComputeResult {
    node_hops: string[]
    segment_list: any[]
    cost: number
    metric_type: string
    sid_count: number
}

export default function PathStudioPage() {
    const [form, setForm] = useState({
        src: '', dst: '',
        metric_type: 1, use_usid: false,
        flex_algo: 0, min_bandwidth: '',
        avoid_srlg: '',
    })
    const [result, setResult] = useState<ComputeResult | null>(null)
    const [error, setError] = useState('')
    const [instantiated, setInstantiated] = useState(false)

    const { data: nodes = [] } = useQuery<any[]>({
        queryKey: ['nodes'],
        queryFn: () => fetch('/api/v1/topology/nodes').then(r => r.json()),
    })

    const computeMut = useMutation({
        mutationFn: (body: any) => fetch('/api/v1/paths/compute', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        }).then(async r => {
            if (!r.ok) throw new Error((await r.json()).error || 'Computation failed')
            return r.json() as Promise<ComputeResult>
        }),
        onSuccess: data => { setResult(data); setError(''); setInstantiated(false) },
        onError: (e: any) => { setError(e.message); setResult(null) },
    })

    const instantiateMut = useMutation({
        mutationFn: () => fetch('/api/v1/lsps', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                src: form.src, dst: form.dst, sr_type: 'srv6',
                constraints: {
                    metric_type: form.metric_type,
                    flex_algo: form.flex_algo,
                    use_usid: form.use_usid,
                    min_bandwidth: form.min_bandwidth ? parseInt(form.min_bandwidth) : 0,
                },
            }),
        }).then(async r => { if (!r.ok) throw new Error((await r.json()).error); return r.json() }),
        onSuccess: () => setInstantiated(true),
        onError: (e: any) => setError(e.message),
    })

    const handleCompute = (e: React.FormEvent) => {
        e.preventDefault()
        setError('')
        computeMut.mutate({
            src: form.src, dst: form.dst,
            constraints: {
                metric_type: form.metric_type,
                use_usid: form.use_usid,
                flex_algo: form.flex_algo,
                min_bandwidth: form.min_bandwidth ? parseInt(form.min_bandwidth) : 0,
                avoid_srlg: form.avoid_srlg
                    ? form.avoid_srlg.split(',').map(s => parseInt(s.trim())).filter(Boolean)
                    : [],
            },
        })
    }

    const metricLabels = ['IGP', 'TE', 'Latency', 'Hop Count']

    return (
        <>
            <div className="page-header">
                <div>
                    <div className="page-title">Path Studio</div>
                    <div className="page-subtitle">Compute SRv6 paths with CSPF constraints — preview before instantiating</div>
                </div>
            </div>
            <div style={{ flex: 1, display: 'flex', gap: 16, padding: 16, overflow: 'hidden', minHeight: 0 }}>
                {/* Control panel */}
                <div style={{ width: 340, flexShrink: 0, display: 'flex', flexDirection: 'column', gap: 14 }}>
                    <form onSubmit={handleCompute} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                        <div className="card">
                            <div className="card-header"><span className="card-title">Endpoints</span></div>
                            <div className="card-body" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                                <div className="form-group">
                                    <label className="form-label">Source Router ID</label>
                                    <input list="node-list" className="form-input" placeholder="192.0.2.1" required
                                        value={form.src} onChange={e => setForm({ ...form, src: e.target.value })} />
                                </div>
                                <div className="form-group">
                                    <label className="form-label">Destination Router ID</label>
                                    <input list="node-list" className="form-input" placeholder="192.0.2.2" required
                                        value={form.dst} onChange={e => setForm({ ...form, dst: e.target.value })} />
                                </div>
                                <datalist id="node-list">
                                    {(nodes || []).map((n: any) => <option key={n.router_id} value={n.router_id}>{n.hostname}</option>)}
                                </datalist>
                            </div>
                        </div>

                        <div className="card">
                            <div className="card-header"><span className="card-title">Constraints</span></div>
                            <div className="card-body" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                                <div className="form-group">
                                    <label className="form-label">Optimize Metric</label>
                                    <select className="form-select" value={form.metric_type}
                                        onChange={e => setForm({ ...form, metric_type: parseInt(e.target.value) })}>
                                        <option value={0}>IGP Metric</option>
                                        <option value={1}>TE Metric</option>
                                        <option value={2}>Latency</option>
                                        <option value={3}>Hop Count</option>
                                    </select>
                                </div>
                                <div className="form-group">
                                    <label className="form-label">Flex-Algorithm</label>
                                    <input type="number" className="form-input" min={0} max={255}
                                        placeholder="0 = default SPF"
                                        value={form.flex_algo} onChange={e => setForm({ ...form, flex_algo: parseInt(e.target.value) || 0 })} />
                                </div>
                                <div className="form-group">
                                    <label className="form-label">Min Bandwidth (B/s)</label>
                                    <input type="number" className="form-input" placeholder="0 = unconstrained"
                                        value={form.min_bandwidth} onChange={e => setForm({ ...form, min_bandwidth: e.target.value })} />
                                </div>
                                <div className="form-group">
                                    <label className="form-label">Avoid SRLG (comma-separated IDs)</label>
                                    <input className="form-input" placeholder="101,202"
                                        value={form.avoid_srlg} onChange={e => setForm({ ...form, avoid_srlg: e.target.value })} />
                                </div>
                                <label className="form-check">
                                    <input type="checkbox" checked={form.use_usid}
                                        onChange={e => setForm({ ...form, use_usid: e.target.checked })} />
                                    <span>SRv6 uSID compression</span>
                                </label>
                            </div>
                        </div>

                        <button type="submit" className="btn btn-primary" disabled={computeMut.isPending} style={{ width: '100%' }}>
                            {computeMut.isPending ? 'Computing…' : '⊕ Compute Path'}
                        </button>
                    </form>
                </div>

                {/* Results */}
                <div style={{ flex: 1, overflow: 'auto', display: 'flex', flexDirection: 'column', gap: 14 }}>
                    {error && (
                        <div style={{ background: 'var(--red-soft)', border: '1px solid var(--red)', borderRadius: 8, padding: '12px 16px', color: 'var(--red)' }}>
                            {error}
                        </div>
                    )}

                    {result && (
                        <>
                            {/* Summary */}
                            <div className="stats-grid" style={{ gridTemplateColumns: 'repeat(4, 1fr)' }}>
                                <div className="stat-card">
                                    <div className="stat-label">Path Cost</div>
                                    <div className="stat-value accent">{result.cost}</div>
                                    <div className="stat-sub">{result.metric_type}</div>
                                </div>
                                <div className="stat-card">
                                    <div className="stat-label">SID Count</div>
                                    <div className="stat-value">{result.sid_count}</div>
                                </div>
                                <div className="stat-card">
                                    <div className="stat-label">Hops</div>
                                    <div className="stat-value">{result.node_hops?.length || 0}</div>
                                </div>
                                <div className="stat-card">
                                    <div className="stat-label">Metric</div>
                                    <div className="stat-value" style={{ fontSize: 18 }}>{result.metric_type?.toUpperCase()}</div>
                                </div>
                            </div>

                            {/* Node Hop Path */}
                            <div className="card">
                                <div className="card-header"><span className="card-title">Node Hop Sequence</span></div>
                                <div className="card-body">
                                    <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
                                        {result.node_hops?.map((h, i) => (
                                            <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                                                <div style={{
                                                    background: i === 0 || i === result.node_hops.length - 1 ? 'var(--accent-soft)' : 'var(--bg-hover)',
                                                    border: `1px solid ${i === 0 || i === result.node_hops.length - 1 ? 'var(--accent)' : 'var(--border)'}`,
                                                    borderRadius: 8, padding: '6px 12px',
                                                    fontFamily: 'JetBrains Mono, monospace', fontSize: 12,
                                                    color: i === 0 || i === result.node_hops.length - 1 ? 'var(--accent)' : 'var(--text-primary)',
                                                }}>
                                                    {h}
                                                </div>
                                                {i < result.node_hops.length - 1 && (
                                                    <span style={{ color: 'var(--text-muted)' }}>→</span>
                                                )}
                                            </div>
                                        ))}
                                    </div>
                                </div>
                            </div>

                            {/* Segment List */}
                            <div className="card">
                                <div className="card-header">
                                    <span className="card-title">SRv6 Segment List</span>
                                    <span className="badge badge-blue">{result.segment_list?.length || 0} SIDs</span>
                                </div>
                                <div className="card-body">
                                    <div className="segment-list">
                                        {result.segment_list?.map((s: any, i: number) => (
                                            <div key={i} className="segment-item">
                                                <div className="segment-index">{i + 1}</div>
                                                <div style={{ display: 'flex', flexDirection: 'column', flex: 1 }}>
                                                    <div className="segment-sid">{s.addr || s.sid || '—'}</div>
                                                    {s.nai && <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>NAI: {s.nai}</div>}
                                                    {s.owner && <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>Owner: {s.owner}</div>}
                                                </div>
                                                <div className="segment-behavior">{s.behavior?.toString() || s.type}</div>
                                            </div>
                                        ))}
                                        {(!result.segment_list || result.segment_list.length === 0) && (
                                            <div style={{ color: 'var(--text-muted)', textAlign: 'center', padding: '16px 0' }}>
                                                No SIDs — TED may lack SRv6 locators
                                            </div>
                                        )}
                                    </div>
                                </div>
                            </div>

                            {/* Instantiate */}
                            {!instantiated ? (
                                <button className="btn btn-primary" style={{ alignSelf: 'flex-start' }}
                                    onClick={() => instantiateMut.mutate()}
                                    disabled={instantiateMut.isPending}>
                                    {instantiateMut.isPending ? 'Instantiating…' : '⇌ Instantiate as LSP (PCInitiate)'}
                                </button>
                            ) : (
                                <div style={{ color: 'var(--green)', fontSize: 13, display: 'flex', alignItems: 'center', gap: 8 }}>
                                    ✓ LSP instantiated via PCInitiate — check the LSPs page
                                </div>
                            )}
                        </>
                    )}

                    {!result && !error && !computeMut.isPending && (
                        <div style={{
                            flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center',
                            color: 'var(--text-muted)', flexDirection: 'column', gap: 12
                        }}>
                            <div style={{ fontSize: 48, opacity: 0.2 }}>⊕</div>
                            <div style={{ fontSize: 14 }}>Configure endpoints and constraints, then compute a path</div>
                        </div>
                    )}
                </div>
            </div>
        </>
    )
}
