package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fabsau/sweeparr/internal/auth"
	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/engine"
)

func registerAuthRoutes(r chi.Router, database *db.DB, authSvc *auth.Service, mgr *engine.Manager, authLimiter *ipRateLimiter) {
	// --- Public Auth Endpoints ---
	r.Get("/api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		needsSetup, _ := authSvc.NeedsSetup()
		u, _, _, _, mfaPending, _ := authSvc.Authenticate(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"needs_setup":   needsSetup,
			"authenticated": u != nil && !mfaPending,
			"mfa_pending":   mfaPending,
			"oidc_enabled":  authSvc.OIDCConfigured(),
			"user":          u,
		})
	})

	r.With(authLimiter.Middleware).Post("/api/auth/setup", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username       string `json:"username"`
			Password       string `json:"password"`
			DisplayName    string `json:"display_name"`
			NumberLocale   string `json:"number_locale"`
			DateTimeLocale string `json:"datetime_locale"`
			TimeFormat     string `json:"time_format"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		req.Username = config.SanitizeText(req.Username, 64)
		req.DisplayName = config.SanitizeText(req.DisplayName, 128)
		if err := config.ValidateUsername(req.Username); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(req.Password) > 256 {
			writeErr(w, http.StatusBadRequest, "Password exceeds maximum length")
			return
		}
		u, tok, err := authSvc.SetupAdmin(req.Username, req.Password, req.DisplayName, clientIP(r))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.NumberLocale != "" || req.DateTimeLocale != "" || req.TimeFormat != "" {
			for _, s := range mgr.ListServers() {
				if wkr := mgr.GetWorker(s.ID); wkr != nil {
					cfg := config.DefaultScannerConfig()
					if req.NumberLocale != "" {
						cfg.NumberLocale = req.NumberLocale
					}
					if req.DateTimeLocale != "" {
						cfg.DateTimeLocale = req.DateTimeLocale
					}
					if req.TimeFormat != "" {
						cfg.TimeFormat = req.TimeFormat
					}
					wkr.UpdateConfig(cfg, false)
				}
			}
		}
		authSvc.SetSessionCookie(w, r, tok)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
	})

	r.With(authLimiter.Middleware).Post("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		req.Username = config.SanitizeText(req.Username, 64)
		if len(req.Password) > 256 {
			writeErr(w, http.StatusUnauthorized, "Authentication failed")
			return
		}
		u, tok, mfaPending, err := authSvc.LoginPassword(req.Username, req.Password, clientIP(r))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Authentication failed")
			return
		}
		authSvc.SetSessionCookie(w, r, tok)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mfa_pending": mfaPending, "user": u})
	})

	r.With(authLimiter.Middleware).Post("/api/auth/mfa/verify", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code string `json:"code"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		u, err := authSvc.VerifyLoginTOTP(r, req.Code)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Authentication failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
	})

	r.With(authLimiter.Middleware).Post("/api/auth/passkey/login/begin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		options, cid, err := authSvc.BeginPasskeyLogin(req.Username)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Authentication failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"options": options, "challenge_id": cid})
	})

	r.With(authLimiter.Middleware).Post("/api/auth/passkey/login/finish", func(w http.ResponseWriter, r *http.Request) {
		cid := r.URL.Query().Get("challenge_id")
		u, tok, err := authSvc.FinishPasskeyLogin(cid, r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Authentication failed")
			return
		}
		authSvc.SetSessionCookie(w, r, tok)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
	})

	r.With(authLimiter.Middleware).Get("/api/auth/oidc/login", func(w http.ResponseWriter, r *http.Request) {
		u, err := authSvc.BeginOIDCLogin()
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, u, http.StatusFound)
	})

	r.With(authLimiter.Middleware).Get("/api/auth/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		_, tok, linked, err := authSvc.HandleOIDCCallback(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"), clientIP(r))
		if err != nil {
			slog.Warn("oidc callback authentication failed", "error", err, "remote_ip", clientIP(r))
			if linked {
				http.Redirect(w, r, "/account?oidc_error="+err.Error(), http.StatusFound)
				return
			}
			http.Redirect(w, r, "/?auth_error=SSO+authentication+failed", http.StatusFound)
			return
		}
		if linked {
			http.Redirect(w, r, "/account?oidc_linked=1", http.StatusFound)
			return
		}
		authSvc.SetSessionCookie(w, r, tok)
		http.Redirect(w, r, "/", http.StatusFound)
	})

	r.Post("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		authSvc.ClearSessionCookie(w, r)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// --- Authenticated Account & Security Routes ---
	r.Group(func(r chi.Router) {
		r.Use(authSvc.RequireSessionOrAPIKey(""))

		r.Get("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"user": auth.UserFromContext(r.Context())})
		})

		r.Get("/api/auth/oidc/link", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			redir, err := authSvc.BeginOIDCLink(u.ID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			http.Redirect(w, r, redir, http.StatusFound)
		})

		r.With(authLimiter.Middleware).Post("/api/auth/password", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				CurrentPassword    string          `json:"current_password"`
				NewPassword        string          `json:"new_password"`
				MFACode            string          `json:"mfa_code"`
				PasskeyChallengeID string          `json:"passkey_challenge_id"`
				PasskeyAssertion   json.RawMessage `json:"passkey_assertion"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if len(req.CurrentPassword) > 256 || len(req.NewPassword) > 256 {
				writeErr(w, http.StatusBadRequest, "Password exceeds maximum length")
				return
			}
			if err := authSvc.ChangePassword(u.ID, req.CurrentPassword, req.NewPassword, config.SanitizeText(req.MFACode, 16), config.SanitizeText(req.PasskeyChallengeID, 128), req.PasskeyAssertion, r); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/auth/password-login", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				Disabled bool   `json:"disabled"`
				Password string `json:"password"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if req.Disabled {
				pks, _ := database.ListPasskeysByUser(u.ID)
				if len(pks) == 0 {
					writeErr(w, http.StatusBadRequest, "Register at least one Passkey before disabling password login")
					return
				}
				if err := authSvc.VerifyUserPassword(u, req.Password); err != nil {
					writeErr(w, http.StatusForbidden, "Current password is required to disable password login")
					return
				}
			}
			if err := database.SetPasswordLoginDisabled(u.ID, req.Disabled); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "password_login_disabled": req.Disabled})
		})

		r.Post("/api/auth/mfa-policy", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				PasskeyRequireMFA bool `json:"passkey_require_mfa"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if req.PasskeyRequireMFA {
				pks, _ := database.ListPasskeysByUser(u.ID)
				mfas, _ := database.ListUserMFA(u.ID)
				if len(pks) == 0 || (!u.TOTPEnabled && len(mfas) == 0) {
					writeErr(w, http.StatusBadRequest, "Register both a Passkey and an MFA key before requiring Passkey + MFA")
					return
				}
			}
			if err := database.SetPasskeyRequireMFA(u.ID, req.PasskeyRequireMFA); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "passkey_require_mfa": req.PasskeyRequireMFA})
		})

		r.Get("/api/auth/mfa", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			list, err := database.ListUserMFA(u.ID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"mfa_keys": list})
		})

		r.Post("/api/auth/mfa/enroll", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			secret, uri, err := authSvc.BeginTOTPEnroll(u)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "otpauth_url": uri})
		})

		r.With(authLimiter.Middleware).Post("/api/auth/mfa/confirm", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				Name string `json:"name"`
				Code string `json:"code"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec, err := authSvc.ConfirmTOTPEnroll(u, config.SanitizeText(req.Name, 64), config.SanitizeText(req.Code, 16))
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mfa": rec})
		})

		r.Patch("/api/auth/mfa/{id}", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				Name string `json:"name"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if err := database.RenameUserMFA(u.ID, chi.URLParam(r, "id"), config.SanitizeText(req.Name, 64)); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Delete("/api/auth/mfa/{id}", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			mfas, _ := database.ListUserMFA(u.ID)
			if u.RequireMFA && len(mfas) <= 1 {
				writeErr(w, http.StatusBadRequest, "MFA is required by your administrator and cannot be removed")
				return
			}
			var req struct {
				Password           string          `json:"password"`
				PasskeyChallengeID string          `json:"passkey_challenge_id"`
				PasskeyAssertion   json.RawMessage `json:"passkey_assertion"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if u.PasswordLoginDisabled {
				if err := authSvc.FinishPasskeyStepUp(u, req.PasskeyChallengeID, req.PasskeyAssertion, r); err != nil {
					writeErr(w, http.StatusForbidden, "Passkey verification required")
					return
				}
			} else if err := authSvc.VerifyUserPassword(u, req.Password); err != nil {
				writeErr(w, http.StatusForbidden, "Password confirmation required")
				return
			}
			if err := database.DeleteUserMFA(u.ID, chi.URLParam(r, "id")); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.With(authLimiter.Middleware).Post("/api/auth/mfa/disable", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			if u.RequireMFA {
				writeErr(w, http.StatusBadRequest, "MFA is required by your administrator and cannot be disabled")
				return
			}
			var req struct {
				Code string `json:"code"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if err := authSvc.DisableTOTP(u, req.Code); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/auth/passkey/stepup/begin", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			opts, cid, err := authSvc.BeginPasskeyStepUp(u)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"options": opts, "challenge_id": cid})
		})

		r.Get("/api/auth/passkeys", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			list, err := database.ListPasskeysByUser(u.ID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"passkeys": list})
		})

		r.Post("/api/auth/passkey/register/begin", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				Label string `json:"label"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			opts, cid, err := authSvc.BeginPasskeyRegister(u, config.SanitizeText(req.Label, 64))
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"options": opts, "challenge_id": cid})
		})

		r.Post("/api/auth/passkey/register/finish", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			cid := r.URL.Query().Get("challenge_id")
			if err := authSvc.FinishPasskeyRegister(u, cid, r); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Patch("/api/auth/passkeys/{id}", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				Label string `json:"label"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if err := database.RenamePasskey(u.ID, chi.URLParam(r, "id"), config.SanitizeText(req.Label, 64)); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Delete("/api/auth/passkeys/{id}", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			pks, _ := database.ListPasskeysByUser(u.ID)
			if u.PasswordLoginDisabled && len(pks) <= 1 {
				writeErr(w, http.StatusBadRequest, "Cannot remove your only Passkey while password login is disabled")
				return
			}
			if u.RequirePasskey && len(pks) <= 1 {
				writeErr(w, http.StatusBadRequest, "Passkey is required by your administrator and cannot be removed")
				return
			}
			var req struct {
				Password           string          `json:"password"`
				PasskeyChallengeID string          `json:"passkey_challenge_id"`
				PasskeyAssertion   json.RawMessage `json:"passkey_assertion"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if u.PasswordLoginDisabled {
				if err := authSvc.FinishPasskeyStepUp(u, req.PasskeyChallengeID, req.PasskeyAssertion, r); err != nil {
					writeErr(w, http.StatusForbidden, "Passkey verification required")
					return
				}
			} else if err := authSvc.VerifyUserPassword(u, req.Password); err != nil {
				writeErr(w, http.StatusForbidden, "Password confirmation required")
				return
			}
			_ = database.DeletePasskey(u.ID, chi.URLParam(r, "id"))
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		// API Keys
		r.Get("/api/api-keys", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var keys []db.APIKey
			var err error
			if u.Role == auth.RoleAdmin {
				keys, err = database.ListAllAPIKeys()
			} else {
				keys, err = database.ListAPIKeys(u.ID)
			}
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"api_keys": keys})
		})

		r.Post("/api/api-keys", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			if u.Role != auth.RoleAdmin && !u.CanCreateAPIKeys {
				writeErr(w, http.StatusForbidden, "API key creation is disabled for your account")
				return
			}
			var req struct {
				Name             string   `json:"name"`
				Scopes           []string `json:"scopes"`
				AllowedServers   []string `json:"allowed_servers"`
				AllowedLibraries []string `json:"allowed_libraries"`
				ExpiresInDays    int      `json:"expires_in_days"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if u.Role != auth.RoleAdmin {
				if u.Role == auth.RoleOperator {
					filtered := make([]string, 0, len(req.Scopes))
					for _, sc := range req.Scopes {
						if sc != "admin" {
							filtered = append(filtered, sc)
						}
					}
					req.Scopes = filtered
				} else {
					filtered := make([]string, 0, len(req.Scopes))
					for _, sc := range req.Scopes {
						if sc == "read" {
							filtered = append(filtered, sc)
						}
					}
					req.Scopes = filtered
				}
			}
			cleanServers := make([]string, 0, len(req.AllowedServers))
			seen := map[string]bool{}
			for _, raw := range req.AllowedServers {
				s := config.SanitizeText(raw, 64)
				if s == "" || s == "*" || seen[s] {
					continue
				}
				if err := config.ValidateServerID(s); err != nil {
					writeErr(w, http.StatusBadRequest, err.Error())
					return
				}
				if !auth.CanAccessServer(u, s) {
					writeErr(w, http.StatusForbidden, "Cannot grant API key access to a server you do not have access to")
					return
				}
				seen[s] = true
				cleanServers = append(cleanServers, s)
			}
			var expiresAt int64
			if req.ExpiresInDays > 0 {
				if req.ExpiresInDays > 3650 {
					req.ExpiresInDays = 3650
				}
				expiresAt = time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).Unix()
			}
			raw, rec, err := authSvc.CreateAPIKey(u.ID, config.SanitizeText(req.Name, 64), req.Scopes, cleanServers, config.SanitizeStringSlice(req.AllowedLibraries, 256, 128), expiresAt)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			database.AddEventUser("", "info", "config", u.Username, "Created API key: "+rec.Name)
			writeJSON(w, http.StatusCreated, map[string]any{"api_key": raw, "record": rec})
		})

		r.Patch("/api/api-keys/{id}", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			var req struct {
				Name string `json:"name"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			name := config.SanitizeText(req.Name, 64)
			if name == "" {
				writeErr(w, http.StatusBadRequest, "API key name is required")
				return
			}
			if err := database.RenameAPIKey(chi.URLParam(r, "id"), u.ID, u.Role == auth.RoleAdmin, name); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Delete("/api/api-keys/{id}", func(w http.ResponseWriter, r *http.Request) {
			u := auth.UserFromContext(r.Context())
			if u.Role == auth.RoleAdmin {
				_ = database.DeleteAPIKeyByID(chi.URLParam(r, "id"))
			} else {
				_ = database.DeleteAPIKey(u.ID, chi.URLParam(r, "id"))
			}
			database.AddEventUser("", "info", "config", u.Username, "Revoked API key")
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})
	})

	// --- Admin User & Server Management ---
	r.Group(func(r chi.Router) {
		r.Use(authSvc.RequireSessionOrAPIKey("admin"))

		r.Get("/api/users", func(w http.ResponseWriter, r *http.Request) {
			users, err := database.ListUsers()
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"users": users})
		})

		r.Post("/api/users", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Username         string   `json:"username"`
				Password         string   `json:"password"`
				DisplayName      string   `json:"display_name"`
				Email            string   `json:"email"`
				Role             string   `json:"role"`
				AllowedServers   []string `json:"allowed_servers"`
				CanCreateAPIKeys *bool    `json:"can_create_api_keys"`
				RequireMFA       bool     `json:"require_mfa"`
				RequirePasskey   bool     `json:"require_passkey"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			req.Username = config.SanitizeText(req.Username, 64)
			req.DisplayName = config.SanitizeText(req.DisplayName, 128)
			req.Email = config.SanitizeText(req.Email, 254)
			if err := config.ValidateUsername(req.Username); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if len(req.Password) > 256 {
				writeErr(w, http.StatusBadRequest, "Password exceeds maximum length")
				return
			}
			u, err := authSvc.CreateUser(req.Username, req.Password, req.DisplayName, req.Email, req.Role, req.AllowedServers)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			canKeys := u.CanCreateAPIKeys
			if req.CanCreateAPIKeys != nil {
				canKeys = *req.CanCreateAPIKeys
			}
			if u.Role == auth.RoleAdmin {
				canKeys = true
			}
			if canKeys != u.CanCreateAPIKeys || req.RequireMFA || req.RequirePasskey {
				_ = database.UpdateUserSecurityPolicy(u.ID, canKeys, req.RequireMFA, req.RequirePasskey)
				u.CanCreateAPIKeys = canKeys
				u.RequireMFA = req.RequireMFA
				u.RequirePasskey = req.RequirePasskey
			}
			writeJSON(w, http.StatusCreated, map[string]any{"user": u})
		})

		r.Patch("/api/users/{id}/policy", func(w http.ResponseWriter, r *http.Request) {
			target := chi.URLParam(r, "id")
			var req struct {
				CanCreateAPIKeys bool `json:"can_create_api_keys"`
				RequireMFA       bool `json:"require_mfa"`
				RequirePasskey   bool `json:"require_passkey"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			u, err := database.GetUserByID(target)
			if err != nil || u == nil {
				writeErr(w, http.StatusNotFound, "User not found")
				return
			}
			canKeys := req.CanCreateAPIKeys
			if u.Role == auth.RoleAdmin {
				canKeys = true
			}
			if err := database.UpdateUserSecurityPolicy(target, canKeys, req.RequireMFA, req.RequirePasskey); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                  true,
				"can_create_api_keys": canKeys,
				"require_mfa":         req.RequireMFA,
				"require_passkey":     req.RequirePasskey,
			})
		})

		r.Post("/api/users/{id}/password", func(w http.ResponseWriter, r *http.Request) {
			target := chi.URLParam(r, "id")
			var req struct {
				Password   string `json:"password"`
				MustChange bool   `json:"must_change"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if len(req.Password) > 256 {
				writeErr(w, http.StatusBadRequest, "Password exceeds maximum length")
				return
			}
			if err := authSvc.AdminSetPassword(target, req.Password, req.MustChange); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "must_change": req.MustChange})
		})

		r.Post("/api/users/{id}/reset-auth", func(w http.ResponseWriter, r *http.Request) {
			target := chi.URLParam(r, "id")
			var req struct {
				MFA      bool `json:"mfa"`
				Passkeys bool `json:"passkeys"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if u, err := database.GetUserByID(target); err != nil || u == nil {
				writeErr(w, http.StatusNotFound, "User not found")
				return
			}
			if req.MFA {
				if err := database.ResetUserMFA(target); err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			if req.Passkeys {
				if err := database.ResetUserPasskeys(target); err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			_ = database.DeleteUserSessions(target)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/users/{id}/kill-sessions", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			target := chi.URLParam(r, "id")
			if err := database.DeleteUserSessions(target); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			database.AddEventUser("", "info", "config", me.Username, "Terminated active sessions for user "+target)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Delete("/api/users/{id}/api-keys", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			target := chi.URLParam(r, "id")
			if err := database.DeleteUserAPIKeys(target); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			database.AddEventUser("", "info", "config", me.Username, "Revoked all API keys for user "+target)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/users/{id}/revoke-oidc", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			target := chi.URLParam(r, "id")
			if err := database.RevokeUserOIDC(target); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			database.AddEventUser("", "info", "config", me.Username, "Revoked OIDC association for user "+target)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		// Consolidated user save (role, server access, policies, disable password login)
		r.Patch("/api/users/{id}", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			target := chi.URLParam(r, "id")
			var req struct {
				Role                  string   `json:"role"`
				AllowedServers        []string `json:"allowed_servers"`
				CanCreateAPIKeys      bool     `json:"can_create_api_keys"`
				RequireMFA            bool     `json:"require_mfa"`
				RequirePasskey        bool     `json:"require_passkey"`
				PasswordLoginDisabled bool     `json:"password_login_disabled"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			u, err := database.GetUserByID(target)
			if err != nil || u == nil {
				writeErr(w, http.StatusNotFound, "User not found")
				return
			}
			if req.Role != "" {
				if me.ID == target && req.Role != auth.RoleAdmin {
					writeErr(w, http.StatusBadRequest, "Cannot demote yourself")
					return
				}
				if req.Role != auth.RoleAdmin && req.Role != auth.RoleOperator && req.Role != auth.RoleViewer {
					writeErr(w, http.StatusBadRequest, "Invalid role")
					return
				}
				if err := database.UpdateUserRole(target, req.Role); err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return
				}
				u.Role = req.Role
			}
			if req.PasswordLoginDisabled && u.PasskeyCount == 0 {
				writeErr(w, http.StatusBadRequest, "User must have at least one Passkey registered before disabling password login")
				return
			}
			if err := database.SetPasswordLoginDisabled(target, req.PasswordLoginDisabled); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			clean := make([]string, 0, len(req.AllowedServers))
			seen := map[string]bool{}
			for _, raw := range req.AllowedServers {
				s := config.SanitizeText(raw, 64)
				if s == "" || seen[s] {
					continue
				}
				if s != "*" {
					if err := config.ValidateServerID(s); err != nil {
						writeErr(w, http.StatusBadRequest, err.Error())
						return
					}
				}
				seen[s] = true
				clean = append(clean, s)
			}
			if u.Role == auth.RoleAdmin {
				clean = []string{}
			}
			if err := database.UpdateUserAllowedServers(target, clean); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			canKeys := req.CanCreateAPIKeys || u.Role == auth.RoleAdmin
			if err := database.UpdateUserSecurityPolicy(target, canKeys, req.RequireMFA, req.RequirePasskey); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			database.AddEventUser("", "info", "config", me.Username, "Updated user "+u.Username)
			updated, _ := database.GetUserByID(target)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": updated})
		})

		r.Get("/api/admin/oidc", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, authSvc.GetOIDCSettings())
		})

		r.Put("/api/admin/oidc", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			var req struct {
				Issuer         string   `json:"issuer"`
				ClientID       string   `json:"client_id"`
				ClientSecret   string   `json:"client_secret"`
				RedirectURL    string   `json:"redirect_url"`
				AutoCreate     bool     `json:"auto_create"`
				GroupsClaim    string   `json:"groups_claim"`
				AdminGroups    []string `json:"admin_groups"`
				OperatorGroups []string `json:"operator_groups"`
				ViewerGroups   []string `json:"viewer_groups"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if err := authSvc.UpdateOIDCSettings(req.Issuer, req.ClientID, req.ClientSecret, req.RedirectURL, req.AutoCreate, req.GroupsClaim, req.AdminGroups, req.OperatorGroups, req.ViewerGroups); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			database.AddEventUser("", "info", "config", me.Username, "Updated OIDC settings")
			writeJSON(w, http.StatusOK, authSvc.GetOIDCSettings())
		})

		r.Post("/api/admin/oidc/test", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Issuer string `json:"issuer"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			res, err := authSvc.TestOIDCDiscovery(r.Context(), req.Issuer)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, res)
		})

		r.Get("/api/admin/security-settings", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"session_ip_binding":   authSvc.IsSessionIPBindingEnabled(),
				"update_check_enabled": isUpdateCheckEnabled(database),
			})
		})

		r.Put("/api/admin/security-settings", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			var req struct {
				SessionIPBinding   bool `json:"session_ip_binding"`
				UpdateCheckEnabled bool `json:"update_check_enabled"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			val := "false"
			if req.SessionIPBinding {
				val = "true"
			}
			if err := database.SetAppSetting("session_ip_binding", val); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			updVal := "false"
			if req.UpdateCheckEnabled {
				updVal = "true"
			}
			if err := database.SetAppSetting("update_check_enabled", updVal); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			versionCache.mu.Lock()
			versionCache.checkedAt = time.Time{}
			versionCache.mu.Unlock()
			database.AddEventUser("", "info", "config", me.Username, "Updated security settings (session_ip_binding="+val+", update_check_enabled="+updVal+")")
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                   true,
				"session_ip_binding":   req.SessionIPBinding,
				"update_check_enabled": req.UpdateCheckEnabled,
			})
		})

		r.Patch("/api/users/{id}/servers", func(w http.ResponseWriter, r *http.Request) {
			target := chi.URLParam(r, "id")
			var req struct {
				AllowedServers []string `json:"allowed_servers"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			clean := make([]string, 0, len(req.AllowedServers))
			seen := map[string]bool{}
			for _, raw := range req.AllowedServers {
				s := config.SanitizeText(raw, 64)
				if s == "" || seen[s] {
					continue
				}
				if s != "*" {
					if err := config.ValidateServerID(s); err != nil {
						writeErr(w, http.StatusBadRequest, err.Error())
						return
					}
				}
				seen[s] = true
				clean = append(clean, s)
			}
			if err := database.UpdateUserAllowedServers(target, clean); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "allowed_servers": clean})
		})

		r.Patch("/api/users/{id}/role", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			target := chi.URLParam(r, "id")
			var req struct {
				Role string `json:"role"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if me.ID == target && req.Role != auth.RoleAdmin {
				writeErr(w, http.StatusBadRequest, "Cannot demote yourself")
				return
			}
			if req.Role != auth.RoleAdmin && req.Role != auth.RoleOperator && req.Role != auth.RoleViewer {
				writeErr(w, http.StatusBadRequest, "Invalid role")
				return
			}
			_ = database.UpdateUserRole(target, req.Role)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Delete("/api/users/{id}", func(w http.ResponseWriter, r *http.Request) {
			me := auth.UserFromContext(r.Context())
			target := chi.URLParam(r, "id")
			if me.ID == target {
				writeErr(w, http.StatusBadRequest, "Cannot delete yourself")
				return
			}
			_ = database.DeleteUser(target)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/servers", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Name      string `json:"name"`
				Kind      string `json:"kind"`
				URL       string `json:"url"`
				Token     string `json:"token"`
				SSLVerify bool   `json:"ssl_verify"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if req.Kind == "" {
				req.Kind = "plex"
			}
			cleanTok, err := config.ValidateSecretValue(req.Token, "token")
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			srv, err := mgr.AddServer(config.SanitizeText(req.Name, 128), config.SanitizeText(req.Kind, 32), req.URL, cleanTok, req.SSLVerify)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"server": srv})
		})

		r.Delete("/api/servers/{serverID}", func(w http.ResponseWriter, r *http.Request) {
			if err := mgr.DeleteServer(chi.URLParam(r, "serverID")); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})
	})
}
