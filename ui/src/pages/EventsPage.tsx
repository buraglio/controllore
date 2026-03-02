import { useEffect, useRef, useState } from 'react'

interface PCEEvent {
    type: string
    ts: string
    id?: string
    data?: any
}

const EVENT_COLORS: Record<string, string> = {
    'lsp.created': 'var(--green)',
    'lsp.updated': 'var(--accent)',
    'lsp.deleted': 'var(--red)',
    'lsp.status_changed': 'var(--yellow)',
    'lsp.rerouted': 'var(--purple)',
    'topology.node_up': 'var(--green)',
    'topology.node_down': 'var(--red)',
    'topology.link_up': 'var(--green)',
    'topology.link_down': 'var(--red)',
    'topology.link_changed': 'var(--yellow)',
    'pcep.session_up': 'var(--green)',
    'pcep.session_down': 'var(--red)',
    'pce.path_computed': 'var(--accent)',
    'pce.path_failed': 'var(--red)',
}

export default function EventsPage() {
    const [events, setEvents] = useState<PCEEvent[]>([])
    const [connected, setConnected] = useState(false)
    const [filter, setFilter] = useState('')
    const [paused, setPaused] = useState(false)
    const wsRef = useRef<WebSocket | null>(null)
    const bottomRef = useRef<HTMLDivElement>(null)

    useEffect(() => {
        const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
        const ws = new WebSocket(`${proto}//${window.location.host}/ws/events`)
        wsRef.current = ws

        ws.onopen = () => setConnected(true)
        ws.onclose = () => setConnected(false)
        ws.onerror = () => setConnected(false)

        ws.onmessage = (msg) => {
            if (paused) return
            try {
                const evt: PCEEvent = JSON.parse(msg.data)
                setEvents(prev => [evt, ...prev].slice(0, 500)) // keep last 500
            } catch { }
        }

        return () => ws.close()
    }, [])

    // Auto-scroll disabled since we show newest at top
    const handleClear = () => setEvents([])

    const filtered = filter
        ? events.filter(e => e.type.includes(filter) || JSON.stringify(e.data).includes(filter))
        : events

    return (
        <>
            <div className="page-header">
                <div>
                    <div className="page-title">Live Events</div>
                    <div className="page-subtitle">Real-time PCE event stream via WebSocket</div>
                </div>
                <div className="page-actions">
                    <span style={{
                        display: 'flex', alignItems: 'center', gap: 6, fontSize: 12,
                        color: connected ? 'var(--green)' : 'var(--red)',
                    }}>
                        <span style={{
                            width: 7, height: 7, borderRadius: '50%',
                            background: 'currentColor',
                            boxShadow: connected ? '0 0 8px var(--green)' : 'none',
                            animation: connected ? 'pulse-green 2s ease-in-out infinite' : 'none',
                        }} />
                        {connected ? 'Connected' : 'Disconnected'}
                    </span>
                    <input
                        className="form-input"
                        style={{ width: 200 }}
                        placeholder="Filter events…"
                        value={filter}
                        onChange={e => setFilter(e.target.value)}
                    />
                    <button className="btn btn-ghost btn-sm" onClick={() => setPaused(p => !p)}>
                        {paused ? '▶ Resume' : '⏸ Pause'}
                    </button>
                    <button className="btn btn-ghost btn-sm" onClick={handleClear}>Clear</button>
                </div>
            </div>

            <div className="page-body">
                {/* Event type filters */}
                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                    {['', 'lsp', 'topology', 'pcep', 'pce'].map(cat => (
                        <button
                            key={cat}
                            className={`btn btn-sm ${filter === cat ? 'btn-primary' : 'btn-ghost'}`}
                            onClick={() => setFilter(cat)}
                        >
                            {cat === '' ? 'All' : cat.toUpperCase()}
                        </button>
                    ))}
                </div>

                <div className="card" style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
                    <div className="card-header">
                        <span className="card-title">Event Stream</span>
                        <span className="badge badge-gray">{filtered.length} events</span>
                    </div>
                    <div style={{ flex: 1, overflow: 'auto', padding: '8px 0' }}>
                        {filtered.length === 0 ? (
                            <div style={{ textAlign: 'center', color: 'var(--text-muted)', padding: '60px 0' }}>
                                {connected ? 'Waiting for events…' : 'WebSocket disconnected'}
                            </div>
                        ) : (
                            <div className="events-list" ref={bottomRef}>
                                {filtered.map((evt, i) => (
                                    <div key={i} className="event-item">
                                        <div className="event-time">
                                            {new Date(evt.ts).toLocaleTimeString('en-US', { hour12: false })}
                                        </div>
                                        <div className="event-type" style={{ color: EVENT_COLORS[evt.type] || 'var(--text-secondary)' }}>
                                            {evt.type}
                                        </div>
                                        {evt.id && (
                                            <div className="mono text-muted" style={{ fontSize: 11, minWidth: 90 }}>
                                                {evt.id.slice(0, 12)}
                                            </div>
                                        )}
                                        {evt.data && (
                                            <div className="event-data">
                                                {typeof evt.data === 'object'
                                                    ? Object.entries(evt.data).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(' · ')
                                                    : String(evt.data)}
                                            </div>
                                        )}
                                    </div>
                                ))}
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </>
    )
}
