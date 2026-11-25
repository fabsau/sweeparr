import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  CheckCircle2,
  KeyRound,
  Link2Off,
  LockKeyhole,
  LogOut,
  Pencil,
  Save,
  ShieldCheck,
  ShieldOff,
  Trash2,
  UserPlus,
} from "lucide-react";
import { api } from "../api";
import { useLocaleFormat } from "../utils/format";
import { stripControlChars } from "../utils/sanitize";
import { Checkbox, ConfirmModal, InfoPopover, SelectMenu } from "../components/Controls";
import type { APIKeyRecord, LibrarySummary, OIDCSettings, ServerSummary, User } from "../types";

interface UserDraft {
  role: "admin" | "operator" | "viewer";
  allowed_servers: string[];
  can_create_api_keys: boolean;
  require_mfa: boolean;
  require_passkey: boolean;
  password_login_disabled: boolean;
}

function userToDraft(u: User): UserDraft {
  return {
    role: u.role,
    allowed_servers: [...(u.allowed_servers || [])].filter((x) => x !== "*").sort(),
    can_create_api_keys: Boolean(u.can_create_api_keys),
    require_mfa: Boolean(u.require_mfa),
    require_passkey: Boolean(u.require_passkey),
    password_login_disabled: Boolean(u.password_login_disabled),
  };
}

function draftsEqual(a: UserDraft, b: UserDraft): boolean {
  return (
    a.role === b.role &&
    a.can_create_api_keys === b.can_create_api_keys &&
    a.require_mfa === b.require_mfa &&
    a.require_passkey === b.require_passkey &&
    a.password_login_disabled === b.password_login_disabled &&
    JSON.stringify([...a.allowed_servers].sort()) === JSON.stringify([...b.allowed_servers].sort())
  );
}

const EMPTY_OIDC: OIDCSettings = {
  enabled: false,
  issuer: "",
  client_id: "",
  has_client_secret: false,
  redirect_url: "",
  auto_create: false,
  groups_claim: "groups",
  admin_groups: "",
  operator_groups: "",
  viewer_groups: "",
};

