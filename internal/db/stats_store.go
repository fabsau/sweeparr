package db

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/fabsau/sweeparr/internal/config"
)

// --- Servers ---

type ServerRecord struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Kind       string               `json:"kind"`
	URL        string               `json:"url"`
	TokenEnc   string               `json:"-"`
	SSLVerify  bool                 `json:"ssl_verify"`
	EnvManaged bool                 `json:"env_managed"`
	Enabled    bool                 `json:"enabled"`
	Config     config.ScannerConfig `json:"config"`
	CreatedAt  int64                `json:"created_at"`
}

func (d *DB) ListServers() ([]ServerRecord, error) {
	rows, err := d.sql.Query(`SELECT id, name, kind, url, token_enc, ssl_verify, env_managed, enabled, config_json, created_at FROM servers ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerRecord
	for rows.Next() {
		var s ServerRecord
		var sslInt, envInt, enInt int
		var cfgRaw string
		if err := rows.Scan(&s.ID, &s.Name, &s.Kind, &s.URL, &s.TokenEnc, &sslInt, &envInt, &enInt, &cfgRaw, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.SSLVerify = sslInt != 0
		s.EnvManaged = envInt != 0
		s.Enabled = enInt != 0
		s.Config = config.DefaultScannerConfig()
		_ = json.Unmarshal([]byte(cfgRaw), &s.Config)
		s.Config = config.SanitizeScannerConfig(s.Config)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) UpsertServer(s ServerRecord) error {
	cfgBytes, _ := json.Marshal(config.SanitizeScannerConfig(s.Config))
	_, err := d.sql.Exec(
		`INSERT INTO servers(id, name, kind, url, token_enc, ssl_verify, env_managed, enabled, config_json, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   name=excluded.name, url=excluded.url, token_enc=CASE WHEN excluded.token_enc!='' THEN excluded.token_enc ELSE servers.token_enc END,
		   ssl_verify=excluded.ssl_verify, enabled=excluded.enabled, config_json=excluded.config_json`,
		s.ID, s.Name, s.Kind, s.URL, s.TokenEnc, boolInt(s.SSLVerify), boolInt(s.EnvManaged), boolInt(s.Enabled), string(cfgBytes), s.CreatedAt,
	)
	return err
}

func (d *DB) UpdateServerConfig(serverID string, cfg config.ScannerConfig) error {
	cfgBytes, _ := json.Marshal(config.SanitizeScannerConfig(cfg))
	_, err := d.sql.Exec(`UPDATE servers SET config_json=? WHERE id=?`, string(cfgBytes), serverID)
	return err
}

func (d *DB) DeleteServer(serverID string) error {
	_, err := d.sql.Exec(`DELETE FROM servers WHERE id=? AND env_managed=0`, serverID)
	return err
}

// --- Capability Aggregates & Library Prefs ---

type CapStat struct {
	Have    int     `json:"have"`
	Missing int     `json:"missing"`
	Total   int     `json:"total"`
	Pct     float64 `json:"pct"`
}

type Aggregates struct {
	Total     int                `json:"total"`
	Complete  int                `json:"complete"`
	Pending   int                `json:"pending"`
	Retry     int                `json:"retry"`
	Exhausted int                `json:"exhausted"`
	Blocked   int                `json:"blocked"`
	Skipped   int                `json:"skipped"`
	Pct       float64            `json:"pct"`
	CapStats  map[string]CapStat `json:"cap_stats"`
}

type LibrarySummary struct {
	Title        string             `json:"title"`
	Kind         string             `json:"kind"`
	Total        int                `json:"total"`
	Complete     int                `json:"complete"`
	Pending      int                `json:"pending"`
	Pct          float64            `json:"pct"`
	DisabledCaps []string           `json:"disabled_caps"`
	CapModes     map[string]string  `json:"cap_modes"`
	CapStats     map[string]CapStat `json:"cap_stats"`
}

func makeCapStat(total, missing int) CapStat {
	have := max(0, total-missing)
	pct := 0.0
	if total > 0 {
		pct = float64(have) / float64(total) * 100.0
	}
	return CapStat{Have: have, Missing: missing, Total: total, Pct: pct}
}

func capAppliesToKind(capName, kind string) bool {
	switch capName {
	case "loudness":
		return true
	case "sonic":
		return kind == "track"
	case "intro":
		return kind == "episode"
	case "voice", "chapters", "credits", "ads", "previews", "deep_analysis":
		return kind == "movie" || kind == "episode"
	default:
		return false
	}
}

func (d *DB) GetAggregates(serverID string) (Aggregates, []LibrarySummary, error) {
	agg := Aggregates{CapStats: map[string]CapStat{}}
	disabledMap := d.GetDisabledCapsMap(serverID)
	capModesMap := d.GetCapModesMap(serverID)
	var srvCfg config.ScannerConfig
	var cfgRaw string
	if err := d.sql.QueryRow(`SELECT config_json FROM servers WHERE id=?`, serverID).Scan(&cfgRaw); err == nil {
		_ = json.Unmarshal([]byte(cfgRaw), &srvCfg)
	}
	rows, err := d.sql.Query(`
		SELECT library,
		       COUNT(*),
		       SUM(CASE WHEN kind='movie' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN kind='episode' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN kind='track' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status='complete' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status='retry' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status='exhausted' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status='blocked' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status='skipped' THEN 1 ELSE 0 END),
		       SUM(miss_loudness), SUM(miss_voice), SUM(miss_chapters), SUM(miss_intro), SUM(miss_credits), SUM(miss_ads), SUM(miss_previews), SUM(miss_deep_analysis), SUM(miss_sonic),
		       SUM(CASE WHEN is_dvr=1 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN is_dvr=1 AND miss_ads=1 THEN 1 ELSE 0 END)
		FROM items WHERE server_id=? GROUP BY library ORDER BY LOWER(library) ASC`, serverID)
	if err != nil {
		return agg, nil, err
	}
	defer rows.Close()

	libs := []LibrarySummary{}
	capMissingTotal := map[string]int{}
	capEligibleTotal := map[string]int{}
	for rows.Next() {
		var lib string
		var tot, movies, episodes, tracks, comp, pend, ret, exh, blk, skp int
		var mL, mV, mC, mI, mCr, mA, mP, mD, mS int
		var dvrCount, dvrMissAds int
		if err := rows.Scan(&lib, &tot, &movies, &episodes, &tracks, &comp, &pend, &ret, &exh, &blk, &skp, &mL, &mV, &mC, &mI, &mCr, &mA, &mP, &mD, &mS, &dvrCount, &dvrMissAds); err != nil {
			return agg, nil, err
		}
		agg.Total += tot
		agg.Complete += comp
		agg.Pending += pend
		agg.Retry += ret
		agg.Exhausted += exh
		agg.Blocked += blk
		agg.Skipped += skp

		libKind := "movie"
		if tracks > 0 && tracks >= movies && tracks >= episodes {
			libKind = "track"
		} else if episodes > 0 && episodes >= movies {
			libKind = "episode"
		}

		modes := capModesMap[lib]
		if modes == nil {
			modes = map[string]string{}
		}
		disabledSet := map[string]bool{}
		for _, dc := range disabledMap[lib] {
			disabledSet[dc] = true
		}
		for capName, mode := range modes {
			if mode == "disabled" {
				disabledSet[capName] = true
			}
		}
		if configuredCaps, hasCfg := srvCfg.LibraryCapabilities[lib]; hasCfg {
			allowed := make(map[string]bool, len(configuredCaps))
			for _, c := range configuredCaps {
				allowed[c] = true
			}
			for _, capName := range config.AllCapabilities {
				if !allowed[capName] {
					disabledSet[capName] = true
				}
			}
		}

		libMiss := map[string]int{
			"loudness": mL, "voice": mV, "chapters": mC, "intro": mI,
			"credits": mCr, "ads": mA, "previews": mP, "deep_analysis": mD, "sonic": mS,
		}
		libEligible := map[string]int{
			"loudness":      tot,
			"voice":         movies + episodes,
			"chapters":      movies + episodes,
			"intro":         episodes,
			"credits":       movies + episodes,
			"ads":           movies + episodes,
			"previews":      movies + episodes,
			"deep_analysis": movies + episodes,
			"sonic":         tracks,
		}
		if modes["ads"] == "recorded_only" {
			libEligible["ads"] = dvrCount
			libMiss["ads"] = dvrMissAds
		}

		libCaps := map[string]CapStat{}
		for _, capName := range config.AllCapabilities {
			if disabledSet[capName] || !capAppliesToKind(capName, libKind) {
				continue
			}
			elig := libEligible[capName]
			if elig <= 0 {
				continue
			}
			miss := min(elig, libMiss[capName])
			capEligibleTotal[capName] += elig
			capMissingTotal[capName] += miss
			libCaps[capName] = makeCapStat(elig, miss)
		}
		libPct := 0.0
		if tot > 0 {
			libPct = float64(comp) / float64(tot) * 100.0
		}
		disabledCaps := disabledMap[lib]
		if disabledCaps == nil {
			disabledCaps = []string{}
		}
		libs = append(libs, LibrarySummary{
			Title:        lib,
			Kind:         libKind,
			Total:        tot,
			Complete:     comp,
			Pending:      pend,
			Pct:          libPct,
			DisabledCaps: disabledCaps,
			CapModes:     modes,
			CapStats:     libCaps,
		})
	}
	if agg.Total > 0 {
		agg.Pct = float64(agg.Complete) / float64(agg.Total) * 100.0
	}
	for _, capName := range config.AllCapabilities {
		if elig := capEligibleTotal[capName]; elig > 0 {
			agg.CapStats[capName] = makeCapStat(elig, capMissingTotal[capName])
		}
	}
	return agg, libs, rows.Err()
}

func (d *DB) SetLibraryDisabledCaps(serverID, library string, caps []string) error {
	if caps == nil {
		caps = []string{}
	}
	b, _ := json.Marshal(caps)
	_, err := d.sql.Exec(`INSERT INTO library_prefs(server_id, library, disabled_caps_json) VALUES(?,?,?)
		ON CONFLICT(server_id, library) DO UPDATE SET disabled_caps_json=excluded.disabled_caps_json`, serverID, library, string(b))
	return err
}

func (d *DB) GetDisabledCapsMap(serverID string) map[string][]string {
	out := map[string][]string{}
	rows, err := d.sql.Query(`SELECT library, disabled_caps_json FROM library_prefs WHERE server_id=?`, serverID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var lib, raw string
		if err := rows.Scan(&lib, &raw); err == nil {
			var caps []string
			_ = json.Unmarshal([]byte(raw), &caps)
			if caps == nil {
				caps = []string{}
			}
			out[lib] = caps
		}
	}
	return out
}

func (d *DB) SetLibraryCapModes(serverID, library string, modes map[string]string) error {
	if modes == nil {
		modes = map[string]string{}
	}
	b, _ := json.Marshal(modes)
	_, err := d.sql.Exec(`INSERT INTO library_prefs(server_id, library, cap_modes_json) VALUES(?,?,?)
		ON CONFLICT(server_id, library) DO UPDATE SET cap_modes_json=excluded.cap_modes_json`, serverID, library, string(b))
	return err
}

func (d *DB) GetCapModesMap(serverID string) map[string]map[string]string {
	out := map[string]map[string]string{}
	rows, err := d.sql.Query(`SELECT library, cap_modes_json FROM library_prefs WHERE server_id=?`, serverID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var lib, raw string
		if err := rows.Scan(&lib, &raw); err == nil {
			modes := map[string]string{}
			_ = json.Unmarshal([]byte(raw), &modes)
			out[lib] = modes
		}
	}
	return out
}

// --- Drive Budget Reservations ---

func (d *DB) ReserveParts(serverID string, parts []string, totalBytes int64, windowHours float64) error {
	now := time.Now().Unix()
	_, _ = d.sql.Exec(`DELETE FROM reservations WHERE server_id=? AND expires_at <= ?`, serverID, now)
	if len(parts) == 0 || totalBytes <= 0 {
		return nil
	}
	exp := now + int64(windowHours*3600)
	perPart := totalBytes / int64(len(parts))
	for _, p := range parts {
		_, _ = d.sql.Exec(`INSERT INTO reservations(server_id, part_key, bytes, expires_at) VALUES(?,?,?,?)
			ON CONFLICT(server_id, part_key) DO UPDATE SET bytes=excluded.bytes, expires_at=excluded.expires_at`,
			serverID, p, perPart, exp)
	}
	return nil
}

func (d *DB) UsedBudgetBytes(serverID string) (int64, int, int64) {
	now := time.Now().Unix()
	_, _ = d.sql.Exec(`DELETE FROM reservations WHERE server_id=? AND expires_at <= ?`, serverID, now)
	var used int64
	var cnt int
	var minExp int64
	_ = d.sql.QueryRow(`SELECT COALESCE(SUM(bytes),0), COUNT(*), COALESCE(MIN(expires_at),0) FROM reservations WHERE server_id=?`, serverID).
		Scan(&used, &cnt, &minExp)
	nextRelease := int64(0)
	if minExp > now {
		nextRelease = minExp - now
	}
	return used, cnt, nextRelease
}

// --- Events ---

type Event struct {
	ID       int64  `json:"id"`
	ServerID string `json:"server_id"`
	TS       int64  `json:"ts"`
	Level    string `json:"level"`
	Category string `json:"category"`
	Username string `json:"username"`
	Message  string `json:"message"`
}

func (d *DB) AddEvent(serverID, level, category, message string) {
	d.AddEventUser(serverID, level, category, "", message)
}

func (d *DB) AddEventUser(serverID, level, category, username, message string) {
	args := []any{"server_id", serverID, "category", category}
	if username != "" {
		args = append(args, "user", username)
	}
	switch strings.ToLower(level) {
	case "error":
		slog.Error(message, args...)
	case "warning", "warn":
		slog.Warn(message, args...)
	default:
		slog.Info(message, args...)
	}
	_, _ = d.sql.Exec(`INSERT INTO events(server_id, ts, level, category, username, message) VALUES(?,?,?,?,?,?)`,
		serverID, time.Now().Unix(), level, category, username, message)
}

func (d *DB) ClearEvents(serverID string) error {
	if serverID == "" {
		_, err := d.sql.Exec(`DELETE FROM events`)
		return err
	}
	_, err := d.sql.Exec(`DELETE FROM events WHERE server_id=? OR server_id=''`, serverID)
	return err
}

type ScanSummary struct {
	Completed int `json:"completed"`
	Retries   int `json:"retries"`
	Failed    int `json:"failed"`
	TotalRuns int `json:"total_runs"`
}

func (d *DB) GetScanSummary(serverID string) ScanSummary {
	var s ScanSummary
	_ = d.sql.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN status='complete' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='retry' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='exhausted' THEN 1 ELSE 0 END),0),
		COUNT(*)
		FROM scan_history WHERE server_id=?`, serverID).Scan(&s.Completed, &s.Retries, &s.Failed, &s.TotalRuns)
	return s
}

// PruneRetention enforces configured log/history retention days and max log rows.
func (d *DB) PruneRetention(serverID string, logDays, historyDays, maxLogRows int) (eventsDeleted, historyDeleted int64) {
	now := time.Now().Unix()
	if logDays > 0 {
		cutoff := now - int64(logDays)*86400
		if res, err := d.sql.Exec(`DELETE FROM events WHERE (server_id=? OR server_id='') AND ts < ?`, serverID, cutoff); err == nil && res != nil {
			n, _ := res.RowsAffected()
			eventsDeleted += n
		}
	}
	if maxLogRows > 0 {
		if res, err := d.sql.Exec(`DELETE FROM events WHERE (server_id=? OR server_id='') AND id NOT IN (SELECT id FROM events WHERE (server_id=? OR server_id='') ORDER BY id DESC LIMIT ?)`, serverID, serverID, maxLogRows); err == nil && res != nil {
			n, _ := res.RowsAffected()
			eventsDeleted += n
		}
	}
	if historyDays > 0 {
		cutoff := now - int64(historyDays)*86400
		if res, err := d.sql.Exec(`DELETE FROM scan_history WHERE server_id=? AND finished_at < ?`, serverID, cutoff); err == nil && res != nil {
			n, _ := res.RowsAffected()
			historyDeleted += n
		}
	}
	if eventsDeleted > 0 || historyDeleted > 0 {
		_, _ = d.sql.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
	}
	return eventsDeleted, historyDeleted
}

type EventPage struct {
	Events     []Event `json:"events"`
	Total      int     `json:"total"`
	Page       int     `json:"page"`
	PageSize   int     `json:"page_size"`
	TotalPages int     `json:"total_pages"`
}

func (d *DB) ListEvents(serverID, level, category, search string, limit int) ([]Event, error) {
	page, err := d.QueryEvents(serverID, level, category, search, 0, "ts_desc", 1, limit)
	return page.Events, err
}

func (d *DB) QueryEvents(serverID, level, category, search string, since int64, sort string, page, pageSize int) (EventPage, error) {
	page, pageSize = clampPagination(page, pageSize)
	clauses := []string{"1=1"}
	args := []any{}
	if serverID != "" {
		clauses = append(clauses, "(server_id=? OR server_id='')")
		args = append(args, serverID)
	}
	if level != "" {
		appendInClause(&clauses, &args, "level", strings.Split(level, ","))
	}
	if category != "" {
		appendInClause(&clauses, &args, "category", strings.Split(category, ","))
	}
	if since > 0 {
		clauses = append(clauses, "ts >= ?")
		args = append(args, since)
	}
	if search != "" {
		clauses = append(clauses, "(LOWER(message) LIKE ? OR LOWER(username) LIKE ?)")
		pat := "%" + strings.ToLower(search) + "%"
		args = append(args, pat, pat)
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM events WHERE `+where, args...).Scan(&total); err != nil {
		return EventPage{}, err
	}
	order := "ts DESC, id DESC"
	if sort == "ts_asc" {
		order = "ts ASC, id ASC"
	}
	offset := (page - 1) * pageSize
	rows, err := d.sql.Query(`SELECT id, server_id, ts, level, category, username, message FROM events WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, pageSize, offset)...)
	if err != nil {
		return EventPage{}, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ServerID, &e.TS, &e.Level, &e.Category, &e.Username, &e.Message); err != nil {
			return EventPage{}, err
		}
		out = append(out, e)
	}
	return EventPage{
		Events:     out,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: calcTotalPages(total, pageSize),
	}, rows.Err()
}
