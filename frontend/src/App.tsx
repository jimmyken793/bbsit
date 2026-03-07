import { useState, useEffect, useCallback } from 'react'
import { BrowserRouter, Routes, Route, Navigate, Outlet, Link } from 'react-router-dom'
import { api } from './api'
import type { AuthStatus } from './types'
import SetupPage from './pages/SetupPage'
import LoginPage from './pages/LoginPage'
import DashboardPage from './pages/DashboardPage'
import ProjectDetailPage from './pages/ProjectDetailPage'
import ProjectFormPage from './pages/ProjectFormPage'

function Navbar({ onLogout }: { onLogout: () => void }) {
  return (
    <nav className="navbar">
      <div className="container">
        <Link to="/" className="navbar-brand">bb<span>sit</span></Link>
        <button className="btn btn-outline btn-sm" onClick={onLogout}>Logout</button>
      </div>
    </nav>
  )
}

function Layout({ onLogout }: { onLogout: () => void }) {
  return (
    <>
      <Navbar onLogout={onLogout} />
      <main className="container" style={{ paddingBottom: '32px' }}>
        <Outlet />
      </main>
    </>
  )
}

export default function App() {
  const [auth, setAuth] = useState<AuthStatus | null>(null)

  const refresh = useCallback(() => {
    api.auth.status()
      .then(setAuth)
      .catch(() => setAuth({ setup_required: false, logged_in: false }))
  }, [])

  useEffect(() => { refresh() }, [refresh])

  if (!auth) {
    return <div className="page-loading"><div className="spinner" /></div>
  }

  return (
    <BrowserRouter>
      <Routes>
        <Route
          path="/setup"
          element={auth.setup_required ? <SetupPage onDone={refresh} /> : <Navigate to="/" replace />}
        />
        <Route
          path="/login"
          element={
            auth.logged_in ? <Navigate to="/" replace /> :
            auth.setup_required ? <Navigate to="/setup" replace /> :
            <LoginPage onDone={refresh} />
          }
        />
        {auth.setup_required ? (
          <Route path="*" element={<Navigate to="/setup" replace />} />
        ) : !auth.logged_in ? (
          <Route path="*" element={<Navigate to="/login" replace />} />
        ) : (
          <Route
            element={<Layout onLogout={() => api.auth.logout().then(refresh)} />}
          >
            <Route path="/" element={<DashboardPage />} />
            <Route path="/projects/new" element={<ProjectFormPage />} />
            <Route path="/projects/:id/edit" element={<ProjectFormPage />} />
            <Route path="/projects/:id" element={<ProjectDetailPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        )}
      </Routes>
    </BrowserRouter>
  )
}