export function AccessView({
  user,
  servers,
  libraries = [],
  onDirtyChange,
  toast,
}: {
  user: User;
  servers: ServerSummary[];
  libraries?: LibrarySummary[];
  onDirtyChange?: (dirty: boolean) => void;
  toast: (msg: string) => void;
}) {
  const { t } = useTranslation();
  const { fmtNum, fmtDate } = useLocaleFormat();

  // API Keys state — least privilege: no scopes pre-checked
  const [apiKeys, setApiKeys] = useState<APIKeyRecord[]>([]);
  const [akName, setAkName] = useState("");
  const [akScopes, setAkScopes] = useState<string[]>([]);
  const [akExpiry, setAkExpiry] = useState("0");
  const [akServers, setAkServers] = useState<string[]>([]);
  const [akLibraries, setAkLibraries] = useState<string[]>([]);
  const [newRawKey, setNewRawKey] = useState("");
  const [renameKey, setRenameKey] = useState<{ id: string; value: string } | null>(null);

  // Users state + per-user drafts
  const [users, setUsers] = useState<User[]>([]);
  const [userDrafts, setUserDrafts] = useState<Record<string, UserDraft>>({});
  const [savingUserId, setSavingUserId] = useState<string | null>(null);
  const [newUser, setNewUser] = useState<{
    username: string;
    password: string;
    display_name: string;
    role: string;
    allowed_servers: string[];
    can_create_api_keys: boolean;
    require_mfa: boolean;
    require_passkey: boolean;
  }>({
    username: "",
    password: "",
    display_name: "",
    role: "viewer",
    allowed_servers: [],
    can_create_api_keys: false,
    require_mfa: false,
    require_passkey: false,
  });

  // OIDC config state (admin)
  const [oidcSaved, setOidcSaved] = useState<OIDCSettings>(EMPTY_OIDC);
  const [oidcDraft, setOidcDraft] = useState<OIDCSettings>(EMPTY_OIDC);
  const [oidcSecret, setOidcSecret] = useState("");
  const [oidcTesting, setOidcTesting] = useState(false);
  const [oidcSaving, setOidcSaving] = useState(false);

  const [confirmAction, setConfirmAction] = useState<{
    open: boolean;
    title: string;
    message: string;
    confirmLabel: string;
    onConfirm: () => Promise<void>;
  } | null>(null);
  const [confirmBusy, setConfirmBusy] = useState(false);

  const [pwModal, setPwModal] = useState<{
    user: User;
    password: string;
    mustChange: boolean;
  } | null>(null);

  const canCreateKeys = user.role === "admin" || Boolean(user.can_create_api_keys);

  async function load() {
    const k = await api<{ api_keys: APIKeyRecord[] }>("/api/api-keys");
    setApiKeys(k.api_keys || []);
    if (user.role === "admin") {
      const [u, o] = await Promise.all([
        api<{ users: User[] }>("/api/users"),
        api<OIDCSettings>("/api/admin/oidc").catch(() => EMPTY_OIDC),
      ]);
      const list = u.users || [];
      setUsers(list);
      const nextDrafts: Record<string, UserDraft> = {};
      for (const usr of list) {
        nextDrafts[usr.id] = userToDraft(usr);
      }
      setUserDrafts(nextDrafts);
      setOidcSaved(o);
      setOidcDraft(o);
      setOidcSecret("");
    }
  }

  useEffect(() => {
    void load();
  }, [user.id]);

  const anyUserDirty = users.some((u) => {
    const d = userDrafts[u.id];
    return d ? !draftsEqual(d, userToDraft(u)) : false;
  });
  const oidcDirty =
    user.role === "admin" &&
    (JSON.stringify(oidcDraft) !== JSON.stringify(oidcSaved) || oidcSecret.trim() !== "");

  useEffect(() => {
    onDirtyChange?.(anyUserDirty || oidcDirty || Boolean(renameKey));
    return () => onDirtyChange?.(false);
  }, [anyUserDirty, oidcDirty, renameKey, onDirtyChange]);

  const roleOptions = [
    { value: "viewer", label: t("security.role_viewer") },
    { value: "operator", label: t("security.role_operator") },
    { value: "admin", label: t("security.role_admin") },
  ];

  const expiryOptions = [
    { value: "0", label: t("access.expiry_never") },
    { value: "30", label: t("access.expiry_days", { count: fmtNum(30) as unknown as number }) },
    { value: "90", label: t("access.expiry_days", { count: fmtNum(90) as unknown as number }) },
    { value: "365", label: t("access.expiry_year") },
  ];

  function patchDraft(userId: string, patch: Partial<UserDraft>) {
    setUserDrafts((prev) => {
      const cur = prev[userId];
      if (!cur) return prev;
      return { ...prev, [userId]: { ...cur, ...patch } };
    });
  }

  async function saveUser(u: User) {
    const d = userDrafts[u.id];
    if (!d) return;
    setSavingUserId(u.id);
    try {
      await api(`/api/users/${u.id}`, "PATCH", {
        role: d.role,
        allowed_servers: d.role === "admin" ? [] : d.allowed_servers,
        can_create_api_keys: d.role === "admin" ? true : d.can_create_api_keys,
        require_mfa: d.require_mfa,
        require_passkey: d.require_passkey,
        password_login_disabled: d.password_login_disabled,
      });
      toast(t("access.user_saved", { username: u.username }));
      await load();
    } catch (err) {
      toast((err as Error).message);
    } finally {
      setSavingUserId(null);
    }
  }

  return (
    <div className="page-view unified-page">
      {/* API Keys */}
      <div className="card form-section">
        <div className="field-label-row">
          <h3>{t("security.apikeys_heading")}</h3>
          <InfoPopover title={t("security.apikeys_heading")} text={t("access.apikeys.tip")} />
        </div>
        {canCreateKeys ? (
          <div className="ro-info-card" style={{ gap: 12 }}>
            <div className="action-group" style={{ flexWrap: "wrap", gap: 10 }}>
              <input
                aria-label={t("security.apikey_placeholder")}
                placeholder={t("security.apikey_placeholder")}
                maxLength={64}
                value={akName}
                onChange={(e) => setAkName(stripControlChars(e.target.value, 64))}
              />
              <div style={{ width: 150 }}>
                <SelectMenu
                  compact
                  ariaLabel={t("access.expiry_label")}
                  value={akExpiry}
                  options={expiryOptions}
                  onChange={setAkExpiry}
                />
              </div>
              {["read", "scan", "webhook", ...(user.role === "admin" ? ["admin"] : [])].map((sc) => (
                <Checkbox
                  key={sc}
                  checked={akScopes.includes(sc)}
                  onChange={(checked) => setAkScopes(checked ? [...akScopes, sc] : akScopes.filter((s) => s !== sc))}
                  label={sc}
                />
              ))}
              <button
                type="button"
                className="btn btn-sm btn-primary"
                disabled={!akName.trim() || akScopes.length === 0}
                onClick={async () => {
                  try {
                    const r = await api<{ api_key: string }>("/api/api-keys", "POST", {
                      name: akName.trim(),
                      scopes: akScopes,
                      expires_in_days: Number(akExpiry) || 0,
                      allowed_servers: akServers,
                      allowed_libraries: akLibraries,
                    });
                    setNewRawKey(r.api_key);
                    setAkName("");
                    setAkScopes([]);
                    setAkExpiry("0");
                    setAkServers([]);
                    setAkLibraries([]);
                    toast(t("access.apikey_created"));
                    await load();
                  } catch (err) {
                    toast((err as Error).message);
                  }
                }}
              >
                <KeyRound size={13} aria-hidden="true" />
                <span>{t("security.apikey_generate")}</span>
              </button>
            </div>
            {servers.length > 0 && (
              <div className="action-group" style={{ gap: 10, flexWrap: "wrap" }}>
                <span className="small muted field-label-row">
                  <span>{t("security.server_access")}:</span>
                  <InfoPopover title={t("security.server_access")} text={t("access.server_scope.tip")} />
                </span>
                <Checkbox
                  checked={akServers.length === 0}
                  onChange={(checked) => {
                    if (checked) setAkServers([]);
                  }}
                  label={t("security.all_servers")}
                />
                {servers.map((s) => {
                  const checked = akServers.includes(s.id);
                  return (
                    <Checkbox
                      key={s.id}
                      checked={checked}
                      onChange={(next) => {
                        setAkServers(next ? [...akServers, s.id] : akServers.filter((id) => id !== s.id));
                      }}
                      label={s.server_name || s.name || s.id}
                    />
                  );
                })}
              </div>
            )}
            {libraries.length > 0 && (
              <div className="action-group" style={{ gap: 10, flexWrap: "wrap" }}>
                <span className="small muted field-label-row">
                  <span>{t("access.library_access")}:</span>
                  <InfoPopover title={t("access.library_access")} text={t("access.library_scope.tip")} />
                </span>
                <Checkbox
                  checked={akLibraries.length === 0}
                  onChange={(checked) => {
                    if (checked) setAkLibraries([]);
                  }}
                  label={t("access.all_libraries")}
                />
                {libraries.map((lib) => {
                  const checked = akLibraries.includes(lib.title);
                  return (
                    <Checkbox
                      key={lib.title}
                      checked={checked}
                      onChange={(next) => {
                        setAkLibraries(next ? [...akLibraries, lib.title] : akLibraries.filter((x) => x !== lib.title));
                      }}
                      label={lib.title}
                    />
                  );
                })}
              </div>
            )}
          </div>
        ) : (
          <div className="small muted">{t("access.apikey_creation_disabled")}</div>
        )}
        {newRawKey && (
          <div className="ro-info-card">
            {t("security.apikey_copy_once")} <code className="mono">{newRawKey}</code>
          </div>
        )}

        <div className="section-head-row" style={{ marginTop: 4 }}>
          <span className="small muted strong">
            {t("access.active_api_keys")} ({fmtNum(apiKeys.length)})
          </span>
        </div>

        {apiKeys.length === 0 ? (
          <div className="small muted">{t("access.no_api_keys")}</div>
        ) : (
          apiKeys.map((k) => {
            const keySrvs = k.allowed_servers || [];
            const isAllSrvs = keySrvs.length === 0 || keySrvs.includes("*");
            const keyLibs = k.allowed_libraries || [];
            const isAllLibs = keyLibs.length === 0 || keyLibs.includes("*");
            const isExpired = Boolean(k.expires_at && k.expires_at > 0 && k.expires_at < Math.floor(Date.now() / 1000));
            return (
              <div key={k.id} className="priority-row">
                <div className="action-group" style={{ gap: 8, flexWrap: "wrap" }}>
                  {renameKey?.id === k.id ? (
                    <div className="action-group" style={{ gap: 6 }}>
                      <input
                        maxLength={64}
                        value={renameKey.value}
                        onChange={(e) => setRenameKey({ ...renameKey, value: stripControlChars(e.target.value, 64) })}
                      />
                      <button
                        type="button"
                        className="btn btn-xs btn-primary"
                        disabled={!renameKey.value.trim()}
                        onClick={async () => {
                          try {
                            await api(`/api/api-keys/${k.id}`, "PATCH", { name: renameKey.value.trim() });
                            setRenameKey(null);
                            toast(t("access.apikey_renamed"));
                            await load();
                          } catch (err) {
                            toast((err as Error).message);
                          }
                        }}
                      >
                        {t("settings.save")}
                      </button>
                      <button type="button" className="btn btn-xs btn-ghost" onClick={() => setRenameKey(null)}>
                        {t("modal.cancel")}
                      </button>
                    </div>
                  ) : (
                    <strong>{k.name}</strong>
                  )}
                  <span className="mono small muted">{k.prefix}…</span>
                  {k.username && (
                    <span className="badge st-pending">{k.username}</span>
                  )}
                  <span className="small muted">
                    ({t("security.scopes")} {k.scopes.join(", ")})
                  </span>
                  <span className="badge st-complete">
                    {isAllSrvs
                      ? t("security.all_servers")
                      : keySrvs.map((id) => servers.find((s) => s.id === id)?.name || id).join(", ")}
                  </span>
                  <span className="badge st-queued">
                    {isAllLibs ? t("access.all_libraries") : keyLibs.join(", ")}
                  </span>
                  {k.expires_at && k.expires_at > 0 ? (
                    <span className={`badge ${isExpired ? "st-exhausted" : "st-paused"}`}>
                      {isExpired ? t("access.expired") : t("access.expires_on", { date: fmtDate(k.expires_at) })}
                    </span>
                  ) : null}
                </div>
                <div className="action-group" style={{ gap: 6 }}>
                  <button
                    type="button"
                    className="btn btn-xs btn-ghost"
                    aria-label={t("access.rename")}
                    title={t("access.rename")}
                    onClick={() => setRenameKey({ id: k.id, value: k.name })}
                  >
                    <Pencil size={12} aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    className="btn btn-xs btn-danger"
                    aria-label={t("access.revoke")}
                    title={t("access.revoke")}
                    onClick={() => {
                      setConfirmAction({
                        open: true,
                        title: t("confirm.revoke_key_title"),
                        message: t("confirm.revoke_key_msg", { name: k.name }),
                        confirmLabel: t("access.revoke"),
                        onConfirm: async () => {
                          await api(`/api/api-keys/${k.id}`, "DELETE");
                          toast(t("access.apikey_revoked"));
                          await load();
                        },
                      });
                    }}
                  >
                    <Trash2 size={12} aria-hidden="true" />
                  </button>
                </div>
              </div>
            );
          })
        )}
      </div>

      {/* Create User (Admin only) */}
      {user.role === "admin" && (
        <div className="card form-section">
          <div className="field-label-row">
            <h3>{t("security.create_user")}</h3>
            <InfoPopover title={t("security.create_user")} text={t("access.create_user.tip")} />
          </div>
          <div className="field-grid">
            <div className="field">
              <label className="small muted" htmlFor="new-user-name">
                {t("auth.username")}
              </label>
              <input
                id="new-user-name"
                aria-label={t("auth.username")}
                placeholder={t("auth.username")}
                maxLength={64}
                value={newUser.username}
                onChange={(e) => setNewUser({ ...newUser, username: stripControlChars(e.target.value, 64) })}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="new-user-pw">
                {t("auth.password")}
              </label>
              <input
                id="new-user-pw"
                type="password"
                aria-label={t("auth.password")}
                placeholder={t("auth.password")}
                maxLength={256}
                value={newUser.password}
                onChange={(e) => setNewUser({ ...newUser, password: e.target.value.slice(0, 256) })}
              />
            </div>
            <div className="field">
              <span className="small muted">{t("access.role_label")}</span>
              <SelectMenu
                ariaLabel={t("access.role_label")}
                value={newUser.role}
                options={roleOptions}
                onChange={(role) =>
                  setNewUser({
                    ...newUser,
                    role,
                    can_create_api_keys: role !== "viewer",
                  })
                }
              />
            </div>
          </div>

          <div className="ro-info-card" style={{ gap: 10 }}>
            <div className="action-group" style={{ gap: 16, flexWrap: "wrap" }}>
              <span className="small muted strong">{t("access.permissions_label")}:</span>
              <Checkbox
                checked={newUser.role === "admin" || newUser.can_create_api_keys}
                disabled={newUser.role === "admin"}
                onChange={(checked) => setNewUser({ ...newUser, can_create_api_keys: checked })}
                label={t("access.can_create_keys")}
              />
              <Checkbox
                checked={newUser.require_mfa}
                onChange={(checked) => setNewUser({ ...newUser, require_mfa: checked })}
                label={t("access.require_mfa")}
              />
              <Checkbox
                checked={newUser.require_passkey}
                onChange={(checked) => setNewUser({ ...newUser, require_passkey: checked })}
                label={t("access.require_passkey")}
              />
            </div>
            {newUser.role !== "admin" && servers.length > 1 && (
              <div className="action-group" style={{ gap: 10, flexWrap: "wrap", paddingTop: 4, borderTop: "1px solid var(--border)" }}>
                <span className="small muted strong">{t("security.server_access")}:</span>
                <Checkbox
                  checked={newUser.allowed_servers.length === 0}
                  onChange={(checked) => {
                    if (checked) setNewUser({ ...newUser, allowed_servers: [] });
                  }}
                  label={t("security.all_servers")}
                />
                {servers.map((s) => {
                  const checked = newUser.allowed_servers.includes(s.id);
                  return (
                    <Checkbox
                      key={s.id}
                      checked={checked}
                      onChange={(next) => {
                        const list = next
                          ? [...newUser.allowed_servers, s.id]
                          : newUser.allowed_servers.filter((id) => id !== s.id);
                        setNewUser({ ...newUser, allowed_servers: list });
                      }}
                      label={s.server_name || s.name || s.id}
                    />
                  );
                })}
              </div>
            )}
          </div>

          <div style={{ display: "flex", justifyContent: "flex-end" }}>
            <button
              type="button"
              className="btn btn-sm btn-primary"
              onClick={async () => {
                try {
                  await api("/api/users", "POST", newUser);
                  setNewUser({
                    username: "",
                    password: "",
                    display_name: "",
                    role: "viewer",
                    allowed_servers: [],
                    can_create_api_keys: false,
                    require_mfa: false,
                    require_passkey: false,
                  });
                  toast("User created");
                  await load();
                } catch (err) {
                  toast((err as Error).message);
                }
              }}
            >
              <UserPlus size={13} aria-hidden="true" />
              <span>{t("security.create_user")}</span>
            </button>
          </div>
        </div>
      )}

      {/* Existing Users (Admin only) */}
      {user.role === "admin" && (
        <div className="card form-section">
          <div className="field-label-row">
            <h3>
              {t("security.users_heading")} ({fmtNum(users.length)})
            </h3>
            <InfoPopover title={t("security.users_heading")} text={t("access.users.tip")} />
          </div>

          {users.map((u) => {
            const draft = userDrafts[u.id] || userToDraft(u);
            const isDirty = !draftsEqual(draft, userToDraft(u));
            const allowed = draft.allowed_servers;
            const isAll = draft.role === "admin" || allowed.length === 0;
            const mfaCount = u.mfa_count ?? (u.totp_enabled ? 1 : 0);
            const pkCount = u.passkey_count ?? 0;
            const sessCount = u.session_count ?? 0;
            const userKeyCount = apiKeys.filter((k) => k.user_id === u.id || k.username === u.username).length;
            return (
              <div key={u.id} className="priority-row" style={{ flexDirection: "column", alignItems: "stretch", gap: 10 }}>
                <div className="section-head-row" style={{ flexWrap: "wrap", gap: 8 }}>
                  <div className="action-group" style={{ gap: 8, flexWrap: "wrap" }}>
                    <strong>{u.username}</strong>
                    <span className="badge st-paused">{t(`role.${u.role}`)}</span>
                    {u.must_change_password && <span className="badge st-retry">{t("access.pw_change_pending")}</span>}
                    {u.oidc_linked && <span className="badge st-active">OIDC</span>}
                    <span className="badge st-complete">
                      {isAll
                        ? t("security.all_servers")
                        : allowed.map((id) => servers.find((s) => s.id === id)?.name || id).join(", ")}
                    </span>
                    <span className={`badge ${mfaCount > 0 ? "st-complete" : "st-pending"}`}>
                      <ShieldCheck size={11} style={{ marginRight: 3, verticalAlign: "-1px" }} aria-hidden="true" />
                      {mfaCount > 0
                        ? t("access.mfa_active", { count: fmtNum(mfaCount) as unknown as number })
                        : t("access.mfa_inactive")}
                    </span>
                    <span className={`badge ${pkCount > 0 ? "st-complete" : "st-pending"}`}>
                      <KeyRound size={11} style={{ marginRight: 3, verticalAlign: "-1px" }} aria-hidden="true" />
                      {pkCount > 0
                        ? t("access.passkey_active", { count: fmtNum(pkCount) as unknown as number })
                        : t("access.passkey_inactive")}
                    </span>
                    <span className="badge st-queued">
                      {sessCount === 1
                        ? t("access.sessions_one", { count: fmtNum(1) as unknown as number })
                        : t("access.sessions_many", { count: fmtNum(sessCount) as unknown as number })}
                    </span>
                  </div>
                  <div className="action-group" style={{ gap: 6, flexWrap: "wrap" }}>
                    <button
                      type="button"
                      className="btn btn-xs btn-primary"
                      disabled={!isDirty || savingUserId === u.id}
                      onClick={() => void saveUser(u)}
                    >
                      <Save size={12} aria-hidden="true" />
                      <span>{t("settings.save")}</span>
                    </button>
                    <button
                      type="button"
                      className="btn btn-xs btn-primary"
                      onClick={() => setPwModal({ user: u, password: "", mustChange: true })}
                    >
                      <LockKeyhole size={12} aria-hidden="true" />
                      <span>{t("access.set_password")}</span>
                    </button>
                    {mfaCount > 0 && (
                      <button
                        type="button"
                        className="btn btn-xs btn-ghost"
                        onClick={() => {
                          setConfirmAction({
                            open: true,
                            title: t("access.reset_mfa_title"),
                            message: t("access.reset_mfa_msg", { username: u.username }),
                            confirmLabel: t("access.reset_mfa"),
                            onConfirm: async () => {
                              await api(`/api/users/${u.id}/reset-auth`, "POST", { mfa: true, passkeys: false });
                              toast(t("access.mfa_reset_done"));
                              await load();
                            },
                          });
                        }}
                      >
                        <ShieldOff size={12} aria-hidden="true" />
                        <span>{t("access.reset_mfa")}</span>
                      </button>
                    )}
                    {pkCount > 0 && (
                      <button
                        type="button"
                        className="btn btn-xs btn-ghost"
                        onClick={() => {
                          setConfirmAction({
                            open: true,
                            title: t("access.reset_passkey_title"),
                            message: t("access.reset_passkey_msg", { username: u.username }),
                            confirmLabel: t("access.reset_passkey"),
                            onConfirm: async () => {
                              await api(`/api/users/${u.id}/reset-auth`, "POST", { mfa: false, passkeys: true });
                              toast(t("access.passkey_reset_done"));
                              await load();
                            },
                          });
                        }}
                      >
                        <KeyRound size={12} aria-hidden="true" />
                        <span>{t("access.reset_passkey")}</span>
                      </button>
                    )}
                    {u.oidc_linked && (
                      <button
                        type="button"
                        className="btn btn-xs btn-danger"
                        onClick={() => {
                          setConfirmAction({
                            open: true,
                            title: t("access.revoke_oidc_title"),
                            message: t("access.revoke_oidc_msg", { username: u.username }),
                            confirmLabel: t("access.revoke_oidc"),
                            onConfirm: async () => {
                              await api(`/api/users/${u.id}/revoke-oidc`, "POST");
                              toast(t("access.oidc_revoked"));
                              await load();
                            },
                          });
                        }}
                      >
                        <Link2Off size={12} aria-hidden="true" />
                        <span>{t("access.revoke_oidc")}</span>
                      </button>
                    )}
                    {userKeyCount > 0 && (
                      <button
                        type="button"
                        className="btn btn-xs btn-danger"
                        onClick={() => {
                          setConfirmAction({
                            open: true,
                            title: t("access.revoke_user_keys_title"),
                            message: t("access.revoke_user_keys_msg", { username: u.username }),
                            confirmLabel: t("access.revoke_user_keys"),
                            onConfirm: async () => {
                              await api(`/api/users/${u.id}/api-keys`, "DELETE");
                              toast(t("access.user_keys_revoked"));
                              await load();
                            },
                          });
                        }}
                      >
                        <KeyRound size={12} aria-hidden="true" />
                        <span>{t("access.revoke_user_keys")}</span>
                      </button>
                    )}
                    <button
                      type="button"
                      className="btn btn-xs btn-danger"
                      disabled={sessCount === 0}
                      onClick={() => {
                        setConfirmAction({
                          open: true,
                          title: t("access.kill_sessions_title"),
                          message: t("access.kill_sessions_msg", { username: u.username }),
                          confirmLabel: t("access.kill_sessions"),
                          onConfirm: async () => {
                            await api(`/api/users/${u.id}/kill-sessions`, "POST");
                            toast(t("access.sessions_killed"));
                            await load();
                          },
                        });
                      }}
                    >
                      <LogOut size={12} aria-hidden="true" />
                      <span>{t("access.kill_sessions")}</span>
                    </button>
                    {u.id !== user.id && (
                      <>
                        <div style={{ width: 136 }}>
                          <SelectMenu
                            compact
                            ariaLabel={`Role for ${u.username}`}
                            value={draft.role}
                            options={roleOptions}
                            onChange={(role) => patchDraft(u.id, { role: role as UserDraft["role"] })}
                          />
                        </div>
                        <button
                          type="button"
                          className="btn btn-xs btn-danger"
                          aria-label={t("queue.remove")}
                          onClick={() => {
                            setConfirmAction({
                              open: true,
                              title: t("confirm.delete_user_title"),
                              message: t("confirm.delete_user_msg", { username: u.username }),
                              confirmLabel: t("queue.remove"),
                              onConfirm: async () => {
                                await api(`/api/users/${u.id}`, "DELETE");
                                toast("User deleted");
                                await load();
                              },
                            });
                          }}
                        >
                          <Trash2 size={12} aria-hidden="true" />
                        </button>
                      </>
                    )}
                  </div>
                </div>

                {/* Per-User RBAC & Security Policy Controls */}
                <div className="action-group" style={{ gap: 14, flexWrap: "wrap", paddingLeft: 4 }}>
                  <Checkbox
                    checked={draft.role === "admin" || draft.can_create_api_keys}
                    disabled={draft.role === "admin"}
                    onChange={(checked) => patchDraft(u.id, { can_create_api_keys: checked })}
                    label={t("access.can_create_keys")}
                  />
                  <Checkbox
                    checked={draft.require_mfa}
                    onChange={(checked) => patchDraft(u.id, { require_mfa: checked })}
                    label={t("access.require_mfa")}
                  />
                  <Checkbox
                    checked={draft.require_passkey}
                    onChange={(checked) => patchDraft(u.id, { require_passkey: checked })}
                    label={t("access.require_passkey")}
                  />
                  <span className="field-label-row">
                    <Checkbox
                      checked={draft.password_login_disabled}
                      disabled={pkCount === 0}
                      onChange={(checked) => patchDraft(u.id, { password_login_disabled: checked })}
                      label={t("security.disable_pw_login")}
                    />
                    {pkCount === 0 && (
                      <InfoPopover
                        title={t("security.disable_pw_login")}
                        text={t("access.disable_pw_requires_passkey")}
                      />
                    )}
                  </span>
                </div>

                {draft.role !== "admin" && servers.length > 0 && (
                  <div className="action-group" style={{ gap: 10, flexWrap: "wrap", paddingLeft: 4 }}>
                    <span className="small muted">{t("security.server_access")}:</span>
                    <Checkbox
                      checked={allowed.length === 0}
                      onChange={(checked) => {
                        if (checked) patchDraft(u.id, { allowed_servers: [] });
                      }}
                      label={t("security.all_servers")}
                    />
                    {servers.map((s) => {
                      const has = allowed.includes(s.id);
                      return (
                        <Checkbox
                          key={s.id}
                          checked={has}
                          onChange={(next) => {
                            const nextList = next
                              ? [...allowed, s.id].sort()
                              : allowed.filter((id) => id !== s.id);
                            patchDraft(u.id, { allowed_servers: nextList });
                          }}
                          label={s.server_name || s.name || s.id}
                        />
                      );
                    })}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* OIDC / SSO Configuration (Admin only) */}
      {user.role === "admin" && (
        <div className="card form-section">
          <div className="section-head-row">
            <div className="field-label-row">
              <h3>{t("access.oidc_heading")}</h3>
              <InfoPopover title={t("access.oidc_heading")} text={t("access.oidc.tip")} />
            </div>
            <div className="action-group">
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={oidcTesting || !oidcDraft.issuer.trim()}
                onClick={async () => {
                  setOidcTesting(true);
                  try {
                    await api("/api/admin/oidc/test", "POST", { issuer: oidcDraft.issuer.trim() });
                    toast(t("access.oidc_test_ok"));
                  } catch (err) {
                    toast((err as Error).message);
                  } finally {
                    setOidcTesting(false);
                  }
                }}
              >
                <CheckCircle2 size={13} aria-hidden="true" />
                <span>{t("access.oidc_test")}</span>
              </button>
              <button
                type="button"
                className="btn btn-sm btn-primary"
                disabled={oidcSaving || !oidcDirty}
                onClick={async () => {
                  setOidcSaving(true);
                  try {
                    await api("/api/admin/oidc", "PUT", {
                      ...oidcDraft,
                      client_secret: oidcSecret,
                    });
                    toast(t("access.oidc_saved"));
                    await load();
                  } catch (err) {
                    toast((err as Error).message);
                  } finally {
                    setOidcSaving(false);
                  }
                }}
              >
                <Save size={13} aria-hidden="true" />
                <span>{t("settings.save")}</span>
              </button>
            </div>
          </div>

          <div className="action-group" style={{ gap: 16, flexWrap: "wrap" }}>
            <Checkbox
              checked={oidcDraft.enabled}
              onChange={(checked) => setOidcDraft({ ...oidcDraft, enabled: checked })}
              label={t("access.oidc_enable")}
            />
            <Checkbox
              checked={oidcDraft.auto_create}
              onChange={(checked) => setOidcDraft({ ...oidcDraft, auto_create: checked })}
              label={t("access.oidc_auto_create")}
            />
          </div>

          <div className="field-grid">
            <div className="field">
              <label className="small muted" htmlFor="oidc-issuer">{t("access.oidc_issuer")}</label>
              <input
                id="oidc-issuer"
                placeholder="https://auth.example.com/application/o/sweeparr/"
                value={oidcDraft.issuer}
                onChange={(e) => setOidcDraft({ ...oidcDraft, issuer: e.target.value })}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="oidc-redirect">{t("access.oidc_redirect")}</label>
              <input
                id="oidc-redirect"
                placeholder="https://sweeparr.example.com/api/auth/oidc/callback"
                value={oidcDraft.redirect_url}
                onChange={(e) => setOidcDraft({ ...oidcDraft, redirect_url: e.target.value })}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="oidc-client-id">{t("access.oidc_client_id")}</label>
              <input
                id="oidc-client-id"
                value={oidcDraft.client_id}
                onChange={(e) => setOidcDraft({ ...oidcDraft, client_id: e.target.value })}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="oidc-client-secret">{t("access.oidc_client_secret")}</label>
              <input
                id="oidc-client-secret"
                type="password"
                placeholder={oidcDraft.has_client_secret ? t("access.oidc_secret_saved") : ""}
                value={oidcSecret}
                onChange={(e) => setOidcSecret(e.target.value)}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="oidc-groups-claim">{t("access.oidc_groups_claim")}</label>
              <input
                id="oidc-groups-claim"
                placeholder="groups"
                value={oidcDraft.groups_claim}
                onChange={(e) => setOidcDraft({ ...oidcDraft, groups_claim: e.target.value })}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="oidc-admin-groups">{t("access.oidc_admin_groups")}</label>
              <input
                id="oidc-admin-groups"
                placeholder="sweeparr-admins"
                value={oidcDraft.admin_groups}
                onChange={(e) => setOidcDraft({ ...oidcDraft, admin_groups: e.target.value })}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="oidc-op-groups">{t("access.oidc_operator_groups")}</label>
              <input
                id="oidc-op-groups"
                placeholder="sweeparr-operators"
                value={oidcDraft.operator_groups}
                onChange={(e) => setOidcDraft({ ...oidcDraft, operator_groups: e.target.value })}
              />
            </div>
            <div className="field">
              <label className="small muted" htmlFor="oidc-viewer-groups">{t("access.oidc_viewer_groups")}</label>
              <input
                id="oidc-viewer-groups"
                placeholder="sweeparr-viewers"
                value={oidcDraft.viewer_groups}
                onChange={(e) => setOidcDraft({ ...oidcDraft, viewer_groups: e.target.value })}
              />
            </div>
          </div>
        </div>
      )}

      {/* Admin Set Password Modal */}
      {pwModal && (
        <div className="modal-backdrop" role="presentation" onClick={() => setPwModal(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
            <h3 style={{ margin: 0 }}>{t("access.set_password_title", { username: pwModal.user.username })}</h3>
            <p className="small muted" style={{ margin: 0 }}>
              {t("auth.password_hint", { min: fmtNum(10) })}
            </p>
            <input
              type="password"
              aria-label={t("security.new_pw")}
              placeholder={t("security.new_pw")}
              maxLength={256}
              value={pwModal.password}
              onChange={(e) => setPwModal({ ...pwModal, password: e.target.value.slice(0, 256) })}
              autoFocus
            />
            <Checkbox
              checked={pwModal.mustChange}
              onChange={(checked) => setPwModal({ ...pwModal, mustChange: checked })}
              label={t("access.force_pw_change")}
            />
            <div className="action-group" style={{ justifyContent: "flex-end" }}>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setPwModal(null)}>
                {t("modal.cancel")}
              </button>
              <button
                type="button"
                className="btn btn-sm btn-primary"
                onClick={async () => {
                  try {
                    await api(`/api/users/${pwModal.user.id}/password`, "POST", {
                      password: pwModal.password,
                      must_change: pwModal.mustChange,
                    });
                    setPwModal(null);
                    toast(t("access.password_updated"));
                    await load();
                  } catch (err) {
                    toast((err as Error).message);
                  }
                }}
              >
                {t("confirm.confirm_btn")}
              </button>
            </div>
          </div>
        </div>
      )}

      <ConfirmModal
        open={Boolean(confirmAction?.open)}
        title={confirmAction?.title || ""}
        message={confirmAction?.message || ""}
        confirmLabel={confirmAction?.confirmLabel || t("confirm.confirm_btn")}
        danger
        busy={confirmBusy}
        onConfirm={async () => {
          if (!confirmAction) return;
          setConfirmBusy(true);
          try {
            await confirmAction.onConfirm();
            setConfirmAction(null);
          } catch (err) {
            toast((err as Error).message);
          } finally {
            setConfirmBusy(false);
          }
        }}
        onCancel={() => setConfirmAction(null)}
      />
    </div>
  );
}
