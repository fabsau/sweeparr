package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/fabsau/sweeparr/internal/db"
)

type webAuthnUser struct {
	u      *db.User
	creds  []webauthn.Credential
	recIDs []string
}

func (w webAuthnUser) WebAuthnID() []byte   { return []byte(w.u.ID) }
func (w webAuthnUser) WebAuthnName() string { return w.u.Username }
func (w webAuthnUser) WebAuthnDisplayName() string {
	if w.u.DisplayName != "" {
		return w.u.DisplayName
	}
	return w.u.Username
}
func (w webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return w.creds
}

func (s *Service) loadWAUser(u *db.User) (webAuthnUser, error) {
	stored, err := s.db.ListPasskeysByUser(u.ID)
	if err != nil {
		return webAuthnUser{}, err
	}
	creds := make([]webauthn.Credential, 0, len(stored))
	recIDs := make([]string, 0, len(stored))
	for _, st := range stored {
		var c webauthn.Credential
		if json.Unmarshal([]byte(st.CredentialJSON), &c) == nil {
			creds = append(creds, c)
			recIDs = append(recIDs, st.ID)
		}
	}
	return webAuthnUser{u: u, creds: creds, recIDs: recIDs}, nil
}

func (s *Service) BeginPasskeyRegister(u *db.User, label string) (any, string, error) {
	wu, err := s.loadWAUser(u)
	if err != nil {
		return nil, "", err
	}
	creation, sessData, err := s.wa.BeginRegistration(wu, webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
		ResidentKey:        protocol.ResidentKeyRequirementPreferred,
		RequireResidentKey: protocol.ResidentKeyNotRequired(),
		UserVerification:   protocol.VerificationPreferred,
	}))
	if err != nil {
		return nil, "", err
	}
	cid := uuid.NewString()
	if strings.TrimSpace(label) == "" {
		label = "Passkey"
	}
	s.mu.Lock()
	s.pruneChallengesLocked()
	s.waSessions[cid] = WebAuthnChallenge{
		SessionData: *sessData,
		UserID:      u.ID,
		Label:       strings.TrimSpace(label),
		ExpiresAt:   time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()
	return creation, cid, nil
}

func (s *Service) FinishPasskeyRegister(u *db.User, challengeID string, r *http.Request) error {
	s.mu.Lock()
	ch, ok := s.waSessions[challengeID]
	delete(s.waSessions, challengeID)
	s.mu.Unlock()
	if !ok || ch.UserID != u.ID || time.Now().After(ch.ExpiresAt) {
		return errors.New("passkey challenge expired or invalid")
	}
	wu, err := s.loadWAUser(u)
	if err != nil {
		return err
	}
	cred, err := s.wa.FinishRegistration(wu, ch.SessionData, r)
	if err != nil {
		slog.Warn("passkey registration failed", "username", u.Username, "error", err)
		return err
	}
	slog.Info("registered passkey", "username", u.Username, "label", ch.Label)
	b, _ := json.Marshal(cred)
	return s.db.AddPasskey(db.StoredPasskey{
		ID:             uuid.NewString(),
		UserID:         u.ID,
		Label:          ch.Label,
		CredentialJSON: string(b),
		CreatedAt:      time.Now().Unix(),
	})
}

