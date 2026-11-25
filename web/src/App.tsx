import { Component, Suspense, lazy, useEffect, useState, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  Activity,
  AlertCircle,
  BookOpen,
  CheckCircle2,
  Home,
  Info,
  Layers,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Settings,
  Shield,
  User as UserIcon,
  WifiOff,
  X,
} from "lucide-react";
import { api, serverApi, serverPath } from "./api";
import { createLocaleFormatter, LocaleFormatProvider } from "./utils/format";
import { safeExternalURL } from "./utils/sanitize";
import {
  applyTheme,
  getStoredAccentColor,
  getStoredThemeMode,
  normalizeAccentColor,
  normalizeThemeMode,
  setStoredAccentColor,
  setStoredThemeMode,
  type AccentColor,
  type ThemeMode,
} from "./utils/theme";
import { AuthGate } from "./components/AuthGate";
import { LanguageMenu, ServerMenu, ThemeMenu } from "./components/Controls";
import { ConfirmModal, StreamsModal, SyncModal } from "./components/Modals";
import { type Lang } from "./i18n";
import type { AuthStatus, ServerStatus, ServerSummary, VersionInfo } from "./types";
const AccessView = lazy(() => import("./views/AccessView").then((module) => ({ default: module.AccessView })));
const AccountView = lazy(() => import("./views/AccountView").then((module) => ({ default: module.AccountView })));
const ApiDocsView = lazy(() => import("./views/ApiDocsView").then((module) => ({ default: module.ApiDocsView })));
const OverviewView = lazy(() => import("./views/OverviewView").then((module) => ({ default: module.OverviewView })));
const QueueView = lazy(() => import("./views/QueueView").then((module) => ({ default: module.QueueView })));
const SettingsView = lazy(() => import("./views/SettingsView").then((module) => ({ default: module.SettingsView })));

type View = "overview" | "queue" | "settings" | "access" | "account" | "docs";
export type ActivityTab = "queue" | "history" | "blocklist" | "logs";

const VIEW_PATHS: Record<View, string> = {
  overview: "/overview",
  queue: "/activity/queue",
  settings: "/settings",
  access: "/access",
  account: "/account",
  docs: "/docs",
};

function viewFromPathname(pathname: string): View {
  const seg = pathname.replace(/^\/+|\/+$/g, "").split("/")[0]?.toLowerCase() || "";
  if (seg === "activity") return "queue";
  if (seg === "queue" || seg === "settings" || seg === "access" || seg === "account" || seg === "docs" || seg === "overview") {
    return seg;
  }
  return "overview";
}

function activityTabFromPathname(pathname: string): ActivityTab {
  const parts = pathname.replace(/^\/+|\/+$/g, "").split("/");
  const sub = (parts[0]?.toLowerCase() === "activity" ? parts[1] : "")?.toLowerCase() || "";
  if (sub === "history" || sub === "blocklist" || sub === "logs" || sub === "queue") {
    return sub;
  }
  return "queue";
}

const VIEW_TITLE_KEYS: Record<View, string> = {
  overview: "nav.overview",
  queue: "nav.queue",
  settings: "nav.settings",
  access: "nav.access",
  account: "nav.account",
  docs: "nav.docs",
};

class ErrorBoundary extends Component<{ children: ReactNode }, { err: string }> {
  state = { err: "" };
  static getDerivedStateFromError(error: Error) {
    return { err: error.message || "Unexpected UI error" };
  }
  render() {
    if (this.state.err) {
      return (
        <main className="auth-shell">
          <div className="auth-card" role="alert">
            <h2 style={{ margin: 0 }}>Sweeparr UI Error</h2>
            <div className="err-banner">{this.state.err}</div>
            <button type="button" className="btn btn-primary" onClick={() => window.location.reload()}>
              Reload Page
            </button>
          </div>
        </main>
      );
    }
    return this.props.children;
  }
}

