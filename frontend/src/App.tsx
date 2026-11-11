import { Navigate, Route, Routes, useLocation } from "react-router-dom"
import { useAuth } from "@/providers/auth"
import { TenantProvider } from "@/providers/tenant"
import { AppShell } from "@/components/layout/AppShell"
import { FullPageLoader } from "@/components/shared"
import { LoginPage } from "@/pages/Login"
import { WelcomePage } from "@/pages/Welcome"
import { InvitePage } from "@/pages/Invite"
import { OverviewPage } from "@/pages/Overview"
import { QueryPage } from "@/pages/Query"
import { DataSourcesPage } from "@/pages/DataSources"
import { SavedQueriesPage } from "@/pages/SavedQueries"
import { TeamPage } from "@/pages/Team"
import { ActivityPage } from "@/pages/ActivityPage"
import { SettingsPage } from "@/pages/Settings"
import { NotFoundPage } from "@/pages/NotFound"

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { me, loading } = useAuth()
  const location = useLocation()
  if (loading) return <FullPageLoader />
  if (!me) {
    const returnTo = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?return_to=${returnTo}`} replace />
  }
  return <>{children}</>
}

function RootRedirect() {
  const { me, loading } = useAuth()
  if (loading) return <FullPageLoader />
  if (!me) return <LoginPage />
  if (me.tenants.length > 0) return <Navigate to={`/w/${me.tenants[0].slug}`} replace />
  return <Navigate to="/welcome" replace />
}

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<RootRedirect />} />
      <Route path="/login" element={<LoginPage />} />
      <Route path="/invite/:token" element={<InvitePage />} />
      <Route
        path="/welcome"
        element={
          <RequireAuth>
            <WelcomePage />
          </RequireAuth>
        }
      />
      <Route
        path="/w/:slug"
        element={
          <RequireAuth>
            <TenantProvider>
              <AppShell />
            </TenantProvider>
          </RequireAuth>
        }
      >
        <Route index element={<OverviewPage />} />
        <Route path="query" element={<QueryPage />} />
        <Route path="sources" element={<DataSourcesPage />} />
        <Route path="saved" element={<SavedQueriesPage />} />
        <Route path="team" element={<TeamPage />} />
        <Route path="activity" element={<ActivityPage />} />
        <Route path="settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  )
}
