import React, { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { startAuthentication, startRegistration } from "@simplewebauthn/browser";
import { QRCodeSVG } from "qrcode.react";
import { Link2, Pencil, ShieldAlert, Trash2 } from "lucide-react";
import { api } from "../api";
import { useLocaleFormat } from "../utils/format";
import { stripControlChars } from "../utils/sanitize";
import { Checkbox, InfoPopover } from "../components/Controls";
import type { PasskeyRecord, User, UserMFARecord } from "../types";

export function AccountView({
  user,
  oidcEnabled = false,
  onDirtyChange,
  onUserRefresh,
  toast,
}: {
  user: User;
  oidcEnabled?: boolean;
  onDirtyChange?: (dirty: boolean) => void;
  onUserRefresh: () => Promise<void>;
  toast: (msg: string) => void;
}) {
  const { t } = useTranslation();
  const { fmtDate, fmtNum } = useLocaleFormat();
  const [curPw, setCurPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [pwMfaCode, setPwMfaCode] = useState("");

  const [mfaKeys, setMfaKeys] = useState<UserMFARecord[]>([]);
  const [mfaSetup, setMfaSetup] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [mfaName, setMfaName] = useState("Authenticator");
  const [mfaCode, setMfaCode] = useState("");

  const [passkeys, setPasskeys] = useState<PasskeyRecord[]>([]);
  const [pkLabel, setPkLabel] = useState("My Passkey");

  const [renameTarget, setRenameTarget] = useState<{ kind: "mfa" | "passkey"; id: string; value: string } | null>(null);
  const [confirmAction, setConfirmAction] = useState<{
    open: boolean;
    title: string;
    message: string;
    needPassword?: boolean;
    onConfirm: (pw: string) => Promise<void>;
  } | null>(null);
  const [confirmPw, setConfirmPw] = useState("");

  async function load() {
    const [m, p] = await Promise.all([
      api<{ mfa_keys: UserMFARecord[] }>("/api/auth/mfa"),
      api<{ passkeys: PasskeyRecord[] }>("/api/auth/passkeys"),
    ]);
    setMfaKeys(m.mfa_keys || []);
    setPasskeys(p.passkeys || []);
  }

  useEffect(() => {
    void load();
  }, [user.id]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get("oidc_linked") === "1") {
      toast(t("account.oidc_linked_ok"));
      void onUserRefresh();
      window.history.replaceState({}, "", window.location.pathname);
    } else if (params.get("oidc_error")) {
      toast(params.get("oidc_error") || "OIDC link failed");
      window.history.replaceState({}, "", window.location.pathname);
    }
  }, []);

  useEffect(() => {
    onDirtyChange?.(Boolean(curPw || newPw || pwMfaCode || renameTarget || mfaSetup));
    return () => onDirtyChange?.(false);
  }, [curPw, newPw, pwMfaCode, renameTarget, mfaSetup, onDirtyChange]);

  async function getPasskeyStepUp() {
    const begin = await api<{
      options: { publicKey: Parameters<typeof startAuthentication>[0]["optionsJSON"] };
      challenge_id: string;
    }>("/api/auth/passkey/stepup/begin", "POST");
    const assertion = await startAuthentication({ optionsJSON: begin.options.publicKey });
    return { passkey_challenge_id: begin.challenge_id, passkey_assertion: assertion };
  }

  async function handlePasswordChange(e: React.FormEvent) {
    e.preventDefault();
    try {
      if ((mfaKeys.length > 0 || passkeys.length > 0) && !pwMfaCode.trim() && passkeys.length > 0) {
        const step = await getPasskeyStepUp();
        await api("/api/auth/password", "POST", {
          current_password: curPw,
          new_password: newPw,
          mfa_code: pwMfaCode.trim(),
          ...step,
        });
      } else {
        await api("/api/auth/password", "POST", {
          current_password: curPw,
          new_password: newPw,
          mfa_code: pwMfaCode.trim(),
        });
      }
      setCurPw("");
      setNewPw("");
      setPwMfaCode("");
      toast("Password updated — please sign in again");
      await onUserRefresh();
    } catch (err) {
      toast((err as Error).message);
    }
  }

  async function togglePasswordLogin(disable: boolean) {
    if (disable) {
      setConfirmPw("");
      setConfirmAction({
        open: true,
        title: t("security.disable_pw_title"),
        message: t("security.disable_pw_msg"),
        needPassword: true,
        onConfirm: async (pw) => {
          await api("/api/auth/password-login", "POST", { disabled: true, password: pw });
          toast("Password login disabled");
          await onUserRefresh();
        },
      });
    } else {
      await api("/api/auth/password-login", "POST", { disabled: false, password: "" });
      toast("Password login enabled");
      await onUserRefresh();
    }
  }

  async function saveRename() {
    if (!renameTarget) return;
    if (renameTarget.kind === "mfa") {
      await api(`/api/auth/mfa/${renameTarget.id}`, "PATCH", { name: renameTarget.value });
    } else {
      await api(`/api/auth/passkeys/${renameTarget.id}`, "PATCH", { label: renameTarget.value });
    }
    setRenameTarget(null);
    toast("Renamed");
    await load();
  }

  const mfaRequiredMissing = Boolean(user.require_mfa) && mfaKeys.length === 0;
  const passkeyRequiredMissing = Boolean(user.require_passkey) && passkeys.length === 0;

  return (
    <div className="page-view unified-page">
      {(mfaRequiredMissing || passkeyRequiredMissing) && (
        <div
          className="card"
          role="alert"
          style={{
            borderColor: "var(--accent)",
            background: "rgba(245, 166, 35, 0.08)",
            display: "flex",
            alignItems: "center",
            gap: 10,
            padding: "12px 16px",
          }}
        >
          <ShieldAlert size={18} aria-hidden="true" />
          <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <strong className="small">{t("account.policy_notice_title")}</strong>
            <span className="small muted">
              {mfaRequiredMissing && passkeyRequiredMissing
                ? t("account.policy_require_both")
                : mfaRequiredMissing
                  ? t("account.policy_require_mfa")
                  : t("account.policy_require_passkey")}
            </span>
          </div>
        </div>
      )}

      {/* Password & Sign-in Policy */}
      <div className="card form-section">
        <div className="field-label-row">
          <h3>{t("security.account_pw")}</h3>
          <InfoPopover title={t("security.account_pw")} text={t("account.password.tip")} />
        </div>
        <form onSubmit={handlePasswordChange} style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 460 }}>
          {!user.password_login_disabled && (
            <input
              type="password"
              aria-label={t("security.current_pw")}
              placeholder={t("security.current_pw")}
              value={curPw}
              onChange={(e) => setCurPw(e.target.value)}
              required
            />
          )}
          <input
            type="password"
            aria-label={t("security.new_pw")}
            placeholder={t("security.new_pw")}
            value={newPw}
            onChange={(e) => setNewPw(e.target.value)}
            minLength={10}
            required
          />
          <span className="small muted">{t("auth.password_hint", { min: fmtNum(10) })}</span>
          {(mfaKeys.length > 0 || user.totp_enabled) && (
            <input
              aria-label={t("auth.mfa_code")}
              placeholder={passkeys.length > 0 ? t("security.pw_mfa_or_passkey") : t("auth.mfa_code")}
              value={pwMfaCode}
              onChange={(e) => setPwMfaCode(e.target.value)}
              maxLength={6}
            />
          )}
          <button type="submit" className="btn btn-primary btn-sm" style={{ alignSelf: "flex-start" }}>
            {t("security.update_pw")}
          </button>
        </form>
        {passkeys.length > 0 && (
          <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 8 }}>
            <Checkbox
              checked={Boolean(user.password_login_disabled)}
              onChange={(checked) => void togglePasswordLogin(checked)}
              label={t("security.disable_pw_login")}
            />
            {(mfaKeys.length > 0 || user.totp_enabled) && (
              <Checkbox
                checked={Boolean(user.passkey_require_mfa)}
                onChange={async (checked) => {
                  try {
                    await api("/api/auth/mfa-policy", "POST", { passkey_require_mfa: checked });
                    toast(checked ? "MFA required after Passkey sign-in" : "Passkey-only sign-in enabled");
                    await onUserRefresh();
                  } catch (err) {
                    toast((err as Error).message);
                  }
                }}
                label={t("security.passkey_require_mfa")}
              />
            )}
          </div>
        )}
      </div>

      {/* Multi-MFA */}
      <div className="card form-section">
        <div className="section-head-row">
          <div className="field-label-row">
            <h3>{t("security.mfa_heading")}</h3>
            <InfoPopover title={t("security.mfa_heading")} text={t("account.mfa.tip")} />
          </div>
          {user.require_mfa && <span className="badge st-running">{t("access.mfa_enforced")}</span>}
        </div>
        {user.password_login_disabled && !user.passkey_require_mfa && (
          <div className="small muted">{t("security.mfa_passwordless_note")}</div>
        )}
        {mfaKeys.map((m) => (
          <div key={m.id} className="priority-row">
            <div>
              {renameTarget?.kind === "mfa" && renameTarget.id === m.id ? (
                <div className="action-group">
                  <input
                    maxLength={64}
                    value={renameTarget.value}
                    onChange={(e) => setRenameTarget({ ...renameTarget, value: stripControlChars(e.target.value, 64) })}
                  />
                  <button type="button" className="btn btn-xs btn-primary" onClick={() => void saveRename()}>
                    {t("settings.save")}
                  </button>
                  <button type="button" className="btn btn-xs btn-ghost" onClick={() => setRenameTarget(null)}>
                    {t("modal.cancel")}
                  </button>
                </div>
              ) : (
                <>
                  <strong>{m.name}</strong>{" "}
                  <span className="small muted">{t("security.added_date", { date: fmtDate(m.created_at) })}</span>
                </>
              )}
            </div>
            <div className="action-group">
              <button
                type="button"
                className="btn btn-xs btn-ghost"
                aria-label="Rename"
                onClick={() => setRenameTarget({ kind: "mfa", id: m.id, value: m.name })}
              >
                <Pencil size={12} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="btn btn-xs btn-danger"
                aria-label="Delete"
                disabled={Boolean(user.require_mfa) && mfaKeys.length <= 1}
                onClick={() => {
                  setConfirmPw("");
                  setConfirmAction({
                    open: true,
                    title: t("confirm.delete_mfa_title"),
                    message: t("confirm.delete_mfa_msg", { name: m.name }),
                    needPassword: !user.password_login_disabled,
                    onConfirm: async (pw) => {
                      const extra = user.password_login_disabled && passkeys.length > 0 ? await getPasskeyStepUp() : {};
                      await api(`/api/auth/mfa/${m.id}`, "DELETE", { password: pw, ...extra });
                      toast("Authenticator removed");
                      await load();
                      await onUserRefresh();
                    },
                  });
                }}
              >
                <Trash2 size={12} aria-hidden="true" />
              </button>
            </div>
          </div>
        ))}
        {!mfaSetup && (
          <div className="action-group" style={{ marginTop: mfaKeys.length > 0 ? 4 : 0 }}>
            <button
              type="button"
              className="btn btn-sm btn-primary"
              onClick={async () => {
                const r = await api<{ secret: string; otpauth_url: string }>("/api/auth/mfa/enroll", "POST");
                setMfaSetup(r);
              }}
            >
              {t("security.mfa_setup_btn")}
            </button>
          </div>
        )}
        {mfaSetup && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 16, alignItems: "center", marginTop: 8 }}>
            <div style={{ background: "#fff", padding: 10, borderRadius: 8 }}>
              <QRCodeSVG value={mfaSetup.otpauth_url} size={140} />
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <div className="small muted">
                {t("security.mfa_secret")} <code className="mono">{mfaSetup.secret}</code>
              </div>
              <input
                maxLength={64}
                placeholder={t("security.mfa_name_placeholder")}
                value={mfaName}
                onChange={(e) => setMfaName(stripControlChars(e.target.value, 64))}
              />
              <div className="action-group">
                <input
                  aria-label={t("security.mfa_enter_code")}
                  placeholder={t("security.mfa_enter_code")}
                  value={mfaCode}
                  onChange={(e) => setMfaCode(e.target.value)}
                  maxLength={6}
                />
                <button
                  type="button"
                  className="btn btn-primary btn-sm"
                  onClick={async () => {
                    try {
                      await api("/api/auth/mfa/confirm", "POST", { name: mfaName, code: mfaCode });
                      setMfaSetup(null);
                      setMfaCode("");
                      toast("Authenticator added");
                      await load();
                      await onUserRefresh();
                    } catch (err) {
                      toast((err as Error).message);
                    }
                  }}
                >
                  {t("security.mfa_confirm_btn")}
                </button>
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => setMfaSetup(null)}>
                  {t("modal.cancel")}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Passkeys */}
      <div className="card form-section">
        <div className="section-head-row">
          <div className="field-label-row">
            <h3>{t("security.passkeys_heading")}</h3>
            <InfoPopover title={t("security.passkeys_heading")} text={t("account.passkeys.tip")} />
          </div>
          {user.require_passkey && <span className="badge st-running">{t("access.passkey_enforced")}</span>}
        </div>
        <div className="action-group">
          <input
            aria-label={t("security.passkey_placeholder")}
            placeholder={t("security.passkey_placeholder")}
            value={pkLabel}
            onChange={(e) => setPkLabel(e.target.value)}
          />
          <button
            type="button"
            className="btn btn-sm btn-primary"
            onClick={async () => {
              try {
                const begin = await api<{
                  options: { publicKey: Parameters<typeof startRegistration>[0]["optionsJSON"] };
                  challenge_id: string;
                }>("/api/auth/passkey/register/begin", "POST", { label: pkLabel });
                const att = await startRegistration({ optionsJSON: begin.options.publicKey });
                await api(`/api/auth/passkey/register/finish?challenge_id=${encodeURIComponent(begin.challenge_id)}`, "POST", att);
                toast("Passkey registered");
                await load();
                await onUserRefresh();
              } catch (err) {
                toast((err as Error).message);
              }
            }}
          >
            {t("security.passkey_register")}
          </button>
        </div>
        {passkeys.map((p) => (
          <div key={p.id} className="priority-row">
            <div>
              {renameTarget?.kind === "passkey" && renameTarget.id === p.id ? (
                <div className="action-group">
                  <input
                    maxLength={64}
                    value={renameTarget.value}
                    onChange={(e) => setRenameTarget({ ...renameTarget, value: stripControlChars(e.target.value, 64) })}
                  />
                  <button type="button" className="btn btn-xs btn-primary" onClick={() => void saveRename()}>
                    {t("settings.save")}
                  </button>
                  <button type="button" className="btn btn-xs btn-ghost" onClick={() => setRenameTarget(null)}>
                    {t("modal.cancel")}
                  </button>
                </div>
              ) : (
                <>
                  <strong>{p.label}</strong>{" "}
                  <span className="small muted">{t("security.added_date", { date: fmtDate(p.created_at) })}</span>
                </>
              )}
            </div>
            <div className="action-group">
              <button
                type="button"
                className="btn btn-xs btn-ghost"
                aria-label="Rename"
                onClick={() => setRenameTarget({ kind: "passkey", id: p.id, value: p.label })}
              >
                <Pencil size={12} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="btn btn-xs btn-danger"
                aria-label="Delete"
                disabled={(Boolean(user.password_login_disabled) || Boolean(user.require_passkey)) && passkeys.length <= 1}
                onClick={() => {
                  setConfirmPw("");
                  setConfirmAction({
                    open: true,
                    title: t("confirm.delete_passkey_title"),
                    message: t("confirm.delete_passkey_msg", { name: p.label }),
                    needPassword: !user.password_login_disabled,
                    onConfirm: async (pw) => {
                      const extra = user.password_login_disabled && passkeys.length > 0 ? await getPasskeyStepUp() : {};
                      await api(`/api/auth/passkeys/${p.id}`, "DELETE", { password: pw, ...extra });
                      toast("Passkey removed");
                      await load();
                      await onUserRefresh();
                    },
                  });
                }}
              >
                <Trash2 size={12} aria-hidden="true" />
              </button>
            </div>
          </div>
        ))}
      </div>

      {/* OIDC / SSO Association */}
      {(oidcEnabled || user.oidc_linked) && (
        <div className="card form-section">
          <div className="section-head-row">
            <div className="field-label-row">
              <h3>{t("account.oidc_heading")}</h3>
              <InfoPopover title={t("account.oidc_heading")} text={t("account.oidc.tip")} />
            </div>
            {user.oidc_linked ? (
              <span className="badge st-complete">{t("account.oidc_linked")}</span>
            ) : (
              <a className="btn btn-sm btn-primary" href="/api/auth/oidc/link" style={{ textDecoration: "none" }}>
                <Link2 size={13} aria-hidden="true" />
                <span>{t("account.oidc_link_btn")}</span>
              </a>
            )}
          </div>
          <p className="small muted" style={{ margin: 0 }}>
            {user.oidc_linked ? t("account.oidc_linked_desc") : t("account.oidc_unlinked_desc")}
          </p>
        </div>
      )}

      {/* Confirmation Modal */}
      {confirmAction?.open && (
        <div className="modal-backdrop" role="presentation" onClick={() => setConfirmAction(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
            <h3 style={{ margin: 0 }}>{confirmAction.title}</h3>
            <p className="small muted" style={{ margin: 0 }}>
              {confirmAction.message}
            </p>
            {confirmAction.needPassword && (
              <input
                type="password"
                placeholder={t("security.current_pw")}
                value={confirmPw}
                onChange={(e) => setConfirmPw(e.target.value)}
                autoFocus
              />
            )}
            <div className="action-group" style={{ justifyContent: "flex-end" }}>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setConfirmAction(null)}>
                {t("modal.cancel")}
              </button>
              <button
                type="button"
                className="btn btn-sm btn-danger"
                onClick={async () => {
                  try {
                    await confirmAction.onConfirm(confirmPw);
                    setConfirmAction(null);
                    setConfirmPw("");
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
    </div>
  );
}