function ForcedPasswordChangeGate({
  username,
  onDone,
  onSignOut,
}: {
  username: string;
  onDone: () => Promise<void>;
  onSignOut: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [curPw, setCurPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    if (newPw !== confirmPw) {
      setErr(t("auth.pw_mismatch"));
      return;
    }
    setBusy(true);
    try {
      await api("/api/auth/password", "POST", { current_password: curPw, new_password: newPw });
      await onDone();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="auth-shell">
      <form className="auth-card" onSubmit={submit}>
        <h2 style={{ margin: 0 }}>{t("auth.force_pw_title")}</h2>
        <p className="small muted" style={{ margin: 0 }}>
          {t("auth.force_pw_desc", { username })}
        </p>
        {err && <div className="err-banner">{err}</div>}
        <input
          type="password"
          aria-label={t("security.current_pw")}
          placeholder={t("security.current_pw")}
          value={curPw}
          onChange={(e) => setCurPw(e.target.value)}
          required
          autoFocus
        />
        <input
          type="password"
          aria-label={t("security.new_pw")}
          placeholder={t("security.new_pw")}
          value={newPw}
          onChange={(e) => setNewPw(e.target.value)}
          minLength={10}
          required
        />
        <input
          type="password"
          aria-label={t("auth.confirm_pw")}
          placeholder={t("auth.confirm_pw")}
          value={confirmPw}
          onChange={(e) => setConfirmPw(e.target.value)}
          minLength={10}
          required
        />
        <span className="small muted">{t("auth.password_hint", { min: 10 })}</span>
        <div className="action-group" style={{ justifyContent: "space-between", marginTop: 4 }}>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => void onSignOut()}>
            {t("sidebar.sign_out")}
          </button>
          <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
            {t("security.update_pw")}
          </button>
        </div>
      </form>
    </main>
  );
}

export function App() {
  return (
    <ErrorBoundary>
      <AppInner />
    </ErrorBoundary>
  );
}

function AppInner() {
  const { t, i18n } = useTranslation();
  const lang = ((i18n.resolvedLanguage || i18n.language || "en").slice(0, 2) as Lang) || "en";
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null);
  const [loadError, setLoadError] = useState("");
  const [isOnline, setIsOnline] = useState(() => (typeof navigator !== "undefined" ? navigator.onLine : true));
  const [servers, setServers] = useState<ServerSummary[]>([]);
  const [serversLoaded, setServersLoaded] = useState(false);
  const [serverId, setServerId] = useState<string>(() => {
    try {
      return localStorage.getItem("sweeparr_server_id") || "";
    } catch {
      return "";
    }
  });
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [view, setView] = useState<View>(() =>
    typeof window !== "undefined" ? viewFromPathname(window.location.pathname) : "overview"
  );
  const [activityTab, setActivityTab] = useState<ActivityTab>(() =>
    typeof window !== "undefined" ? activityTabFromPathname(window.location.pathname) : "queue"
  );
  const [historyStatusFilter, setHistoryStatusFilter] = useState<string>("");
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false);
  const [versionInfo, setVersionInfo] = useState<VersionInfo | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem("sweeparr_sidebar_collapsed") === "1";
    } catch {
      return false;
    }
  });
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [themeMode, setThemeMode] = useState<ThemeMode>(() => getStoredThemeMode());
  const [accentColor, setAccentColor] = useState<AccentColor>(() => getStoredAccentColor());
  const [toasts, setToasts] = useState<{ id: number; message: string; tone: "info" | "success" | "error" }[]>([]);
  const [syncOpen, setSyncOpen] = useState(false);
  const [streamsOpen, setStreamsOpen] = useState(false);
  const [confirmState, setConfirmState] = useState<{
    open: boolean;
    title: string;
    message: string;
    confirmLabel: string;
    onConfirm: () => void;
  }>({ open: false, title: "", message: "", confirmLabel: "Confirm", onConfirm: () => {} });

  function toggleSidebar() {
    setSidebarCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem("sweeparr_sidebar_collapsed", next ? "1" : "0");
      } catch {
        // ignore storage errors
      }
      return next;
    });
  }

  function dismissToast(id: number) {
    setToasts((cur) => cur.filter((x) => x.id !== id));
  }

  function toast(msg: string) {
    const id = Date.now() + Math.floor(Math.random() * 10000);
    const low = msg.toLowerCase();
    const tone: "info" | "success" | "error" =
      low.includes("fail") || low.includes("error") || low.includes("denied") || low.includes("invalid") || low.includes("required")
        ? "error"
        : low.includes("saved") || low.includes("created") || low.includes("updated") || low.includes("registered") || low.includes("completed") || low.includes("resumed") || low.includes("removed") || low.includes("cleaned") || low.includes("terminated")
          ? "success"
          : "info";
    setToasts((cur) => [...cur.slice(-3), { id, message: msg, tone }]);
    window.setTimeout(() => {
      setToasts((cur) => cur.filter((x) => x.id !== id));
    }, 4500);
  }

  useEffect(() => {
    document.title = `${t(VIEW_TITLE_KEYS[view])} — ${t("brand.title")}`;
  }, [view, lang, t]);

  function updateThemeMode(next: ThemeMode) {
    setThemeMode(next);
    setStoredThemeMode(next);
    applyTheme(next, accentColor);
  }

  function updateAccentColor(next: AccentColor) {
    setAccentColor(next);
    setStoredAccentColor(next);
    applyTheme(themeMode, next);
  }

  useEffect(() => {
    applyTheme(themeMode, accentColor);
    if (themeMode !== "system" || typeof window === "undefined" || !window.matchMedia) return;
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    const onChange = () => applyTheme("system", accentColor);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [themeMode, accentColor]);

  useEffect(() => {
    if (status?.config?.accent_color) {
      const serverAccent = normalizeAccentColor(status.config.accent_color);
      if (!localStorage.getItem("sweeparr_accent_color")) {
        setAccentColor(serverAccent);
      }
    }
    if (status?.config?.theme && !localStorage.getItem("sweeparr_theme_mode")) {
      setThemeMode(normalizeThemeMode(status.config.theme));
    }
  }, [status?.config?.theme, status?.config?.accent_color]);

  useEffect(() => {
    if (!mobileMenuOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMobileMenuOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mobileMenuOpen]);

  useEffect(() => {
    const onPop = () => {
      setView(viewFromPathname(window.location.pathname));
      setActivityTab(activityTabFromPathname(window.location.pathname));
    };
    const onOnline = () => setIsOnline(true);
    const onOffline = () => setIsOnline(false);
    const onUnauthorized = () => {
      setAuthStatus((prev) => ({
        needs_setup: false,
        authenticated: false,
        mfa_pending: false,
        oidc_enabled: prev?.oidc_enabled ?? false,
        user: null,
      }));
      setVersionInfo(null);
    };
    window.addEventListener("popstate", onPop);
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    window.addEventListener("sweeparr:unauthorized", onUnauthorized);
    return () => {
      window.removeEventListener("popstate", onPop);
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
      window.removeEventListener("sweeparr:unauthorized", onUnauthorized);
    };
  }, []);

  useEffect(() => {
    const loadVersion = () => {
      if (authStatus?.authenticated && authStatus.user?.role === "admin") {
        void api<VersionInfo>("/api/version").then(setVersionInfo).catch(() => {});
      } else {
        setVersionInfo(null);
      }
    };
    loadVersion();
    window.addEventListener("sweeparr:version-refresh", loadVersion);
    return () => window.removeEventListener("sweeparr:version-refresh", loadVersion);
  }, [authStatus?.authenticated, authStatus?.user?.role]);

  useEffect(() => {
    window.scrollTo(0, 0);
    const main = document.getElementById("main-content");
    if (main) main.scrollTop = 0;
  }, [view]);

  function handleActivityTabChange(tab: ActivityTab) {
    setActivityTab(tab);
    const targetPath = `/activity/${tab}`;
    if (window.location.pathname !== targetPath) {
      window.history.replaceState({ view: "queue", tab }, "", targetPath);
    }
  }

  function navigateToHistory(statusFilter: string) {
    setHistoryStatusFilter(statusFilter);
    setActivityTab("history");
    setHasUnsavedChanges(false);
    setMobileMenuOpen(false);
    setView("queue");
    const targetPath = "/activity/history";
    if (window.location.pathname !== targetPath) {
      window.history.pushState({ view: "queue", tab: "history" }, "", targetPath);
    }
  }

  function navigateTo(next: View, e?: MouseEvent) {
    if (e) {
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
      e.preventDefault();
    }
    if (next === view) return;
    const commit = () => {
      setHasUnsavedChanges(false);
      setMobileMenuOpen(false);
      setView(next);
      const targetPath = next === "queue" ? `/activity/${activityTab}` : VIEW_PATHS[next];
      if (window.location.pathname !== targetPath) {
        window.history.pushState({ view: next }, "", targetPath);
      }
    };
    if (hasUnsavedChanges) {
      setConfirmState({
        open: true,
        title: t("confirm.unsaved_title"),
        message: t("confirm.unsaved_msg"),
        confirmLabel: t("confirm.unsaved_leave"),
        onConfirm: () => {
          setConfirmState((s) => ({ ...s, open: false }));
          commit();
        },
      });
      return;
    }
    commit();
  }

  async function refreshAuth() {
    const res = await api<AuthStatus>("/api/auth/status");
    setAuthStatus(res);
    return res;
  }

  function selectServer(nextId: string) {
    if (nextId === serverId) return;
    setStatus(null);
    setServerId(nextId);
    try {
      if (nextId) localStorage.setItem("sweeparr_server_id", nextId);
    } catch {
      // ignore storage errors
    }
  }

  async function refreshServers() {
    const res = await api<{ servers: ServerSummary[] }>("/api/servers");
    const list = res.servers || [];
    setServers(list);
    setServersLoaded(true);
    if (list.length === 0) {
      setServerId("");
      setStatus(null);
      return;
    }
    if (!serverId || !list.some((s) => s.id === serverId)) {
      const firstId = list[0].id;
      setServerId(firstId);
      try {
        localStorage.setItem("sweeparr_server_id", firstId);
      } catch {
        // ignore storage errors
      }
    }
  }

  async function refreshStatus(sid = serverId) {
    if (!sid) return;
    const st = await serverApi<ServerStatus>(sid, "/status");
    setStatus(st);
  }

  useEffect(() => {
    void refreshAuth()
      .then((st) => {
        if (st.authenticated) {
          return refreshServers();
        }
      })
      .catch((err) => setLoadError((err as Error).message || "Failed to connect to Sweeparr"));
  }, []);

  useEffect(() => {
    if (!authStatus?.authenticated || !serverId) return;
    const es = new EventSource(serverPath(serverId, "/stream"));
    es.addEventListener("status", (ev) => {
      try {
        setStatus(JSON.parse((ev as MessageEvent).data) as ServerStatus);
      } catch {
        // ignore parse errors
      }
    });
    es.onerror = () => {
      void refreshStatus(serverId).catch(() => {});
    };
    return () => es.close();
  }, [authStatus?.authenticated, serverId]);

  if (loadError) {
    return (
      <main className="auth-shell">
        <div className="auth-card" role="alert">
          <h2 style={{ margin: 0 }}>{t("error.conn_title")}</h2>
          <div className="err-banner">{loadError}</div>
          <button type="button" className="btn btn-primary" onClick={() => window.location.reload()}>
            {t("error.retry")}
          </button>
        </div>
      </main>
    );
  }

  if (!authStatus) {
    return (
      <main className="auth-shell muted" role="status" aria-live="polite">
        {t("loading.app")}
      </main>
    );
  }

  if (!authStatus.authenticated || !authStatus.user) {
    return (
      <AuthGate
        status={authStatus}
        onSuccess={async () => {
          const st = await refreshAuth();
          if (st.authenticated && !st.user?.must_change_password) await refreshServers();
        }}
      />
    );
  }

  if (authStatus.user.must_change_password) {
    return (
      <ForcedPasswordChangeGate
        username={authStatus.user.username}
        onDone={async () => {
          await refreshAuth();
        }}
        onSignOut={async () => {
          await api("/api/auth/logout", "POST");
          await refreshAuth();
        }}
      />
    );
  }

  const canScan = authStatus.user.role === "admin" || authStatus.user.role === "operator";
  const isAdmin = authStatus.user.role === "admin";
  const activeServer = servers.find((s) => s.id === serverId) || status?.server;

  function openConfirmIgnoreCurrent() {
    setConfirmState({
      open: true,
      title: t("confirm.ignore_title"),
      message: t("confirm.ignore_msg"),
      confirmLabel: t("overview.ignore_current"),
      onConfirm: () => {
        setConfirmState((s) => ({ ...s, open: false }));
        void serverApi(serverId, "/control/ignore-current", "POST").then(() => {
          toast("Blocklisted current item");
          void refreshStatus();
        });
      },
    });
  }

  function openConfirmSkip() {
    setConfirmState({
      open: true,
      title: t("confirm.skip_title"),
      message: t("confirm.skip_msg"),
      confirmLabel: t("overview.skip_current"),
      onConfirm: () => {
        setConfirmState((s) => ({ ...s, open: false }));
        void serverApi(serverId, "/control/step", "POST").then(() => {
          toast("Triggered next queued item");
          void refreshStatus();
        });
      },
    });
  }

  function openConfirmInterrupt() {
    setConfirmState({
      open: true,
      title: t("confirm.interrupt_title"),
      message: t("confirm.interrupt_msg"),
      confirmLabel: t("confirm.interrupt_btn"),
      onConfirm: () => {
        setConfirmState((s) => ({ ...s, open: false }));
        void serverApi<{ cancelled: number; non_cancellable?: number }>(serverId, "/plex/activities/cancel-all", "POST").then((r) => {
          const nc = r.non_cancellable || 0;
          if (nc > 0) {
            toast(`Paused scanner (${r.cancelled ?? 0} cancelled; ${nc} non-cancellable Plex task(s) finishing in background)`);
          } else {
            toast(`Interrupted ${r.cancelled ?? 0} active task(s) and paused scanner`);
          }
          void refreshStatus();
        });
      },
    });
  }

  function openConfirmScanLib(lib: string) {
    const isAll = !lib;
    setConfirmState({
      open: true,
      title: isAll ? t("confirm.scan_all_title") : t("confirm.scan_lib_title", { library: lib }),
      message: isAll ? t("confirm.scan_all_msg") : t("confirm.scan_lib_msg", { library: lib }),
      confirmLabel: isAll ? t("overview.scan_all_libs") : `${t("explorer.scan")} ${lib}`,
      onConfirm: () => {
        setConfirmState((s) => ({ ...s, open: false }));
        void serverApi<{ queued: number }>(serverId, "/libraries/scan", "POST", { library: lib }).then((r) => {
          toast(isAll ? `Queued ${r.queued} item(s) across all libraries` : `Queued ${r.queued} item(s) in ${lib}`);
          void refreshStatus();
        });
      },
    });
  }

  const localeFmt = createLocaleFormatter(
    status?.config.number_locale,
    status?.config.datetime_locale,
    status?.config.time_format
  );

  return (
    <LocaleFormatProvider
      numberLocale={status?.config.number_locale}
      dateLocale={status?.config.datetime_locale}
      timeFormat={status?.config.time_format}
    >
      <div className="app-layout">
        <a href="#main-content" className="skip-link">
          {t("a11y.skip_to_main")}
        </a>

        {/* Mobile & Tablet Top Bar */}
        <header className="mobile-topbar">
          <div className="mobile-topbar-left">
            <button
              type="button"
              className="mobile-menu-btn"
              aria-label={t("a11y.open_menu")}
              aria-expanded={mobileMenuOpen}
              onClick={() => setMobileMenuOpen(true)}
            >
              <Menu size={18} strokeWidth={1.5} aria-hidden="true" />
            </button>
            <button
              type="button"
              className="sidebar-brand"
              onClick={() => navigateTo("overview")}
            >
              <Activity size={20} strokeWidth={1.75} style={{ color: "var(--accent-ink)" }} aria-hidden="true" />
              <span className="brand-title">{t("brand.title")}</span>
            </button>
          </div>
          <button
            type="button"
            className="pill"
            aria-label={t("a11y.inspect_streams")}
            onClick={() => setStreamsOpen(true)}
          >
            <span className={`dot ${(status?.plex.active_streams || 0) > 0 ? "live" : ""}`} aria-hidden="true" />
            <span className="mono">{localeFmt.fmtNum(status?.plex.active_streams || 0)}</span>
          </button>
        </header>

      <div
        className={`sidebar-backdrop ${mobileMenuOpen ? "open" : ""}`}
        aria-hidden="true"
        onClick={() => setMobileMenuOpen(false)}
      />

      {/* Collapsible Left Sidebar / Mobile Slide-Over Drawer */}
      <aside
        className={`sidebar ${sidebarCollapsed ? "collapsed" : ""} ${mobileMenuOpen ? "mobile-open" : ""}`}
        aria-label={t("a11y.primary_sidebar")}
      >
        <div className="sidebar-head-row">
          <button
            type="button"
            className="sidebar-brand"
            aria-label={t("nav.overview")}
            onClick={() => navigateTo("overview")}
          >
            <Activity size={22} strokeWidth={1.75} style={{ color: "var(--accent-ink)", flexShrink: 0 }} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && (
              <div className="brand-title">{t("brand.title")}</div>
            )}
          </button>
          <button
            type="button"
            className="sidebar-collapse-btn"
            aria-label={sidebarCollapsed ? t("a11y.expand_sidebar") : t("a11y.collapse_sidebar")}
            aria-expanded={!sidebarCollapsed}
            onClick={toggleSidebar}
          >
            {sidebarCollapsed ? <PanelLeftOpen size={16} strokeWidth={1.5} aria-hidden="true" /> : <PanelLeftClose size={16} strokeWidth={1.5} aria-hidden="true" />}
          </button>
          <button
            type="button"
            className="mobile-close-btn"
            aria-label={t("a11y.close_menu")}
            onClick={() => setMobileMenuOpen(false)}
          >
            <X size={16} strokeWidth={1.5} aria-hidden="true" />
          </button>
        </div>

        {/* Grouped Workspace Preferences: Theme -> Language -> Account */}
        <div className="sidebar-section-group">
          {(!sidebarCollapsed || mobileMenuOpen) && (
            <div className="sidebar-section-title">{t("sidebar.workspace")}</div>
          )}
          <ThemeMenu mode={themeMode} collapsed={sidebarCollapsed && !mobileMenuOpen} direction="down" onChange={updateThemeMode} />
          <LanguageMenu collapsed={sidebarCollapsed && !mobileMenuOpen} direction="down" />
          <a
            href={VIEW_PATHS.account}
            className={`nav-item sidebar-lang-btn ${view === "account" ? "active" : ""}`}
            aria-label={t("nav.account")}
            aria-current={view === "account" ? "page" : undefined}
            title={sidebarCollapsed && !mobileMenuOpen ? `${t("nav.account")} (${authStatus.user.username})` : undefined}
            onClick={(e) => navigateTo("account", e)}
          >
            <UserIcon size={17} strokeWidth={1.6} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && (
              <>
                <span className="sidebar-item-label">{authStatus.user.username}</span>
                <span className="lang-code-pill" style={{ marginLeft: "auto" }}>
                  {t(`role.${authStatus.user.role}`)}
                </span>
              </>
            )}
          </a>
        </div>

        {/* Styled Active Server Switcher */}
        {servers.length > 0 && (!sidebarCollapsed || mobileMenuOpen) && (
          <ServerMenu
            servers={servers}
            serverId={serverId}
            onSelect={(id) => {
              selectServer(id);
              setMobileMenuOpen(false);
            }}
          />
        )}

        <nav className="sidebar-nav" aria-label={t("a11y.main_nav")}>
          <a
            href={VIEW_PATHS.overview}
            className={`nav-item ${view === "overview" ? "active" : ""}`}
            aria-label={t("nav.overview")}
            aria-current={view === "overview" ? "page" : undefined}
            onClick={(e) => navigateTo("overview", e)}
          >
            <Home size={17} strokeWidth={1.5} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && <span>{t("nav.overview")}</span>}
          </a>
          <a
            href={`/activity/${activityTab}`}
            className={`nav-item ${view === "queue" ? "active" : ""}`}
            aria-label={t("nav.queue")}
            aria-current={view === "queue" ? "page" : undefined}
            onClick={(e) => navigateTo("queue", e)}
          >
            <Layers size={17} strokeWidth={1.5} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && <span>{t("nav.queue")}</span>}
          </a>
          <a
            href={VIEW_PATHS.access}
            className={`nav-item ${view === "access" ? "active" : ""}`}
            aria-label={t("nav.access")}
            aria-current={view === "access" ? "page" : undefined}
            onClick={(e) => navigateTo("access", e)}
          >
            <Shield size={17} strokeWidth={1.5} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && <span>{t("nav.access")}</span>}
          </a>
          <a
            href={VIEW_PATHS.settings}
            className={`nav-item ${view === "settings" ? "active" : ""}`}
            aria-label={t("nav.settings")}
            aria-current={view === "settings" ? "page" : undefined}
            onClick={(e) => navigateTo("settings", e)}
          >
            <Settings size={17} strokeWidth={1.5} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && <span>{t("nav.settings")}</span>}
          </a>
        </nav>

        <div className="sidebar-foot">
          <a
            href={VIEW_PATHS.docs}
            className={`nav-item ${view === "docs" ? "active" : ""}`}
            aria-label={t("nav.docs")}
            aria-current={view === "docs" ? "page" : undefined}
            onClick={(e) => navigateTo("docs", e)}
          >
            <BookOpen size={17} strokeWidth={1.5} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && <span>{t("nav.docs")}</span>}
          </a>
          {isAdmin && versionInfo && safeExternalURL(versionInfo.release_url) && (
            <a
              href={safeExternalURL(versionInfo.release_url)!}
              target="_blank"
              rel="noopener noreferrer"
              className="version-pill"
              title={
                !versionInfo.update_check_enabled
                  ? `v${versionInfo.current_version} — ${t("settings.update_check_offline_badge")}`
                  : versionInfo.update_available
                    ? `Update available: v${versionInfo.latest_version}`
                    : `Up to date (v${versionInfo.current_version})`
              }
            >
              <span
                className={`dot ${!versionInfo.update_check_enabled ? "" : versionInfo.update_available ? "warn" : "live"}`}
                aria-hidden="true"
              />
              {(!sidebarCollapsed || mobileMenuOpen) && <span className="tabular-nums small">v{versionInfo.current_version}</span>}
            </a>
          )}
          <button
            type="button"
            className="btn btn-ghost btn-sm btn-signout"
            aria-label={t("sidebar.sign_out")}
            style={{ width: "100%", justifyContent: sidebarCollapsed && !mobileMenuOpen ? "center" : "flex-start" }}
            onClick={async () => {
              await api("/api/auth/logout", "POST");
              await refreshAuth();
            }}
          >
            <LogOut size={15} aria-hidden="true" />
            {(!sidebarCollapsed || mobileMenuOpen) && <span>{t("sidebar.sign_out")}</span>}
          </button>
        </div>
      </aside>

      <main id="main-content" className="app-main" tabIndex={-1}>
        {!isOnline && (
          <div className="err-banner" role="status" style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <WifiOff size={15} aria-hidden="true" />
            <span>Offline or poor network connection — retrying automatically when connectivity returns…</span>
          </div>
        )}
        {view === "overview" && (
          <header className="topbar">
            <h1 className="topbar-title">{t(VIEW_TITLE_KEYS[view])}</h1>
            <div className="topbar-actions">
              <button
                type="button"
                className="pill"
                aria-label={t("a11y.inspect_streams")}
                onClick={() => setStreamsOpen(true)}
              >
                <span className={`dot ${(status?.plex.active_streams || 0) > 0 ? "live" : ""}`} aria-hidden="true" />
                <span>
                  {status?.plex.active_streams === 1
                    ? t("topbar.stream_one", { count: localeFmt.fmtNum(1) as unknown as number })
                    : t("topbar.stream_many", { count: localeFmt.fmtNum(status?.plex.active_streams || 0) as unknown as number })}
                </span>
              </button>
            </div>
          </header>
        )}

        {serversLoaded && servers.length === 0 && (view === "overview" || view === "queue" || view === "settings") && (
          <div className="card" role="status" style={{ padding: 20 }}>
            <h3 style={{ marginTop: 0 }}>{t("error.no_servers")}</h3>
          </div>
        )}
        <Suspense fallback={<div className="card" role="status">{t("loading.app")}</div>}>
          {view === "overview" && status && (
            <OverviewView
              key={serverId}
              serverId={serverId}
              status={status}
              canScan={canScan}
              onOpenSync={() => setSyncOpen(true)}
              onConfirmSkip={openConfirmSkip}
              onConfirmIgnoreCurrent={openConfirmIgnoreCurrent}
              onConfirmInterrupt={openConfirmInterrupt}
              onConfirmScanLib={openConfirmScanLib}
              onNavigateHistory={navigateToHistory}
              onRefreshStatus={() => void refreshStatus()}
              toast={toast}
            />
          )}
          {view === "queue" && serverId && (
            <QueueView
              key={serverId}
              serverId={serverId}
              libraries={status?.libraries || []}
              catalogVersion={status?.catalog_version || 0}
              canScan={canScan}
              dateLocale={status?.config.datetime_locale || "de-DE"}
              timeFormat={status?.config.time_format || "24h"}
              activeTab={activityTab}
              onTabChange={handleActivityTabChange}
              initialHistoryStatus={historyStatusFilter}
              onConsumeHistoryStatus={() => setHistoryStatusFilter("")}
              currentItemStartedAt={status?.daemon.current_item_started_at || 0}
              pendingTriggers={status?.daemon.pending_triggers || []}
              onConfirmSkip={openConfirmSkip}
              toast={toast}
            />
          )}
          {view === "settings" && status && (
            <SettingsView
              key={serverId}
              serverId={serverId}
              servers={servers}
              status={status}
              isAdmin={isAdmin}
              canScan={canScan}
              themeMode={themeMode}
              accentColor={accentColor}
              onThemeModeChange={updateThemeMode}
              onAccentColorChange={updateAccentColor}
              onDirtyChange={setHasUnsavedChanges}
              onSelectServer={(id) => {
                selectServer(id);
                toast("Switched active server");
              }}
              onServersChanged={async () => {
                await refreshServers();
                await refreshStatus();
              }}
              onOpenSync={() => setSyncOpen(true)}
              toast={toast}
            />
          )}
          {view === "access" && (
            <AccessView
              user={authStatus.user}
              servers={servers}
              libraries={status?.libraries || []}
              onDirtyChange={setHasUnsavedChanges}
              toast={toast}
            />
          )}
          {view === "account" && (
            <AccountView
              user={authStatus.user}
              oidcEnabled={authStatus.oidc_enabled}
              onDirtyChange={setHasUnsavedChanges}
              onUserRefresh={async () => {
                await refreshAuth();
                await refreshServers();
              }}
              toast={toast}
            />
          )}
          {view === "docs" && <ApiDocsView serverId={serverId} />}
        </Suspense>
      </main>

      <ConfirmModal
        open={confirmState.open}
        title={confirmState.title}
        message={confirmState.message}
        confirmLabel={confirmState.confirmLabel}
        onConfirm={confirmState.onConfirm}
        onCancel={() => setConfirmState((s) => ({ ...s, open: false }))}
      />

      <SyncModal
        open={syncOpen}
        onCancel={() => setSyncOpen(false)}
        onDelta={() => {
          setSyncOpen(false);
          void serverApi(serverId, "/control/refresh", "POST").then(() => {
            toast("Delta sync queued");
            void refreshStatus();
          });
        }}
        onFull={() => {
          setSyncOpen(false);
          void serverApi(serverId, "/control/refresh?full=true", "POST").then(() => {
            toast("Full re-index queued");
            void refreshStatus();
          });
        }}
      />

      <StreamsModal
        open={streamsOpen}
        serverId={serverId}
        server={activeServer}
        sessions={status?.plex.sessions || []}
        transcodes={status?.plex.active_transcodes || 0}
        onClose={() => setStreamsOpen(false)}
      />

      {toasts.length > 0 && (
        <div className="toast-stack" role="region" aria-label="Notifications" aria-live="polite">
          {toasts.map((item) => (
            <div key={item.id} className={`toast-item toast-${item.tone}`} role="status">
              <span className="toast-icon" aria-hidden="true">
                {item.tone === "error" ? (
                  <AlertCircle size={15} strokeWidth={1.75} />
                ) : item.tone === "success" ? (
                  <CheckCircle2 size={15} strokeWidth={1.75} />
                ) : (
                  <Info size={15} strokeWidth={1.75} />
                )}
              </span>
              <span className="toast-msg">{item.message}</span>
              <button
                type="button"
                className="toast-close"
                aria-label={t("modal.close")}
                onClick={() => dismissToast(item.id)}
              >
                <X size={14} aria-hidden="true" />
              </button>
            </div>
          ))}
        </div>
      )}
      </div>
    </LocaleFormatProvider>
  );
}
