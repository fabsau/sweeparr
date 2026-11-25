import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, ChevronDown, ChevronRight, Copy, ExternalLink, KeyRound, RefreshCw, ShieldAlert, Webhook } from "lucide-react";
import { InfoPopover } from "../components/Controls";
import { useLocaleFormat } from "../utils/format";

interface OpenAPIOp {
  summary?: string;
  description?: string;
  tags?: string[];
  security?: Record<string, string[]>[];
}

const METHOD_COLORS: Record<string, string> = {
  GET: "st-active",
  POST: "st-complete",
  PUT: "st-paused",
  PATCH: "st-queued",
  DELETE: "st-exhausted",
};

export function ApiDocsView({ serverId = "" }: { serverId?: string }) {
  const { t } = useTranslation();
  const { fmtNum } = useLocaleFormat();
  const [paths, setPaths] = useState<Record<string, Record<string, OpenAPIOp>>>({});
  const [baseUrl, setBaseUrl] = useState<string>(() =>
    typeof window !== "undefined" ? window.location.origin : "https://your-sweeparr-host"
  );
  const [search, setSearch] = useState("");
  const [methodFilter, setMethodFilter] = useState("ALL");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [copiedId, setCopiedId] = useState("");

  useEffect(() => {
    fetch("/api/openapi.json", { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : {}) as Promise<{ paths?: Record<string, Record<string, OpenAPIOp>>; servers?: { url?: string }[] }>)
      .then((d) => {
        setPaths(d.paths || {});
        if (Array.isArray(d.servers) && d.servers[0]?.url) {
          setBaseUrl(String(d.servers[0].url).replace(/\/+$/, ""));
        }
      })
      .catch(() => {});
  }, []);

  function copyText(id: string, text: string) {
    if (navigator.clipboard) {
      void navigator.clipboard.writeText(text);
    }
    setCopiedId(id);
    window.setTimeout(() => setCopiedId((prev) => (prev === id ? "" : prev)), 1800);
  }

  const activeServer = serverId || "{serverID}";
  const resolvePath = (p: string) => p.replace(/\{serverID\}/g, activeServer);

  const plexWebhookUrl = `${baseUrl}/api/v1/servers/${activeServer}/webhooks/plex?api_key=$SWEEPARR_API_KEY`;
  const arrWebhookUrl = `${baseUrl}/api/v1/servers/${activeServer}/webhooks/arr?api_key=$SWEEPARR_API_KEY`;
  const syncCmd = `curl -X POST "${baseUrl}/api/v1/servers/${activeServer}/sync?mode=delta" \\\n  -H "X-Api-Key: $SWEEPARR_API_KEY"`;

  const allEndpoints = Object.entries(paths).flatMap(([path, methods]) =>
    Object.entries(methods).map(([method, op]) => ({
      id: `${method.toUpperCase()}:${path}`,
      method: method.toUpperCase(),
      path,
      summary: op.summary || "",
      description: op.description || "",
    }))
  );

  const availableMethods = [
    "ALL",
    ...Array.from(new Set(allEndpoints.map((e) => e.method))).sort((a, b) => {
      const order = ["GET", "POST", "PUT", "PATCH", "DELETE"];
      return order.indexOf(a) - order.indexOf(b);
    }),
  ];

  const endpoints = allEndpoints
    .filter((ep) => {
      if (methodFilter !== "ALL" && ep.method !== methodFilter) return false;
      const q = search.trim().toLowerCase();
      if (!q) return true;
      return (
        ep.path.toLowerCase().includes(q) ||
        ep.method.toLowerCase().includes(q) ||
        ep.summary.toLowerCase().includes(q) ||
        ep.description.toLowerCase().includes(q)
      );
    })
    .sort((a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method));

  return (
    <div className="page-view unified-page">
      {/* Header & Base URL */}
      <div className="card form-section">
        <div className="section-head-row">
          <div>
            <h3>{t("docs.title")}</h3>
            <div className="small muted">{t("docs.subtitle")}</div>
          </div>
          <div className="action-group">
            <span className="badge mono">{baseUrl}</span>
            <a
              className="btn btn-sm btn-blue"
              href="/api/openapi.json"
              target="_blank"
              rel="noreferrer"
              style={{ textDecoration: "none" }}
            >
              <ExternalLink size={13} aria-hidden="true" />
              <span>{t("docs.openapi_btn")}</span>
            </a>
          </div>
        </div>
      </div>

      {/* Authentication, RBAC & Rate Limiting */}
      <div className="card form-section">
        <div className="action-group" style={{ gap: 8 }}>
          <KeyRound size={16} color="var(--accent)" aria-hidden="true" />
          <h3 style={{ margin: 0 }}>Authentication, Roles &amp; Rate Limiting</h3>
          <InfoPopover title="Authentication, Roles & Rate Limiting" text={t("docs.auth.tip")} />
        </div>
        <div className="table-wrap">
          <table className="media-table">
            <thead>
              <tr>
                <th scope="col" style={{ width: "22%" }}>Role / Scope</th>
                <th scope="col" style={{ width: "26%" }}>Mechanism</th>
                <th scope="col">Permitted Operations</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td><span className="badge st-pending">viewer</span> / <span className="badge st-pending">read</span></td>
                <td>Session Cookie / X-Api-Key</td>
                <td>Read status, libraries, media items, groups, queue, history, blocklist, logs, exports, and OpenAPI docs (scoped to allowed servers).</td>
              </tr>
              <tr>
                <td><span className="badge st-queued">operator</span> / <span className="badge st-queued">scan</span></td>
                <td>Session Cookie / X-Api-Key</td>
                <td>All read endpoints plus library/item scans, delta &amp; full catalog sync, pause/resume, queue reorder, blocklist/history management, and scanner settings (scoped to allowed servers).</td>
              </tr>
              <tr>
                <td><span className="badge st-complete">webhook</span></td>
                <td>X-Api-Key / ?api_key=</td>
                <td>Receive native Plex <code className="mono">library.new</code> webhooks on <code className="mono">/api/v1/servers/{activeServer}/webhooks/plex</code> (or multi-server <code className="mono">/api/v1/webhooks/plex</code>), scoped to the key&apos;s allowed servers.</td>
              </tr>
              <tr>
                <td><span className="badge st-paused">admin</span></td>
                <td>Session Cookie</td>
                <td>Full administrative control across all servers, including managing users, roles, OIDC/SSO, security policies, and per-user server access.</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div className="ro-info-card small muted" style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
          <ShieldAlert size={15} color="var(--accent)" aria-hidden="true" />
          <span>
            Authentication endpoints enforce progressive brute-force rate limiting (12 req/min per IP, escalating 2–15 min lockouts with <code className="mono">429 Too Many Requests</code> &amp; <code className="mono">Retry-After</code>). All secrets are encrypted at rest with AES-256-GCM.
          </span>
        </div>
      </div>

      {/* Webhook & Sync Integrations */}
      <div className="hero-grid">
        <div className="card form-section">
          <div className="section-head-row">
            <div className="action-group" style={{ gap: 8 }}>
              <Webhook size={15} color="var(--green)" aria-hidden="true" />
              <h3 style={{ margin: 0 }}>*arr Auto-Scan Webhook (Sonarr / Radarr / Lidarr)</h3>
              <InfoPopover title="*arr Auto-Scan Webhook" text={t("docs.arr_webhook.tip")} />
            </div>
            <button type="button" className="btn btn-xs btn-ghost" onClick={() => copyText("arr", arrWebhookUrl)}>
              {copiedId === "arr" ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
              <span>{copiedId === "arr" ? "Copied" : "Copy"}</span>
            </button>
          </div>
          <p className="small muted" style={{ margin: 0 }}>
            Add this URL under <strong>Settings → Connect → Webhook</strong> in Sonarr, Radarr, or Lidarr (enable <em>On Import</em>, <em>On Upgrade</em>, and <em>On Rename</em>). Sweeparr coalesces full season/album imports, triggers a targeted Plex directory refresh (similar to tools like Autoscan or Autopulse), waits for pickup, and automatically queues capability analysis.
          </p>
          <pre className="ro-info-card mono small" style={{ overflowX: "auto", margin: 0 }}>{arrWebhookUrl}</pre>
        </div>

        <div className="card form-section">
          <div className="section-head-row">
            <div className="action-group" style={{ gap: 8 }}>
              <Webhook size={15} color="var(--accent-ink)" aria-hidden="true" />
              <h3 style={{ margin: 0 }}>Native Plex Webhook</h3>
              <InfoPopover title="Native Plex Webhook" text={t("docs.plex_webhook.tip")} />
            </div>
            <button type="button" className="btn btn-xs btn-ghost" onClick={() => copyText("plex", plexWebhookUrl)}>
              {copiedId === "plex" ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
              <span>{copiedId === "plex" ? "Copied" : "Copy"}</span>
            </button>
          </div>
          <p className="small muted" style={{ margin: 0 }}>
            Create a server-scoped API key with the <code className="mono">webhook</code> scope in <strong>Access</strong>, then paste this URL into <strong>Plex Settings → Webhooks</strong>. Whenever Plex finishes adding a new movie, episode, or track (<code className="mono">library.new</code>), Sweeparr inspects that exact item and queues it for analysis immediately.
          </p>
          <pre className="ro-info-card mono small" style={{ overflowX: "auto", margin: 0 }}>{plexWebhookUrl}</pre>
        </div>
      </div>

      <div className="card form-section">
        <div className="section-head-row">
          <div className="action-group" style={{ gap: 8 }}>
            <RefreshCw size={15} color="var(--cyan)" aria-hidden="true" />
            <h3 style={{ margin: 0 }}>Programmatic Catalog Sync</h3>
            <InfoPopover title="Programmatic Catalog Sync" text={t("docs.sync_api.tip")} />
          </div>
          <button type="button" className="btn btn-xs btn-ghost" onClick={() => copyText("sync", syncCmd)}>
            {copiedId === "sync" ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
            <span>{copiedId === "sync" ? "Copied" : "Copy"}</span>
          </button>
        </div>
        <p className="small muted" style={{ margin: 0 }}>
          Trigger a catalog sync on demand using an API key with the <code className="mono">scan</code> scope. Use <code className="mono">mode=delta</code> for fast incremental updates or <code className="mono">mode=full</code> for a complete library re-index.
        </p>
        <pre className="ro-info-card mono small" style={{ overflowX: "auto", margin: 0 }}>{syncCmd}</pre>
      </div>

      {/* Interactive REST Endpoints Explorer */}
      <div className="card form-section">
        <div className="section-head-row">
          <div className="field-label-row">
            <h3>{t("docs.endpoints_heading")} ({fmtNum(endpoints.length)})</h3>
            <InfoPopover title={t("docs.endpoints_heading")} text={t("docs.endpoints.tip")} />
          </div>
          <div className="action-group">
            {availableMethods.map((m) => (
              <button
                key={m}
                type="button"
                className={`btn btn-xs ${methodFilter === m ? "btn-primary" : "btn-ghost"}`}
                onClick={() => setMethodFilter(m)}
              >
                {m}
              </button>
            ))}
          </div>
        </div>
        <div className="explorer-toolbar" style={{ marginBottom: 4 }}>
          <input
            type="search"
            aria-label="Filter API endpoints"
            placeholder="Filter endpoints by path, method, or description…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {endpoints.map((ep) => {
            const isOpen = Boolean(expanded[ep.id]);
            const resolved = resolvePath(ep.path);
            const sampleCurl = `curl -X ${ep.method} "${baseUrl}${resolved}" \\\n  -H "X-Api-Key: $SWEEPARR_API_KEY"`;
            return (
              <div key={ep.id} className="ro-info-card" style={{ gap: 8 }}>
                <div
                  className="section-head-row"
                  style={{ cursor: "pointer" }}
                  onClick={() => setExpanded((prev) => ({ ...prev, [ep.id]: !prev[ep.id] }))}
                >
                  <div className="action-group" style={{ gap: 8 }}>
                    {isOpen ? <ChevronDown size={14} className="muted" aria-hidden="true" /> : <ChevronRight size={14} className="muted" aria-hidden="true" />}
                    <span className={`badge ${METHOD_COLORS[ep.method] || "st-pending"} mono`} style={{ minWidth: 58, justifyContent: "center" }}>
                      {ep.method}
                    </span>
                    <code className="mono strong">{resolved}</code>
                  </div>
                  <span className="small muted">{ep.summary}</span>
                </div>
                {isOpen && (
                  <div style={{ display: "flex", flexDirection: "column", gap: 8, paddingTop: 6, borderTop: "1px solid var(--border)" }}>
                    {ep.description && <div className="small muted">{ep.description}</div>}
                    <div className="section-head-row">
                      <pre className="mono small" style={{ margin: 0, overflowX: "auto", flex: 1 }}>{sampleCurl}</pre>
                      <button
                        type="button"
                        className="btn btn-xs btn-ghost"
                        onClick={() => copyText(ep.id, sampleCurl)}
                      >
                        {copiedId === ep.id ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
                        <span>{copiedId === ep.id ? "Copied" : "Copy"}</span>
                      </button>
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
