import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'

interface LSP {
    id: string
    name: string
    pcc: string
    src: string
    dst: string
    sr_type: string
    status: string
    bsid?: string
    segment_list: any[]
    computed_metric: number
    constraints: any
    created_at: string
}

function StatusBadge({ status }: { status: string }) {
    const map: Record<string, string> = {
        active: 'badge-green', down: 'badge-red',
        delegated: 'badge-blue', reported: 'badge-gray', pending: 'badge-yellow',
    }
    return (
        <span className={`badge ${map[status] || 'badge-gray'}`}>
            <span className="status-dot" />{status}
        </span>
    )
}

function MetricBadge({ type }: { type: number }) {
    const names = ['IGP', 'TE', 'Latency', 'Hopcount']
    return <span className="chip">{names[type] || 'Unknown'}</span>
}

export default function LSPsPage() {
    const qc = useQueryClient()
    const [typeFilter, setTypeFilter] = useState('')
    const [statusFilter, setStatusFilter] = useState('')
    const [selected, setSelected] = useState<LSP | null>(null)
    const [showCreate, setShowCreate] = useState(false)

    const { data: lsps = [], isLoading } = useQuery<LSP[]>({
        queryKey: ['lsps', typeFilter],
        queryFn: () => {
            const q = typeFilter ? `?type=${typeFilter}` : ''
            return fetch(`/api/v1/lsps${q}`).then(r => r.json())
        },
        refetchInterval: 5000,
    })

    const deleteMut = useMutation({
        mutationFn: (id: string) => fetch(`/api/v1/lsps/${id}`, { method: 'DELETE' }),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['lsps'] }),
    })

    const filtered = (lsps || []).filter(l => !statusFilter || l.status === statusFilter)

    return (
        <>
            <div className="page-header">
                <div>
                    <div className="page-title">LSP Management</div>
                    <div className="page-subtitle">{filtered.length} LSPs · PCE-controlled SR policies</div>
                </div>
                <div className="page-actions">
                    <select className="form-select" style={{ width: 120 }} value={typeFilter} onChange={e => setTypeFilter(e.target.value)}>
                        <option value="">All Types</option>
                        <option value="srv6">SRv6</option>
                        <option value="mpls">MPLS</option>
                    </select>
                    <select className="form-select" style={{ width: 130 }} value={statusFilter} onChange={e => setStatusFilter(e.target.value)}>
                        <option value="">All Status</option>
                        <option value="active">Active</option>
                        <option value="down">Down</option>
                        <option value="pending">Pending</option>
                        <option value="delegated">Delegated</option>
                    </select>
                    <button className="btn btn-primary" onClick={() => setShowCreate(true)}>+ Create LSP</button>
                </div>
            </div>

            <div style={{ flex: 1, display: 'flex', overflow: 'hidden', minHeight: 0 }}>
                <div style={{ flex: 1, overflow: 'auto', padding: '16px' }}>
                    {isLoading ? (
                        <div className="loading-center"><div className="spinner" />Loading LSPs...</div>
                    ) : (
                        <div className="card">
                            <div className="table-container">
                                <table>
                                    <thead>
                                        <tr>
                                            <th>Name / ID</th>
                                            <th>PCC</th>
                                            <th>Source</th>
                                            <th>Destination</th>
                                            <th>Type</th>
                                            <th>Status</th>
                                            <th>Metric</th>
                                            <th>SIDs</th>
                                            <th>Cost</th>
                                            <th></th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {filtered.map(l => (
                                            <tr key={l.id} onClick={() => setSelected(l)}>
                                                <td>
                                                    <div style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{l.name || '—'}</div>
                                                    <div className="mono text-muted" style={{ fontSize: 11 }}>{l.id.slice(0, 12)}…</div>
                                                </td>
                                                <td className="mono text-secondary">{l.pcc || '—'}</td>
                                                <td className="mono">{l.src}</td>
                                                <td className="mono">{l.dst}</td>
                                                <td>
                                                    <span className={`badge ${l.sr_type === 'srv6' ? 'badge-blue' : 'badge-purple'}`}>
                                                        {l.sr_type?.toUpperCase()}
                                                    </span>
                                                </td>
                                                <td><StatusBadge status={l.status} /></td>
                                                <td><MetricBadge type={l.constraints?.metric_type} /></td>
                                                <td className="mono text-accent">{l.segment_list?.length || 0}</td>
                                                <td className="mono">{l.computed_metric || '—'}</td>
                                                <td>
                                                    <button
                                                        className="btn btn-danger btn-sm"
                                                        onClick={e => { e.stopPropagation(); deleteMut.mutate(l.id) }}
                                                    >
                                                        ✕
                                                    </button>
                                                </td>
                                            </tr>
                                        ))}
                                        {filtered.length === 0 && (
                                            <tr>
                                                <td colSpan={10} style={{ textAlign: 'center', color: 'var(--text-muted)', padding: '40px 0' }}>
                                                    No LSPs found
                                                </td>
                                            </tr>
                                        )}
                                    </tbody>
                                </table>
                            </div>
                        </div>
                    )}
                </div>

                {selected && (
                    <div className="side-panel">
                        <div className="panel-header">
                            <span className="panel-title">⇌ LSP Detail</span>
                            <button className="panel-close" onClick={() => setSelected(null)}>✕</button>
                        </div>
                        <div className="panel-body">
                            <LSPDetail lsp={selected} />
                        </div>
                    </div>
                )}
            </div>

            {showCreate && <CreateLSPModal onClose={() => setShowCreate(false)} onCreated={() => { setShowCreate(false); qc.invalidateQueries({ queryKey: ['lsps'] }) }} />}
        </>
    )
}

