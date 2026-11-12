import { lazy, Suspense } from "react"
import { Navigate, Route, Routes, useLocation } from "react-router-dom"
import { useAuth } from "@/providers/auth"
import { TenantProvider } from "@/providers/tenant"
import { FullPageLoader } from "@/components/shared"
import { LoginPage } from "@/pages/Login"

const AppShell = lazy(() => import("@/components/layout/AppShell").then((m) => ({ default: m.AppShell })))
const WelcomePage = lazy(() => import("@/pages/Welcome").then((m) => ({ default: m.WelcomePage })))
const InvitePage = lazy(() => import("@/pages/Invite").then((m) => ({ default: m.InvitePage })))
const OverviewPage = lazy(() => import("@/pages/Overview").then((m) => ({ default: m.OverviewPage })))
const QueryPage = lazy(() => import("@/pages/Query").then((m) => ({ default: m.QueryPage })))
const DataSourcesPage = lazy(() => import("@/pages/DataSources").then((m) => ({ default: m.DataSourcesPage })))
const SavedQueriesPage = lazy(() => import("@/pages/SavedQueries").then((m) => ({ default: m.SavedQueriesPage })))
const TeamPage = lazy(() => import("@/pages/Team").then((m) => ({ default: m.TeamPage })))
const ActivityPage = lazy(() => import("@/pages/ActivityPage").then((m) => ({ default: m.ActivityPage })))
const SettingsPage = lazy(() => import("@/pages/Settings").then((m) => ({ default: m.SettingsPage })))
const NotFoundPage = lazy(() => import("@/pages/NotFound").then((m) => ({ default: m.NotFoundPage })))

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
    <Suspense fallback={<FullPageLoader />}>
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
    </Suspense>
  )
}