func (s *Service) BeginPasskeyLogin(username string) (any, string, error) {
	uname := strings.TrimSpace(username)
	if uname == "" {
		assertion, sessData, err := s.wa.BeginDiscoverableLogin()
		if err != nil {
			return nil, "", errors.New("authentication failed")
		}
		cid := uuid.NewString()
		s.mu.Lock()
		s.pruneChallengesLocked()
		s.waSessions[cid] = WebAuthnChallenge{
			SessionData: *sessData,
			UserID:      "",
			ExpiresAt:   time.Now().Add(5 * time.Minute),
		}
		s.mu.Unlock()
		return assertion, cid, nil
	}
	u, err := s.db.GetUserByUsername(uname)
	if err != nil || u == nil {
		return nil, "", errors.New("authentication failed")
	}
	wu, err := s.loadWAUser(u)
	if err != nil || len(wu.creds) == 0 {
		return nil, "", errors.New("authentication failed")
	}
	assertion, sessData, err := s.wa.BeginLogin(wu)
	if err != nil {
		return nil, "", errors.New("authentication failed")
	}
	cid := uuid.NewString()
	s.mu.Lock()
	s.pruneChallengesLocked()
	s.waSessions[cid] = WebAuthnChallenge{
		SessionData: *sessData,
		UserID:      u.ID,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()
	return assertion, cid, nil
}

func (s *Service) FinishPasskeyLogin(challengeID string, r *http.Request) (*db.User, string, error) {
	s.mu.Lock()
	ch, ok := s.waSessions[challengeID]
	delete(s.waSessions, challengeID)
	s.mu.Unlock()
	if !ok || time.Now().After(ch.ExpiresAt) {
		return nil, "", errors.New("authentication failed")
	}
	var u *db.User
	var wu webAuthnUser
	var cred *webauthn.Credential
	var err error
	if ch.UserID == "" {
		cred, err = s.wa.FinishDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
			found, gErr := s.db.GetUserByID(string(userHandle))
			if gErr != nil || found == nil {
				return nil, errors.New("authentication failed")
			}
			u = found
			loaded, lErr := s.loadWAUser(found)
			if lErr != nil {
				return nil, lErr
			}
			wu = loaded
			return loaded, nil
		}, ch.SessionData, r)
		if err != nil || u == nil {
			slog.Warn("discoverable passkey login failed", "error", err)
			return nil, "", errors.New("authentication failed")
		}
	} else {
		u, err = s.db.GetUserByID(ch.UserID)
		if err != nil || u == nil {
			return nil, "", errors.New("authentication failed")
		}
		wu, err = s.loadWAUser(u)
		if err != nil {
			return nil, "", errors.New("authentication failed")
		}
		cred, err = s.wa.FinishLogin(wu, ch.SessionData, r)
		if err != nil {
			slog.Warn("passkey login failed", "username", u.Username, "error", err)
			return nil, "", errors.New("authentication failed")
		}
	}
	slog.Info("passkey login succeeded", "username", u.Username)
	if cred != nil {
		for i, existing := range wu.creds {
			if string(existing.ID) == string(cred.ID) && i < len(wu.recIDs) {
				if b, err := json.Marshal(cred); err == nil {
					_ = s.db.UpdatePasskeyJSON(wu.recIDs[i], string(b))
				}
				break
			}
		}
	}
	mfas, _ := s.db.ListUserMFA(u.ID)
	requireMFA := u.PasskeyRequireMFA && (u.TOTPEnabled || len(mfas) > 0)
	rawTok, _, err := s.issueSession(u.ID, requireMFA, RequestClientIP(r))
	return u, rawTok, err
}

func (s *Service) BeginPasskeyStepUp(u *db.User) (any, string, error) {
	wu, err := s.loadWAUser(u)
	if err != nil || len(wu.creds) == 0 {
		return nil, "", errors.New("no passkeys registered")
	}
	assertion, sessData, err := s.wa.BeginLogin(wu)
	if err != nil {
		return nil, "", err
	}
	cid := uuid.NewString()
	s.mu.Lock()
	s.pruneChallengesLocked()
	s.waSessions[cid] = WebAuthnChallenge{
		SessionData: *sessData,
		UserID:      u.ID,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()
	return assertion, cid, nil
}

func (s *Service) FinishPasskeyStepUp(u *db.User, challengeID string, assertionBytes []byte, origReq *http.Request) error {
	s.mu.Lock()
	ch, ok := s.waSessions[challengeID]
	delete(s.waSessions, challengeID)
	s.mu.Unlock()
	if !ok || ch.UserID != u.ID || time.Now().After(ch.ExpiresAt) {
		return errors.New("passkey verification failed")
	}
	if len(assertionBytes) == 0 {
		return errors.New("passkey verification failed")
	}
	wu, err := s.loadWAUser(u)
	if err != nil {
		return err
	}
	stepReq, err := http.NewRequestWithContext(origReq.Context(), http.MethodPost, origReq.URL.String(), bytes.NewReader(assertionBytes))
	if err != nil {
		return errors.New("passkey verification failed")
	}
	stepReq.Host = origReq.Host
	stepReq.Header = origReq.Header.Clone()
	cred, err := s.wa.FinishLogin(wu, ch.SessionData, stepReq)
	if err != nil {
		return errors.New("passkey verification failed")
	}
	if cred != nil {
		for i, existing := range wu.creds {
			if string(existing.ID) == string(cred.ID) && i < len(wu.recIDs) {
				if b, err := json.Marshal(cred); err == nil {
					_ = s.db.UpdatePasskeyJSON(wu.recIDs[i], string(b))
				}
				break
			}
		}
	}
	return nil
}
