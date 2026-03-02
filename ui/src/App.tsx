import { Routes, Route, NavLink, useLocation } from 'react-router-dom'
import { useState, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import TopologyPage from './pages/TopologyPage'
import LSPsPage from './pages/LSPsPage'
import PathStudioPage from './pages/PathStudioPage'
import SessionsPage from './pages/SessionsPage'
import EventsPage from './pages/EventsPage'
import Dashboard from './pages/Dashboard'

function Sidebar() {
    const location = useLocation()
    const navItems = [
        { path: '/', label: 'Dashboard', icon: '⊞' },
        { path: '/topology', label: 'Topology', icon: '◈' },
        { path: '/lsps', label: 'LSPs', icon: '⇌' },
        { path: '/path-studio', label: 'Path Studio', icon: '⊕' },
        { path: '/sessions', label: 'PCEP Sessions', icon: '⊙' },
        { path: '/events', label: 'Live Events', icon: '≋' },
    ]

    return (
        <div className="sidebar">
            <div className="brand">
                <div className="brand-name">
                    <span className="brand-dot" />
                    Controllore
                </div>
                <div className="brand-sub">SRv6 Stateful PCE</div>
            </div>
            <nav className="nav-section">
                <div className="nav-section-title">Navigation</div>
                {navItems.map(item => (
                    <NavLink
                        key={item.path}
                        to={item.path}
                        end={item.path === '/'}
                        className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
                    >
                        <span style={{ fontSize: '16px', lineHeight: 1 }}>{item.icon}</span>
                        {item.label}
                    </NavLink>
                ))}
            </nav>
            <div style={{ flex: 1 }} />
            <HealthIndicator />
        </div>
    )
}

function HealthIndicator() {
    const { data, isError } = useQuery({
        queryKey: ['health'],
        queryFn: () => fetch('/api/v1/health').then(r => r.json()),
        refetchInterval: 3000,
    })
    const ok = !isError && data?.status === 'ok'
    return (
        <div style={{
            padding: '12px 16px',
            borderTop: '1px solid var(--border)',
            display: 'flex', alignItems: 'center', gap: 8,
            fontSize: 11, color: 'var(--text-muted)'
        }}>
            <span style={{
                width: 7, height: 7, borderRadius: '50%',
                background: ok ? 'var(--green)' : 'var(--red)',
                flexShrink: 0,
                boxShadow: `0 0 6px ${ok ? 'var(--green)' : 'var(--red)'}`,
            }} />
            <span>
                {ok
                    ? `${data.ted_nodes} nodes · ${data.ted_links} links`
                    : 'API unreachable'}
            </span>
        </div>
    )
}

export default function App() {
    return (
        <div className="layout">
            <Sidebar />
            <div className="main-content">
                <Routes>
                    <Route path="/" element={<Dashboard />} />
                    <Route path="/topology" element={<TopologyPage />} />
                    <Route path="/lsps" element={<LSPsPage />} />
                    <Route path="/path-studio" element={<PathStudioPage />} />
                    <Route path="/sessions" element={<SessionsPage />} />
                    <Route path="/events" element={<EventsPage />} />
                </Routes>
            </div>
        </div>
    )
}