function LSPDetail({ lsp }: { lsp: LSP }) {
    const metricNames = ['IGP', 'TE', 'Latency', 'Hopcount']
    return (
        <div>
            <div className="prop-row"><span className="prop-label">ID</span><span className="prop-value" style={{ fontSize: 10 }}>{lsp.id}</span></div>
            <div className="prop-row"><span className="prop-label">Name</span><span className="prop-value">{lsp.name || '—'}</span></div>
            <div className="prop-row"><span className="prop-label">PCC</span><span className="prop-value">{lsp.pcc || '—'}</span></div>
            <div className="prop-row"><span className="prop-label">Source</span><span className="prop-value">{lsp.src}</span></div>
            <div className="prop-row"><span className="prop-label">Destination</span><span className="prop-value">{lsp.dst}</span></div>
            <div className="prop-row"><span className="prop-label">Type</span>
                <span className={`badge ${lsp.sr_type === 'srv6' ? 'badge-blue' : 'badge-purple'}`}>{lsp.sr_type?.toUpperCase()}</span>
            </div>
            <div className="prop-row"><span className="prop-label">Status</span>
                <span className={`badge ${lsp.status === 'active' ? 'badge-green' : lsp.status === 'down' ? 'badge-red' : 'badge-gray'}`}>
                    {lsp.status}
                </span>
            </div>
            {lsp.bsid && <div className="prop-row"><span className="prop-label">BSID</span><span className="prop-value">{lsp.bsid}</span></div>}
            <div className="prop-row">
                <span className="prop-label">Metric</span>
                <span className="prop-value">{metricNames[lsp.constraints?.metric_type] || 'TE'} = {lsp.computed_metric}</span>
            </div>
            <div className="prop-row">
                <span className="prop-label">Constraints</span>
                <span className="prop-value" style={{ fontSize: 10 }}>
                    BW: {lsp.constraints?.min_bandwidth ? `${lsp.constraints.min_bandwidth} B/s` : 'any'}<br />
                    FlexAlgo: {lsp.constraints?.flex_algo || 0}<br />
                    uSID: {lsp.constraints?.use_usid ? 'yes' : 'no'}
                </span>
            </div>

            {lsp.segment_list && lsp.segment_list.length > 0 && (
                <div style={{ marginTop: 16 }}>
                    <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 8 }}>
                        Segment List ({lsp.segment_list.length} SIDs)
                    </div>
                    <div className="segment-list">
                        {lsp.segment_list.map((s: any, i: number) => (
                            <div key={i} className="segment-item">
                                <div className="segment-index">{i + 1}</div>
                                <div className="segment-sid">{s.addr || s.sid || '—'}</div>
                                <div className="segment-behavior">{s.behavior || s.type}</div>
                            </div>
                        ))}
                    </div>
                </div>
            )}
        </div>
    )
}

function CreateLSPModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
    const [form, setForm] = useState({
        name: '', src: '', dst: '',
        sr_type: 'srv6', metric_type: '1',
        min_bandwidth: '', flex_algo: '0', use_usid: false,
    })
    const [error, setError] = useState('')

    const createMut = useMutation({
        mutationFn: (body: any) => fetch('/api/v1/lsps', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        }).then(async r => {
            if (!r.ok) throw new Error((await r.json()).error || 'Failed')
            return r.json()
        }),
        onSuccess: onCreated,
        onError: (e: any) => setError(e.message),
    })

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault()
        setError('')
        createMut.mutate({
            name: form.name,
            src: form.src,
            dst: form.dst,
            sr_type: form.sr_type,
            constraints: {
                metric_type: parseInt(form.metric_type),
                min_bandwidth: form.min_bandwidth ? parseInt(form.min_bandwidth) : 0,
                flex_algo: parseInt(form.flex_algo),
                use_usid: form.use_usid,
            },
        })
    }

    return (
        <div style={{
            position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.7)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100
        }}>
            <div style={{ background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 12, padding: 24, width: 520, maxHeight: '90vh', overflow: 'auto' }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
                    <span style={{ fontSize: 16, fontWeight: 700 }}>Create SRv6 LSP</span>
                    <button className="panel-close" onClick={onClose}>✕</button>
                </div>
                <form onSubmit={handleSubmit}>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                        <div className="form-group">
                            <label className="form-label">LSP Name</label>
                            <input className="form-input" placeholder="pe1-to-pe2-srv6" value={form.name}
                                onChange={e => setForm({ ...form, name: e.target.value })} />
                        </div>
                        <div className="form-grid">
                            <div className="form-group">
                                <label className="form-label">Source Router ID *</label>
                                <input className="form-input" placeholder="192.0.2.1" required value={form.src}
                                    onChange={e => setForm({ ...form, src: e.target.value })} />
                            </div>
                            <div className="form-group">
                                <label className="form-label">Destination Router ID *</label>
                                <input className="form-input" placeholder="192.0.2.2" required value={form.dst}
                                    onChange={e => setForm({ ...form, dst: e.target.value })} />
                            </div>
                        </div>
                        <div className="form-grid">
                            <div className="form-group">
                                <label className="form-label">SR Type</label>
                                <select className="form-select" value={form.sr_type} onChange={e => setForm({ ...form, sr_type: e.target.value })}>
                                    <option value="srv6">SRv6</option>
                                    <option value="mpls">SR-MPLS</option>
                                </select>
                            </div>
                            <div className="form-group">
                                <label className="form-label">Metric</label>
                                <select className="form-select" value={form.metric_type} onChange={e => setForm({ ...form, metric_type: e.target.value })}>
                                    <option value="0">IGP</option>
                                    <option value="1">TE Metric</option>
                                    <option value="2">Latency</option>
                                    <option value="3">Hop Count</option>
                                </select>
                            </div>
                        </div>
                        <div className="form-grid">
                            <div className="form-group">
                                <label className="form-label">Flex-Algorithm</label>
                                <input className="form-input" type="number" min={0} max={255} placeholder="0 = default" value={form.flex_algo}
                                    onChange={e => setForm({ ...form, flex_algo: e.target.value })} />
                            </div>
                            <div className="form-group">
                                <label className="form-label">Min Bandwidth (B/s)</label>
                                <input className="form-input" type="number" placeholder="0 = unconstrained" value={form.min_bandwidth}
                                    onChange={e => setForm({ ...form, min_bandwidth: e.target.value })} />
                            </div>
                        </div>
                        <label className="form-check">
                            <input type="checkbox" checked={form.use_usid} onChange={e => setForm({ ...form, use_usid: e.target.checked })} />
                            <span style={{ fontSize: 13 }}>Use SRv6 uSID compression</span>
                        </label>
                        {error && <div style={{ color: 'var(--red)', fontSize: 12, padding: '8px 12px', background: 'var(--red-soft)', borderRadius: 8 }}>{error}</div>}
                        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
                            <button type="button" className="btn btn-ghost" onClick={onClose}>Cancel</button>
                            <button type="submit" className="btn btn-primary" disabled={createMut.isPending}>
                                {createMut.isPending ? 'Computing path…' : 'Create LSP'}
                            </button>
                        </div>
                    </div>
                </form>
            </div>
        </div>
    )
}
