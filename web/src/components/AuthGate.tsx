import React, { useState } from "react";
import { useTranslation } from "react-i18next";
import { startAuthentication, startRegistration } from "@simplewebauthn/browser";
import { KeyRound, ShieldCheck, Fingerprint, LogIn } from "lucide-react";
import { api } from "../api";
import { sanitizeDigits, stripControlChars } from "../utils/sanitize";
import { Checkbox } from "./Controls";
import type { AuthStatus } from "../types";

interface Props {
  status: AuthStatus;
  onSuccess: () => Promise<void>;
}

export function AuthGate({ status, onSuccess }: Props) {
  const { t } = useTranslation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [setupStep, setSetupStep] = useState<1 | 2>(1);
  const [setupPasskeyAdded, setSetupPasskeyAdded] = useState(false);
  const [setupDisablePw, setSetupDisablePw] = useState(false);

  async function handleSetup(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const navLang = (typeof navigator !== "undefined" && navigator.language) || "de-DE";
      const resolved = Intl.DateTimeFormat(navLang, { hour: "numeric" }).resolvedOptions();
      const timeFmt = resolved.hour12 ? "12h" : "24h";
      await api("/api/auth/setup", "POST", {
        username,
        password,
        display_name: displayName,
        number_locale: navLang,
        datetime_locale: navLang,
        time_format: timeFmt,
      });
      setSetupStep(2);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function handleSetupPasskey() {
    setError("");
    setBusy(true);
    try {
      const begin = await api<{
        options: { publicKey: Parameters<typeof startRegistration>[0]["optionsJSON"] };
        challenge_id: string;
      }>("/api/auth/passkey/register/begin", "POST", { label: "Primary Passkey" });
      const att = await startRegistration({ optionsJSON: begin.options.publicKey });
      await api(`/api/auth/passkey/register/finish?challenge_id=${encodeURIComponent(begin.challenge_id)}`, "POST", att);
      setSetupPasskeyAdded(true);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function finishSetupStep2() {
    setError("");
    setBusy(true);
    try {
      if (setupPasskeyAdded && setupDisablePw) {
        await api("/api/auth/password-login", "POST", { disabled: true, password });
      }
      await onSuccess();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await api("/api/auth/login", "POST", { username, password });
      await onSuccess();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function handleMFA(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await api("/api/auth/mfa/verify", "POST", { code: mfaCode });
      await onSuccess();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function handlePasskey() {
    setError("");
    setBusy(true);
    try {
      const begin = await api<{
        options: { publicKey: Parameters<typeof startAuthentication>[0]["optionsJSON"] };
        challenge_id: string;
      }>("/api/auth/passkey/login/begin", "POST", { username: username.trim() });
      const assertion = await startAuthentication({ optionsJSON: begin.options.publicKey });
      await api(`/api/auth/passkey/login/finish?challenge_id=${encodeURIComponent(begin.challenge_id)}`, "POST", assertion);
      await onSuccess();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  if (status.needs_setup || setupStep === 2) {
    if (setupStep === 2) {
      return (
        <main className="auth-shell">
          <div className="auth-card" aria-label={t("auth.setup_passkey_title")}>
            <div className="auth-head">
              <Fingerprint size={28} color="#e5a00d" aria-hidden="true" />
              <div>
                <h1>{t("auth.setup_passkey_title")}</h1>
                <div className="small muted">{t("auth.setup_passkey_desc")}</div>
              </div>
            </div>
            {error && (
              <div className="err-banner" role="alert">
                {error}
              </div>
            )}
            <button type="button" className="btn" disabled={busy} onClick={() => void handleSetupPasskey()}>
              <Fingerprint size={16} aria-hidden="true" />{" "}
              {setupPasskeyAdded ? "Passkey Registered ✓" : t("security.passkey_register")}
            </button>
            {setupPasskeyAdded && (
              <Checkbox
                checked={setupDisablePw}
                onChange={setSetupDisablePw}
                label={t("security.disable_pw_login")}
              />
            )}
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void finishSetupStep2()}>
              {setupPasskeyAdded ? t("confirm.confirm_btn") : t("auth.setup_skip_btn")}
            </button>
          </div>
        </main>
      );
    }
    return (
      <main className="auth-shell">
        <form className="auth-card" onSubmit={handleSetup} aria-label={t("auth.setup_title")}>
          <div className="auth-head">
            <ShieldCheck size={28} color="#e5a00d" aria-hidden="true" />
            <div>
              <h1>{t("auth.setup_title")}</h1>
              <div className="small muted">{t("auth.setup_subtitle")}</div>
            </div>
          </div>
          {error && (
            <div className="err-banner" role="alert">
              {error}
            </div>
          )}
          <label className="field">
            <span>{t("auth.admin_username")}</span>
            <input value={username} onChange={(e) => setUsername(stripControlChars(e.target.value, 64))} required minLength={2} maxLength={64} autoFocus />
          </label>
          <label className="field">
            <span>{t("auth.display_name")}</span>
            <input value={displayName} onChange={(e) => setDisplayName(stripControlChars(e.target.value, 128))} maxLength={128} placeholder={t("auth.optional")} />
          </label>
          <label className="field">
            <span>{t("auth.password_min8")}</span>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value.slice(0, 256))} required minLength={10} maxLength={256} />
            <span className="small muted">{t("auth.password_hint", { min: 10 })}</span>
          </label>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {t("auth.create_admin")}
          </button>
        </form>
      </main>
    );
  }

  if (status.mfa_pending) {
    return (
      <main className="auth-shell">
        <form className="auth-card" onSubmit={handleMFA} aria-label={t("auth.mfa_title")}>
          <div className="auth-head">
            <KeyRound size={28} color="#e5a00d" aria-hidden="true" />
            <div>
              <h1>{t("auth.mfa_title")}</h1>
              <div className="small muted">{t("auth.mfa_subtitle")}</div>
            </div>
          </div>
          {error && (
            <div className="err-banner" role="alert">
              {error}
            </div>
          )}
          <label className="field">
            <span>{t("auth.mfa_code")}</span>
            <input
              value={mfaCode}
              onChange={(e) => setMfaCode(sanitizeDigits(e.target.value, 6))}
              placeholder="123456"
              inputMode="numeric"
              maxLength={6}
              required
              autoFocus
            />
          </label>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {t("auth.verify_code")}
          </button>
        </form>
      </main>
    );
  }

  return (
    <main className="auth-shell">
      <form className="auth-card" onSubmit={handleLogin} aria-label={t("auth.signin_title")}>
        <div className="auth-head">
          <LogIn size={28} color="#e5a00d" aria-hidden="true" />
          <div>
            <h1>{t("auth.signin_title")}</h1>
            <div className="small muted">{t("auth.signin_subtitle")}</div>
          </div>
        </div>
        {error && (
          <div className="err-banner" role="alert">
            {error}
          </div>
        )}
        <label className="field">
          <span>{t("auth.username")}</span>
          <input value={username} onChange={(e) => setUsername(stripControlChars(e.target.value, 64))} maxLength={64} required autoFocus />
        </label>
        <label className="field">
          <span>{t("auth.password")}</span>
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value.slice(0, 256))} maxLength={256} required />
        </label>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {t("auth.signin")}
        </button>

        <div className="auth-divider">{t("auth.or")}</div>
        <button type="button" className="btn" onClick={handlePasskey} disabled={busy}>
          <Fingerprint size={16} aria-hidden="true" /> {t("auth.signin_passkey")}
        </button>
        {status.oidc_enabled && (
          <a className="btn btn-blue" href="/api/auth/oidc/login" style={{ textDecoration: "none" }}>
            {t("auth.signin_oidc")}
          </a>
        )}
      </form>
    </main>
  );
}
