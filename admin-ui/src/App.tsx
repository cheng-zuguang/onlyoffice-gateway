import { BrowserRouter, Routes, Route, Navigate, NavLink } from 'react-router-dom'
import { lazy, Suspense } from 'react'
import { getToken, clearToken } from './lib/api'
import { Key, Server, LogOut, FileArchive, ScrollText } from 'lucide-react'
import { Button } from './components/ui/button'

const LoginPage = lazy(() => import('./pages/LoginPage'))
const ServicesPage = lazy(() => import('./pages/ServicesPage'))
const AttachmentsPage = lazy(() => import('./pages/AttachmentsPage'))
const LogsPage = lazy(() => import('./pages/LogsPage'))

function Layout({ children }: { children: React.ReactNode }) {
  const handleLogout = () => {
    clearToken()
    window.location.href = '/admin/login'
  }

  return (
    <div className="h-screen min-h-screen flex">
      <aside className="w-56 border-r bg-card flex flex-col shrink-0">
        <div className="h-14 flex items-center gap-2 px-4 border-b">
          <Key className="h-5 w-5" />
          <span className="font-semibold text-sm">Gateway Admin</span>
        </div>
        <nav className="flex-1 space-y-1 px-2 py-3">
          <SidebarLink to="/admin/services" icon={<Server className="h-4 w-4" />}>服务管理</SidebarLink>
          <SidebarLink to="/admin/attachments" icon={<FileArchive className="h-4 w-4" />}>临时附件</SidebarLink>
          <SidebarLink to="/admin/logs" icon={<ScrollText className="h-4 w-4" />}>运行日志</SidebarLink>
        </nav>
        <div className="border-t p-2">
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start text-muted-foreground"
            onClick={handleLogout}
          >
            <LogOut className="h-4 w-4" />
            退出登录
          </Button>
        </div>
      </aside>
      <main className="flex min-h-0 flex-1 bg-muted/20 p-6 min-w-0">{children}</main>
    </div>
  )
}

function SidebarLink({ to, icon, children }: { to: string; icon: React.ReactNode; children: React.ReactNode }) {
  return <NavLink to={to} className={({ isActive }) => `flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors ${isActive ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'}`}>{icon}{children}</NavLink>
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
      <div className="text-sm text-muted-foreground animate-pulse">加载中...</div>
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
          <Route path="/admin/attachments" element={<AuthGuard><AttachmentsPage /></AuthGuard>} />
          <Route path="/admin/logs" element={<AuthGuard><LogsPage /></AuthGuard>} />
          <Route path="*" element={<Navigate to="/admin/services" replace />} />
        </Routes>
      </Suspense>
    </BrowserRouter>
  )
}
