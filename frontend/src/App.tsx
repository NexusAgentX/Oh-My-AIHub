import {
  createBrowserRouter,
  createRoutesFromElements,
  Navigate,
  Outlet,
  Route,
  RouterProvider,
} from 'react-router-dom'
import { AdminAccountsPage } from './accounts/AdminAccountsPage'
import { CreatedCredentialPage } from './accounts/CreatedCredentialPage'
import { EphemeralCredentialProvider } from './accounts/EphemeralCredentialProvider'
import { AccountSettingsPage } from './auth/AccountSettingsPage'
import { AuthProvider, useAuth } from './auth/AuthProvider'
import { FirstPasswordChangePage } from './auth/FirstPasswordChangePage'
import { LoginPage } from './auth/LoginPage'
import { canEnterAdmin, defaultDestination } from './auth/routePolicy'
import { AdminModelsPage } from './models/AdminModelsPage'
import { LoadingState } from './ui/FormControls'
import { WelcomePage } from './welcome/WelcomePage'

function RequireSession() {
  const { account, loading } = useAuth()
  if (loading) return <FullPageLoading />
  if (!account) return <Navigate replace to="/login" />
  return <Outlet />
}

function RequireReadyAccount() {
  const { account } = useAuth()
  if (account?.must_change_password) {
    return <Navigate replace to="/account/password?first=1" />
  }
  return <Outlet />
}

function RequireAdministrator() {
  const { account } = useAuth()
  if (!canEnterAdmin(account)) return <Navigate replace to="/account" />
  return <Outlet />
}

function RootRedirect() {
  const { account, loading } = useAuth()
  if (loading) return <FullPageLoading />
  return <Navigate replace to={defaultDestination(account)} />
}

function FullPageLoading() {
  return (
    <main className="route-loading">
      <LoadingState />
    </main>
  )
}

function AppProviders() {
  return (
    <EphemeralCredentialProvider>
      <AuthProvider>
        <Outlet />
      </AuthProvider>
    </EphemeralCredentialProvider>
  )
}

export const appRoutes = createRoutesFromElements(
  <Route element={<AppProviders />}>
    <Route element={<WelcomePage />} path="/welcome" />
    <Route element={<LoginPage />} path="/login" />
    <Route element={<RequireSession />}>
      <Route element={<FirstPasswordChangePage />} path="/account/password" />
      <Route element={<RequireReadyAccount />}>
        <Route element={<AccountSettingsPage />} path="/account" />
        <Route element={<RequireAdministrator />}>
          <Route element={<AdminAccountsPage />} path="/admin/accounts" />
          <Route
            element={<CreatedCredentialPage />}
            path="/admin/accounts/created"
          />
          <Route element={<AdminModelsPage />} path="/admin/models" />
        </Route>
      </Route>
    </Route>
    <Route element={<RootRedirect />} path="/" />
    <Route element={<RootRedirect />} path="*" />
  </Route>,
)

let router: ReturnType<typeof createBrowserRouter> | undefined

export function App() {
  router ??= createBrowserRouter(appRoutes)
  return <RouterProvider router={router} />
}
