import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "@/lib/api";
import { AuthGate } from "@/components/AuthGate";
import { Layout } from "@/components/Layout";
import { Dashboard } from "@/pages/Dashboard";
import { HealthchecksPage, HeartbeatsPage } from "@/pages/MonitorLists";
import { HealthcheckForm } from "@/pages/HealthcheckForm";
import { HeartbeatForm } from "@/pages/HeartbeatForm";
import { HeartbeatDetail } from "@/pages/HeartbeatDetail";
import { Notifications } from "@/pages/Notifications";
import { MaintenancePage } from "@/pages/Maintenance";
import { IncidentsPage } from "@/pages/Incidents";
import { Settings } from "@/pages/Settings";
import { KubernetesPage } from "@/pages/Kubernetes";
import { Users } from "@/pages/Users";
import { Account } from "@/pages/Account";
import { StatusPageEditor } from "@/pages/StatusPageEditor";
// Imported eagerly: it captures and clears the connect handoff at page load.
import { UptimyConnected } from "@/pages/UptimyConnected";
import { StatusPage } from "@/pages/StatusPage";
import "./index.css";
import "@/lib/theme";
import { ConfirmDialog } from "@/components/ui/confirm";
import { Toaster } from "@/components/ui/toast";
import TipLayer from "@/components/ui/tip-layer";
import { ErrorBoundary } from "@/components/ErrorBoundary";

// Recharts is the bulk of the bundle; only load it on the page with the chart.
const HealthcheckDetail = lazy(() =>
  import("@/pages/HealthcheckDetail").then((m) => ({ default: m.HealthcheckDetail })),
);

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      // Client errors won't fix themselves; network errors (0) and 5xx might.
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
    },
  },
});

// A 401 anywhere means the session expired: re-check auth so the login form shows.
queryClient.getQueryCache().subscribe((event) => {
  const err = event.query.state.error;
  if (event.type === "updated" && err instanceof ApiError && err.status === 401 && event.query.queryKey[0] !== "auth") {
    queryClient.invalidateQueries({ queryKey: ["auth"] });
  }
});

// On the status page's own domain the server marks the page so only the
// status page renders; nothing else is served there.
const statusOnly = document.querySelector('meta[name="uptimy-view"]')?.getAttribute("content") === "status";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ConfirmDialog />
      <Toaster />
      <TipLayer />
      <BrowserRouter>
        {statusOnly ? (
          <Routes>
            <Route
              path="*"
              element={
                <ErrorBoundary>
                  <StatusPage />
                </ErrorBoundary>
              }
            />
          </Routes>
        ) : (
          <Routes>
            <Route
              path="/status"
              element={
                <ErrorBoundary>
                  <StatusPage />
                </ErrorBoundary>
              }
            />
            <Route
              element={
                <AuthGate>
                  <Layout />
                </AuthGate>
              }
            >
              <Route index element={<Dashboard />} />
              <Route path="healthchecks">
                <Route index element={<HealthchecksPage />} />
                <Route path="new" element={<HealthcheckForm />} />
                <Route
                  path=":id"
                  element={
                    <Suspense>
                      <HealthcheckDetail />
                    </Suspense>
                  }
                />
                <Route path=":id/edit" element={<HealthcheckForm />} />
              </Route>
              <Route path="heartbeats">
                <Route index element={<HeartbeatsPage />} />
                <Route path="new" element={<HeartbeatForm />} />
                <Route path=":id" element={<HeartbeatDetail />} />
                <Route path=":id/edit" element={<HeartbeatForm />} />
              </Route>
              <Route path="maintenance" element={<MaintenancePage />} />
              <Route path="incidents" element={<IncidentsPage />} />
              <Route path="notifications" element={<Notifications />} />
              <Route path="users" element={<Users />} />
              <Route path="status-page" element={<StatusPageEditor />} />
              <Route path="settings" element={<Settings />} />
              <Route path="settings/kubernetes" element={<KubernetesPage />} />
              <Route path="account" element={<Account />} />
              <Route path="uptimy/connected" element={<UptimyConnected />} />
            </Route>
          </Routes>
        )}
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
