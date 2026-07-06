import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { lazy, Suspense } from 'react'
import { getToken, clearToken } from './lib/api'
import { Key, Server, LogOut } from 'lucide-react'
import { Button } from './components/ui/button'

const LoginPage = lazy(() => import('./pages/LoginPage'))
const ServicesPage = lazy(() => import('./pages/ServicesPage'))

function Layout({ children }: { children: React.ReactNode }) {
  const handleLogout = () => {
    clearToken()
    window.location.href = '/admin/login'
  }

  return (
    <div className="min-h-screen flex">
      <aside className="w-56 border-r bg-card flex flex-col shrink-0">
        <div className="h-14 flex items-center gap-2 px-4 border-b">
          <Key className="h-5 w-5" />
          <span className="font-semibold text-sm">Gateway Admin</span>
        </div>
        <nav className="flex-1 px-2 py-3 space-y-1">
          <a
            href="/admin/services"
            className="flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium bg-accent text-accent-foreground"
          >
            <Server className="h-4 w-4" />
            Services
          </a>
        </nav>
        <div className="border-t p-2">
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start text-muted-foreground"
            onClick={handleLogout}
          >
            <LogOut className="h-4 w-4" />
            Sign out
          </Button>
        </div>
      </aside>
      <main className="flex-1 bg-muted/20 p-6 min-w-0">{children}</main>
    </div>
  )
}

function AuthGuard({ children }: { children: React.ReactNode }) {
  const token = getToken()
  if (!token) {
    return <Navigate to="/admin/login" replace />
  }
  return <Layout>{children}</Layout>
}

function Loading() {
  return (
    <div className="min-h-screen flex items-center justify-center">
      <div className="text-sm text-muted-foreground animate-pulse">Loading...</div>
    </div>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <Suspense fallback={<Loading />}>
        <Routes>
          <Route path="/admin/login" element={<LoginPage />} />
          <Route
            path="/admin/services"
            element={
              <AuthGuard>
                <ServicesPage />
              </AuthGuard>
            }
          />
          <Route path="*" element={<Navigate to="/admin/services" replace />} />
        </Routes>
      </Suspense>
    </BrowserRouter>
  )
}
