package engine

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/crypto"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/media"
	"github.com/fabsau/sweeparr/internal/media/plex"
)

const gib = 1024 * 1024 * 1024

type AdapterFactory func(srv db.ServerRecord, token string) media.Adapter

type Manager struct {
	db      *db.DB
	env     config.EnvConfig
	factory AdapterFactory

	mu      sync.RWMutex
	workers map[string]*ServerWorker
	order   []string
}

func NewManager(database *db.DB, env config.EnvConfig, factory AdapterFactory) (*Manager, error) {
	if factory == nil {
		factory = func(srv db.ServerRecord, token string) media.Adapter {
			return plex.NewClient(srv.URL, token, srv.SSLVerify)
		}
	}
	m := &Manager{
		db:      database,
		env:     env,
		factory: factory,
		workers: map[string]*ServerWorker{},
	}
	if err := m.bootstrapServers(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) bootstrapServers() error {
	existing, err := m.db.ListServers()
	if err != nil {
		return err
	}
	existingByID := map[string]db.ServerRecord{}
	for _, s := range existing {
		existingByID[s.ID] = s
	}

	if m.env.ConfigManaged && len(m.env.Servers) > 0 {
		for _, spec := range m.env.Servers {
			encTok, err := crypto.Encrypt(m.env.MasterKey, spec.Token)
			if err != nil {
				return err
			}
			cfg := spec.Config
			if prev, ok := existingByID[spec.ID]; ok {
				// Preserve GUI display preferences & runtime pause state across restarts
				if prev.Config.NumberLocale != "" {
					cfg.NumberLocale = prev.Config.NumberLocale
				}
				if prev.Config.DateTimeLocale != "" {
					cfg.DateTimeLocale = prev.Config.DateTimeLocale
				}
				if prev.Config.TimeFormat != "" {
					cfg.TimeFormat = prev.Config.TimeFormat
				}
				if prev.Config.Theme != "" {
					cfg.Theme = prev.Config.Theme
				}
				if prev.Config.AccentColor != "" {
					cfg.AccentColor = prev.Config.AccentColor
				}
				if prev.Config.DefaultPageSize > 0 {
					cfg.DefaultPageSize = prev.Config.DefaultPageSize
				}
				cfg.Paused = prev.Config.Paused
				cfg.PausedUntil = prev.Config.PausedUntil
			}
			rec := db.ServerRecord{
				ID:         spec.ID,
				Name:       spec.Name,
				Kind:       spec.Kind,
				URL:        spec.URL,
				TokenEnc:   encTok,
				SSLVerify:  spec.SSLVerify,
				EnvManaged: true,
				Enabled:    spec.Enabled,
				Config:     config.SanitizeScannerConfig(cfg),
				CreatedAt:  time.Now().Unix(),
			}
			if prev, ok := existingByID[spec.ID]; ok {
				rec.CreatedAt = prev.CreatedAt
			}
			if err := m.db.UpsertServer(rec); err != nil {
				return err
			}
			w := newServerWorker(m.db, rec, m.factory(rec, spec.Token), true)
			m.workers[rec.ID] = w
			m.order = append(m.order, rec.ID)
		}
		return nil
	}

	if len(existing) == 0 && m.env.PlexURL != "" {
		encTok, err := crypto.Encrypt(m.env.MasterKey, m.env.PlexToken)
		if err != nil {
			return err
		}
		def := db.ServerRecord{
			ID:         "default",
			Name:       "Primary Server",
			Kind:       "plex",
			URL:        m.env.PlexURL,
			TokenEnc:   encTok,
			SSLVerify:  m.env.PlexSSLVerify,
			EnvManaged: true,
			Enabled:    true,
			Config:     config.DefaultScannerConfig(),
			CreatedAt:  time.Now().Unix(),
		}
		if err := m.db.UpsertServer(def); err != nil {
			return err
		}
		existing = []db.ServerRecord{def}
	}
	for _, s := range existing {
		tok, _ := crypto.Decrypt(m.env.MasterKey, s.TokenEnc)
		if s.EnvManaged && m.env.PlexToken != "" {
			tok = m.env.PlexToken
			s.URL = m.env.PlexURL
			s.SSLVerify = m.env.PlexSSLVerify
		}
		w := newServerWorker(m.db, s, m.factory(s, tok), false)
		m.workers[s.ID] = w
		m.order = append(m.order, s.ID)
	}
	return nil
}

func (m *Manager) StartAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, w := range m.workers {
		w.Start()
	}
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, w := range m.workers {
		w.Stop()
	}
}

type ServerSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	URL        string `json:"url"`
	SSLVerify  bool   `json:"ssl_verify"`
	EnvManaged bool   `json:"env_managed"`
	Enabled    bool   `json:"enabled"`
	Connected  bool   `json:"connected"`
	ServerName string `json:"server_name"`
	Version    string `json:"version"`
}

func (m *Manager) ListServers() []ServerSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerSummary, 0, len(m.workers))
	seen := make(map[string]bool, len(m.workers))
	for _, id := range m.order {
		if w, ok := m.workers[id]; ok {
			out = append(out, w.Summary())
			seen[id] = true
		}
	}
	if len(seen) < len(m.workers) {
		var extra []ServerSummary
		for id, w := range m.workers {
			if !seen[id] {
				extra = append(extra, w.Summary())
			}
		}
		sort.Slice(extra, func(i, j int) bool { return extra[i].ID < extra[j].ID })
		out = append(out, extra...)
	}
	return out
}

func (m *Manager) GetWorker(id string) *ServerWorker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workers[id]
}

func (m *Manager) AddServer(name, kind, rawURL, token string, sslVerify bool) (ServerSummary, error) {
	if m.env.ConfigManaged {
		return ServerSummary{}, fmt.Errorf("servers are strictly managed by config.yaml")
	}
	if kind != "plex" {
		return ServerSummary{}, fmt.Errorf("unsupported server kind %q (only 'plex' is currently enabled)", kind)
	}
	cleanURL, err := config.ValidatePlexURL(rawURL, m.env.AllowedHosts...)
	if err != nil {
		return ServerSummary{}, err
	}
	encTok, err := crypto.Encrypt(m.env.MasterKey, token)
	if err != nil {
		return ServerSummary{}, err
	}
	rec := db.ServerRecord{
		ID:         uuid.NewString()[:8],
		Name:       name,
		Kind:       kind,
		URL:        cleanURL,
		TokenEnc:   encTok,
		SSLVerify:  sslVerify,
		EnvManaged: false,
		Enabled:    true,
		Config:     config.DefaultScannerConfig(),
		CreatedAt:  time.Now().Unix(),
	}
	if err := m.db.UpsertServer(rec); err != nil {
		return ServerSummary{}, err
	}
	w := newServerWorker(m.db, rec, m.factory(rec, token), false)
	w.Start()
	m.mu.Lock()
	m.workers[rec.ID] = w
	m.order = append(m.order, rec.ID)
	m.mu.Unlock()
	return w.Summary(), nil
}

func (m *Manager) DeleteServer(id string) error {
	if m.env.ConfigManaged {
		return fmt.Errorf("servers are strictly managed by config.yaml")
	}
	m.mu.Lock()
	w, ok := m.workers[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("server not found")
	}
	if w.rec.EnvManaged {
		m.mu.Unlock()
		return fmt.Errorf("cannot delete environment-managed primary server")
	}
	delete(m.workers, id)
	nextOrder := m.order[:0]
	for _, oid := range m.order {
		if oid != id {
			nextOrder = append(nextOrder, oid)
		}
	}
	m.order = nextOrder
	m.mu.Unlock()
	w.Stop()
	return m.db.DeleteServer(id)
}
