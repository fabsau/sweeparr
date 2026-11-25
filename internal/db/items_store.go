package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fabsau/sweeparr/internal/config"
)

type MediaItem struct {
	ServerID         string   `json:"server_id"`
	Key              string   `json:"key"`
	RatingKey        string   `json:"rating_key"`
	Library          string   `json:"library"`
	Kind             string   `json:"kind"`
	Title            string   `json:"title"`
	GrandparentTitle string   `json:"grandparent_title"`
	ParentTitle      string   `json:"parent_title"`
	Collection       string   `json:"collection"`
	IsDVR            bool     `json:"is_dvr"`
	ParentIndex      int      `json:"parent_index"`
	ItemIndex        int      `json:"item_index"`
	Bytes            int64    `json:"bytes"`
	Parts            []string `json:"parts"`
	Missing          []string `json:"missing"`
	Status           string   `json:"status"`
	Attempts         int      `json:"attempts"`
	PriorityPos      int      `json:"priority_pos"`
	Queued           bool     `json:"queued"`
	Skipped          bool     `json:"skipped"`
	NextRetryAt      int64    `json:"next_retry_at"`
	LastError        string   `json:"last_error"`
	QueuedBy         string   `json:"queued_by"`
	QueuedReason     string   `json:"queued_reason"`
	BlocklistedBy    string   `json:"blocklisted_by"`
	BlocklistedAt    int64    `json:"blocklisted_at"`
	AddedAt          int64    `json:"added_at"`
	UpdatedAt        int64    `json:"updated_at"`
}

const itemSelectCols = `server_id, item_key, rating_key, library, kind, title, grandparent_title, parent_title, collection, is_dvr,
	parent_index, item_index, bytes, parts_json, missing_json, status, attempts, priority_pos, skipped, next_retry_at, last_error, queued_by, queued_reason, blocklisted_by, blocklisted_at, added_at, updated_at`

func scanMediaItem(row scannerRow) (MediaItem, error) {
	var it MediaItem
	var partsRaw, missRaw string
	var skippedInt, isDVRInt int
	if err := row.Scan(
		&it.ServerID, &it.Key, &it.RatingKey, &it.Library, &it.Kind, &it.Title, &it.GrandparentTitle, &it.ParentTitle, &it.Collection, &isDVRInt,
		&it.ParentIndex, &it.ItemIndex, &it.Bytes, &partsRaw, &missRaw, &it.Status, &it.Attempts, &it.PriorityPos, &skippedInt, &it.NextRetryAt, &it.LastError, &it.QueuedBy, &it.QueuedReason, &it.BlocklistedBy, &it.BlocklistedAt, &it.AddedAt, &it.UpdatedAt,
	); err != nil {
		return it, err
	}
	_ = json.Unmarshal([]byte(partsRaw), &it.Parts)
	_ = json.Unmarshal([]byte(missRaw), &it.Missing)
	it.IsDVR = isDVRInt != 0
	it.Queued = it.PriorityPos > 0
	it.Skipped = skippedInt != 0
	return it, nil
}

func (d *DB) queryMediaItems(query string, args ...any) ([]MediaItem, error) {
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MediaItem{}
	for rows.Next() {
		it, err := scanMediaItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (d *DB) queryItemKeys(query string, args ...any) ([]string, error) {
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
		}
	}
	return keys, rows.Err()
}

func missingCapFlags(missing []string) (string, [9]int) {
	missB, _ := json.Marshal(missing)
	mset := make(map[string]int, len(missing))
	for _, m := range missing {
		mset[m] = 1
	}
	return string(missB), [9]int{
		mset["loudness"], mset["voice"], mset["chapters"],
		mset["intro"], mset["credits"], mset["ads"],
		mset["previews"], mset["deep_analysis"], mset["sonic"],
	}
}

func (d *DB) UpsertItems(serverID string, items []MediaItem) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO items(
			server_id, item_key, rating_key, library, kind, title, grandparent_title, parent_title, collection, is_dvr,
			parent_index, item_index, bytes, parts_json, missing_json,
			miss_loudness, miss_voice, miss_chapters, miss_intro, miss_credits, miss_ads, miss_previews, miss_deep_analysis, miss_sonic,
			status, added_at, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(server_id, item_key) DO UPDATE SET
			rating_key=excluded.rating_key, library=excluded.library, kind=excluded.kind, title=excluded.title,
			grandparent_title=excluded.grandparent_title, parent_title=excluded.parent_title, collection=excluded.collection, is_dvr=excluded.is_dvr,
			parent_index=excluded.parent_index, item_index=excluded.item_index,
			bytes=excluded.bytes, parts_json=excluded.parts_json, missing_json=excluded.missing_json,
			miss_loudness=excluded.miss_loudness, miss_voice=excluded.miss_voice, miss_chapters=excluded.miss_chapters,
			miss_intro=excluded.miss_intro, miss_credits=excluded.miss_credits, miss_ads=excluded.miss_ads,
			miss_previews=excluded.miss_previews, miss_deep_analysis=excluded.miss_deep_analysis, miss_sonic=excluded.miss_sonic,
			status=CASE WHEN items.skipped=1 THEN 'skipped'
			            WHEN excluded.status='complete' THEN 'complete'
			            WHEN items.status IN ('exhausted','blocked','retry') THEN items.status
			            ELSE 'pending' END,
			attempts=CASE WHEN excluded.status='complete' THEN 0 ELSE items.attempts END,
			next_retry_at=CASE WHEN excluded.status='complete' THEN 0 ELSE items.next_retry_at END,
			last_error=CASE WHEN excluded.status='complete' THEN '' ELSE items.last_error END,
			updated_at=excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, it := range items {
		partsB, _ := json.Marshal(it.Parts)
		missJSON, f := missingCapFlags(it.Missing)
		if _, err := stmt.Exec(
			serverID, it.Key, it.RatingKey, it.Library, it.Kind, it.Title, it.GrandparentTitle, it.ParentTitle, it.Collection, boolInt(it.IsDVR),
			it.ParentIndex, it.ItemIndex, it.Bytes, string(partsB), missJSON,
			f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7], f[8],
			it.Status, it.AddedAt, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ItemQuery struct {
	ServerID string
	Search   string
	Library  string
	Status   string
	Cap      string
	Sort     string
	Page     int
	PageSize int
	Enabled  []string
	Priority []string
}

type ItemPage struct {
	Items      []MediaItem `json:"items"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
}

func (d *DB) QueryItems(q ItemQuery) (ItemPage, error) {
	q.Page, q.PageSize = clampPagination(q.Page, q.PageSize)
	where, args := buildItemWhere(q)
	var total int
	if err := d.sql.QueryRow("SELECT COUNT(*) FROM items WHERE "+where, args...).Scan(&total); err != nil {
		return ItemPage{}, err
	}
	orderBy := buildItemOrder(q.Sort)
	offset := (q.Page - 1) * q.PageSize
	query := fmt.Sprintf(`SELECT %s FROM items WHERE %s ORDER BY %s LIMIT ? OFFSET ?`, itemSelectCols, where, orderBy)
	items, err := d.queryMediaItems(query, append(args, q.PageSize, offset)...)
	if err != nil {
		return ItemPage{}, err
	}
	return ItemPage{
		Items:      items,
		Total:      total,
		Page:       q.Page,
		PageSize:   q.PageSize,
		TotalPages: calcTotalPages(total, q.PageSize),
	}, nil
}

func appendTokenSearch(clauses *[]string, args *[]any, rawSearch, concatExpr string) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(rawSearch)))
	if len(tokens) > 10 {
		tokens = tokens[:10]
	}
	for _, tok := range tokens {
		*clauses = append(*clauses, "LOWER("+concatExpr+") LIKE ?")
		*args = append(*args, "%"+tok+"%")
	}
}

func buildItemWhere(q ItemQuery) (string, []any) {
	clauses := []string{"server_id=?"}
	args := []any{q.ServerID}
	if q.Search != "" {
		appendTokenSearch(&clauses, &args, q.Search, "grandparent_title || ' ' || parent_title || ' ' || title || ' ' || collection || ' ' || library")
	}
	if q.Library != "" {
		appendInClause(&clauses, &args, "library", strings.Split(q.Library, ","))
	}
	if q.Status != "" {
		appendInClause(&clauses, &args, "status", strings.Split(q.Status, ","))
	}
	if q.Cap != "" {
		capCol := map[string]string{
			"loudness": "miss_loudness", "voice": "miss_voice", "chapters": "miss_chapters",
			"intro": "miss_intro", "credits": "miss_credits", "ads": "miss_ads",
			"previews": "miss_previews", "deep_analysis": "miss_deep_analysis", "sonic": "miss_sonic",
		}
		var capClauses []string
		for _, c := range strings.Split(q.Cap, ",") {
			if col, ok := capCol[strings.TrimSpace(c)]; ok {
				capClauses = append(capClauses, col+"=1")
			}
		}
		if len(capClauses) > 0 {
			clauses = append(clauses, "("+strings.Join(capClauses, " OR ")+")")
		}
	}
	return strings.Join(clauses, " AND "), args
}

func buildItemOrder(sort string) string {
	switch sort {
	case "alphabetical":
		return "LOWER(title) ASC"
	case "alphabetical_desc":
		return "LOWER(title) DESC"
	case "library_asc":
		return "LOWER(library) ASC, LOWER(title) ASC"
	case "library_desc":
		return "LOWER(library) DESC, LOWER(title) ASC"
	case "status_asc":
		return "status ASC, LOWER(title) ASC"
	case "status_desc":
		return "status DESC, LOWER(title) ASC"
	case "newest_first":
		return "added_at DESC, LOWER(title) ASC"
	case "oldest_first":
		return "added_at ASC, LOWER(title) ASC"
	case "largest_first":
		return "bytes DESC, LOWER(title) ASC"
	case "smallest_first":
		return "bytes ASC, LOWER(title) ASC"
	case "fewest_attempts_first":
		return "attempts ASC, added_at DESC"
	default:
		return "CASE WHEN priority_pos > 0 THEN 0 ELSE 1 END ASC, priority_pos ASC, LOWER(grandparent_title) ASC, parent_index ASC, item_index ASC, LOWER(title) ASC"
	}
}

func (d *DB) EnqueueKeys(serverID string, keys []string, front bool) (int, error) {
	return d.EnqueueKeysBy(serverID, keys, front, "system", "auto")
}

func (d *DB) EnqueueKeysBy(serverID string, keys []string, front bool, actor, reason string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	if strings.TrimSpace(actor) == "" {
		actor = "system"
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var maxPos int
	_ = tx.QueryRow(`SELECT COALESCE(MAX(priority_pos), 0) FROM items WHERE server_id=?`, serverID).Scan(&maxPos)
	pos := maxPos
	if front {
		pos = 0
		if _, err := tx.Exec(`UPDATE items SET priority_pos = priority_pos + ? WHERE server_id=? AND priority_pos > 0`, len(keys), serverID); err != nil {
			return 0, err
		}
	}
	updated := 0
	for _, k := range keys {
		pos++
		res, err := tx.Exec(`UPDATE items SET priority_pos=?, skipped=0, queued_by=?, queued_reason=?, status=CASE WHEN status IN ('complete','skipped') THEN 'pending' ELSE status END WHERE server_id=? AND item_key=?`, pos, actor, reason, serverID, k)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func (d *DB) EnqueueGroup(serverID, library, grandparentTitle, parentTitle string) (int, error) {
	clauses := []string{"server_id=?", "library=?", "skipped=0", "status != 'complete'"}
	args := []any{serverID, library}
	if parentTitle == "Collection" && grandparentTitle != "" {
		clauses = append(clauses, "kind='movie'", "collection=?")
		args = append(args, grandparentTitle)
	} else {
		if grandparentTitle != "" {
			clauses = append(clauses, "(grandparent_title=? OR (grandparent_title='' AND title=?))")
			args = append(args, grandparentTitle, grandparentTitle)
		}
		if parentTitle != "" {
			clauses = append(clauses, "parent_title=?")
			args = append(args, parentTitle)
		}
	}
	keys, err := d.queryItemKeys(`SELECT item_key FROM items WHERE `+strings.Join(clauses, " AND ")+` ORDER BY parent_index ASC, item_index ASC, LOWER(title) ASC`, args...)
	if err != nil {
		return 0, err
	}
	return d.EnqueueKeys(serverID, keys, true)
}

func (d *DB) EnqueueLibrary(serverID, library string, enabledLibs []string) (int, error) {
	clauses := []string{"server_id=?", "skipped=0", "status IN ('pending','retry')"}
	args := []any{serverID}
	if library != "" {
		clauses = append(clauses, "library=?")
		args = append(args, library)
	} else {
		appendInClause(&clauses, &args, "library", enabledLibs)
	}
	keys, err := d.queryItemKeys(`SELECT item_key FROM items WHERE `+strings.Join(clauses, " AND ")+` ORDER BY LOWER(grandparent_title) ASC, parent_index ASC, item_index ASC, LOWER(title) ASC LIMIT 5000`, args...)
	if err != nil {
		return 0, err
	}
	return d.EnqueueKeys(serverID, keys, true)
}

type GroupRow struct {
	Library    string `json:"library"`
	Kind       string `json:"kind"`
	GroupTitle string `json:"group_title"`
	SubTitle   string `json:"sub_title"`
	Total      int    `json:"total"`
	Complete   int    `json:"complete"`
	Remaining  int    `json:"remaining"`
	Issues     int    `json:"issues"`
	Bytes      int64  `json:"bytes"`
}

type GroupPage struct {
	Groups     []GroupRow `json:"groups"`
	Total      int        `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
	TotalPages int        `json:"total_pages"`
}

func (d *DB) QueryGroups(q ItemQuery) (GroupPage, error) {
	q.Page, q.PageSize = clampPagination(q.Page, q.PageSize)
	where, args := buildItemWhere(q)
	groupExpr := `CASE WHEN grandparent_title != '' THEN grandparent_title WHEN kind='movie' AND collection != '' THEN collection ELSE title END`
	subExpr := `CASE WHEN kind='movie' AND collection != '' THEN 'Collection' ELSE parent_title END`
	countSQL := fmt.Sprintf(`SELECT COUNT(*) FROM (SELECT 1 FROM items WHERE %s GROUP BY library, %s, %s)`, where, groupExpr, subExpr)
	var total int
	if err := d.sql.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return GroupPage{}, err
	}
	offset := (q.Page - 1) * q.PageSize
	var groupOrder string
	switch q.Sort {
	case "alphabetical":
		groupOrder = "LOWER(gtitle) ASC, MIN(parent_index) ASC"
	case "alphabetical_desc":
		groupOrder = "LOWER(gtitle) DESC, MIN(parent_index) ASC"
	case "library_asc":
		groupOrder = "LOWER(library) ASC, LOWER(gtitle) ASC, MIN(parent_index) ASC"
	case "library_desc":
		groupOrder = "LOWER(library) DESC, LOWER(gtitle) ASC, MIN(parent_index) ASC"
	case "sub_asc":
		groupOrder = "LOWER(stitle) ASC, LOWER(gtitle) ASC"
	case "sub_desc":
		groupOrder = "LOWER(stitle) DESC, LOWER(gtitle) ASC"
	case "largest_first":
		groupOrder = "COALESCE(SUM(bytes), 0) DESC, LOWER(gtitle) ASC"
	case "smallest_first":
		groupOrder = "COALESCE(SUM(bytes), 0) ASC, LOWER(gtitle) ASC"
	case "newest_first":
		groupOrder = "MAX(added_at) DESC, LOWER(gtitle) ASC"
	case "oldest_first":
		groupOrder = "MIN(added_at) ASC, LOWER(gtitle) ASC"
	default:
		groupOrder = "SUM(CASE WHEN status != 'complete' AND skipped=0 THEN 1 ELSE 0 END) DESC, LOWER(gtitle) ASC, MIN(parent_index) ASC"
	}
	query := fmt.Sprintf(`
		SELECT library, MAX(kind), %s AS gtitle, %s AS stitle,
		       COUNT(*),
		       SUM(CASE WHEN status='complete' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status != 'complete' AND skipped=0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status IN ('exhausted','retry','blocked') THEN 1 ELSE 0 END),
		       COALESCE(SUM(bytes), 0)
		FROM items WHERE %s
		GROUP BY library, gtitle, stitle
		ORDER BY %s
		LIMIT ? OFFSET ?`, groupExpr, subExpr, where, groupOrder)
	rows, err := d.sql.Query(query, append(args, q.PageSize, offset)...)
	if err != nil {
		return GroupPage{}, err
	}
	defer rows.Close()
	groups := []GroupRow{}
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(&g.Library, &g.Kind, &g.GroupTitle, &g.SubTitle, &g.Total, &g.Complete, &g.Remaining, &g.Issues, &g.Bytes); err != nil {
			return GroupPage{}, err
		}
		groups = append(groups, g)
	}
	return GroupPage{
		Groups:     groups,
		Total:      total,
		Page:       q.Page,
		PageSize:   q.PageSize,
		TotalPages: calcTotalPages(total, q.PageSize),
	}, rows.Err()
}

func (d *DB) ExportItems(q ItemQuery) ([]MediaItem, error) {
	where, args := buildItemWhere(q)
	orderBy := buildItemOrder(q.Sort)
	return d.queryMediaItems(fmt.Sprintf(`SELECT %s FROM items WHERE %s ORDER BY %s LIMIT 10000`, itemSelectCols, where, orderBy), args...)
}

func (d *DB) ReorderPriority(serverID string, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	updated := 0
	for i, k := range keys {
		res, err := tx.Exec(`UPDATE items SET priority_pos=? WHERE server_id=? AND item_key=? AND priority_pos > 0`, i+1, serverID, k)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func (d *DB) RemoveFromPriority(serverID, key string) (int64, error) {
	res, err := d.sql.Exec(`UPDATE items SET priority_pos=0 WHERE server_id=? AND item_key=? AND priority_pos > 0`, serverID, key)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *DB) ClearPriority(serverID string) (int64, error) {
	res, err := d.sql.Exec(`UPDATE items SET priority_pos=0 WHERE server_id=? AND priority_pos > 0`, serverID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *DB) SetSkip(serverID string, keys []string, skip bool) (int64, error) {
	return d.SetSkipBy(serverID, keys, skip, "system")
}

func (d *DB) SetSkipBy(serverID string, keys []string, skip bool, actor string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	if strings.TrimSpace(actor) == "" {
		actor = "system"
	}
	status := "pending"
	blBy := ""
	blAt := int64(0)
	if skip {
		status = "skipped"
		blBy = actor
		blAt = time.Now().Unix()
	}
	clauses := []string{"server_id=?"}
	args := []any{boolInt(skip), status, boolInt(skip), blBy, blAt, time.Now().Unix(), serverID}
	appendInClause(&clauses, &args, "item_key", keys)
	res, err := d.sql.Exec(`UPDATE items SET skipped=?, status=?, priority_pos=CASE WHEN ?=1 THEN 0 ELSE priority_pos END, blocklisted_by=?, blocklisted_at=?, updated_at=? WHERE `+strings.Join(clauses, " AND "), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *DB) ResetRetries(serverID string, keys []string) (int64, error) {
	if len(keys) == 0 {
		res, err := d.sql.Exec(`UPDATE items SET status='pending', attempts=0, next_retry_at=0, last_error='' WHERE server_id=? AND status IN ('exhausted','retry','blocked')`, serverID)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	clauses := []string{"server_id=?"}
	args := []any{serverID}
	appendInClause(&clauses, &args, "item_key", keys)
	res, err := d.sql.Exec(`UPDATE items SET status='pending', attempts=0, next_retry_at=0, last_error='' WHERE `+strings.Join(clauses, " AND "), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *DB) PickNextItem(serverID string, cfg config.ScannerConfig) (*MediaItem, error) {
	now := time.Now().Unix()
	clauses := []string{"server_id=?", "skipped=0", "status IN ('pending','retry')", "next_retry_at <= ?"}
	args := []any{serverID, now}
	appendInClause(&clauses, &args, "library", cfg.EnabledLibraries)
	libOrder := ""
	if len(cfg.LibraryPriority) > 0 {
		var cases []string
		for i, l := range cfg.LibraryPriority {
			cases = append(cases, fmt.Sprintf("WHEN ? THEN %d", i))
			args = append(args, l)
		}
		libOrder = "CASE library " + strings.Join(cases, " ") + " ELSE 999 END ASC, "
	}
	stratOrder := buildItemOrder(cfg.OrderStrategy)
	query := fmt.Sprintf(`
		SELECT %s FROM items
		WHERE %s
		ORDER BY CASE WHEN priority_pos > 0 THEN 0 ELSE 1 END ASC, priority_pos ASC, %s%s
		LIMIT 1`, itemSelectCols, strings.Join(clauses, " AND "), libOrder, stratOrder)
	it, err := scanMediaItem(d.sql.QueryRow(query, args...))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func (d *DB) MarkItemResult(serverID, itemKey, status string, attempts int, nextRetryAt int64, lastError string, missing []string) error {
	missJSON, f := missingCapFlags(missing)
	_, err := d.sql.Exec(`
		UPDATE items SET status=?, attempts=?, priority_pos=0, next_retry_at=?, last_error=?, missing_json=?,
			miss_loudness=?, miss_voice=?, miss_chapters=?, miss_intro=?, miss_credits=?, miss_ads=?, miss_previews=?, miss_deep_analysis=?, miss_sonic=?,
			updated_at=?
		WHERE server_id=? AND item_key=?`,
		status, attempts, nextRetryAt, lastError, missJSON,
		f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7], f[8],
		time.Now().Unix(), serverID, itemKey,
	)
	return err
}

func (d *DB) MarkItemActive(serverID, itemKey string) error {
	_, err := d.sql.Exec(`UPDATE items SET status='active', updated_at=? WHERE server_id=? AND item_key=?`, time.Now().Unix(), serverID, itemKey)
	return err
}

func (d *DB) ResetStaleActiveItems(serverID string) {
	_, _ = d.sql.Exec(`UPDATE items SET status='pending' WHERE server_id=? AND status='active'`, serverID)
}

func (d *DB) QueryMatchingKeys(q ItemQuery, limit int) ([]string, error) {
	if limit < 1 || limit > 25000 {
		limit = 25000
	}
	where, args := buildItemWhere(q)
	return d.queryItemKeys(fmt.Sprintf(`SELECT item_key FROM items WHERE %s LIMIT ?`, where), append(args, limit)...)
}

type HistoryRow struct {
	ID           int64    `json:"id"`
	ServerID     string   `json:"server_id"`
	ItemKey      string   `json:"item_key"`
	Title        string   `json:"title"`
	Library      string   `json:"library"`
	Kind         string   `json:"kind"`
	Status       string   `json:"status"`
	Detail       string   `json:"detail"`
	AddedCaps    []string `json:"added_caps"`
	MissingCaps  []string `json:"missing_caps"`
	Bytes        int64    `json:"bytes"`
	QueuedBy     string   `json:"queued_by"`
	QueuedReason string   `json:"queued_reason"`
	DurationMs   int64    `json:"duration_ms"`
	FinishedAt   int64    `json:"finished_at"`
}

func (d *DB) AddScanHistory(h HistoryRow) {
	if h.AddedCaps == nil {
		h.AddedCaps = []string{}
	}
	if h.MissingCaps == nil {
		h.MissingCaps = []string{}
	}
	addJSON, _ := json.Marshal(h.AddedCaps)
	missJSON, _ := json.Marshal(h.MissingCaps)
	_, _ = d.sql.Exec(
		`INSERT INTO scan_history(server_id, item_key, title, library, kind, status, detail, added_caps_json, missing_caps_json, bytes, queued_by, queued_reason, duration_ms, finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ServerID, h.ItemKey, h.Title, h.Library, h.Kind, h.Status, h.Detail, string(addJSON), string(missJSON), h.Bytes, h.QueuedBy, h.QueuedReason, h.DurationMs, h.FinishedAt,
	)
}

func scanHistoryRow(rows *sql.Rows) (HistoryRow, error) {
	var h HistoryRow
	var addRaw, missRaw string
	if err := rows.Scan(&h.ID, &h.ServerID, &h.ItemKey, &h.Title, &h.Library, &h.Kind, &h.Status, &h.Detail, &addRaw, &missRaw, &h.Bytes, &h.QueuedBy, &h.QueuedReason, &h.DurationMs, &h.FinishedAt); err != nil {
		return h, err
	}
	_ = json.Unmarshal([]byte(addRaw), &h.AddedCaps)
	_ = json.Unmarshal([]byte(missRaw), &h.MissingCaps)
	if h.AddedCaps == nil {
		h.AddedCaps = []string{}
	}
	if h.MissingCaps == nil {
		h.MissingCaps = []string{}
	}
	return h, nil
}

func (d *DB) SetItemHoldUntil(serverID, itemKey string, holdUntil int64) error {
	_, err := d.sql.Exec(`UPDATE items SET next_retry_at=?, updated_at=? WHERE server_id=? AND item_key=?`,
		holdUntil, time.Now().Unix(), serverID, itemKey)
	return err
}

type HistoryPage struct {
	Items      []HistoryRow `json:"items"`
	Total      int          `json:"total"`
	Page       int          `json:"page"`
	PageSize   int          `json:"page_size"`
	TotalPages int          `json:"total_pages"`
}

func (d *DB) QueryScanHistoryPage(serverID, search, library, status, infoFilter string, since int64, sort string, page, pageSize int) (HistoryPage, error) {
	page, pageSize = clampPagination(page, pageSize)
	clauses := []string{"server_id=?"}
	args := []any{serverID}
	if s := strings.TrimSpace(search); s != "" {
		appendTokenSearch(&clauses, &args, s, "title || ' ' || library || ' ' || detail || ' ' || queued_by")
	}
	if library != "" {
		appendInClause(&clauses, &args, "library", strings.Split(library, ","))
	}
	if status != "" {
		appendInClause(&clauses, &args, "status", strings.Split(status, ","))
	}
	if since > 0 {
		clauses = append(clauses, "finished_at >= ?")
		args = append(args, since)
	}
	if infoFilter != "" {
		var infoClauses []string
		for _, tok := range strings.Split(infoFilter, ",") {
			switch strings.TrimSpace(strings.ToLower(tok)) {
			case "added":
				infoClauses = append(infoClauses, "(added_caps_json != '[]' OR detail LIKE '%added:%' OR (status='complete' AND detail=''))")
			case "missing":
				infoClauses = append(infoClauses, "(missing_caps_json != '[]' OR detail LIKE '%missing:%' OR detail LIKE '%still missing%')")
			case "error":
				infoClauses = append(infoClauses, "(detail LIKE '%error:%' OR (detail != '' AND detail NOT LIKE '%added:%' AND detail NOT LIKE '%missing:%' AND detail NOT LIKE '%still missing%' AND detail != 'Dry-run'))")
			}
		}
		if len(infoClauses) > 0 {
			clauses = append(clauses, "("+strings.Join(infoClauses, " OR ")+")")
		}
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM scan_history WHERE `+where, args...).Scan(&total); err != nil {
		return HistoryPage{}, err
	}
	order := "finished_at DESC, id DESC"
	switch sort {
	case "title_asc":
		order = "LOWER(title) ASC, id DESC"
	case "title_desc":
		order = "LOWER(title) DESC, id DESC"
	case "library_asc":
		order = "LOWER(library) ASC, id DESC"
	case "library_desc":
		order = "LOWER(library) DESC, id DESC"
	case "status_asc":
		order = "status ASC, id DESC"
	case "status_desc":
		order = "status DESC, id DESC"
	case "duration_asc":
		order = "duration_ms ASC, id DESC"
	case "duration_desc":
		order = "duration_ms DESC, id DESC"
	case "size_asc":
		order = "bytes ASC, id DESC"
	case "size_desc":
		order = "bytes DESC, id DESC"
	case "finished_asc":
		order = "finished_at ASC, id ASC"
	case "finished_desc":
		order = "finished_at DESC, id DESC"
	}
	offset := (page - 1) * pageSize
	rows, err := d.sql.Query(`SELECT id, server_id, item_key, title, library, kind, status, detail, added_caps_json, missing_caps_json, bytes, queued_by, queued_reason, duration_ms, finished_at FROM scan_history WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, pageSize, offset)...)
	if err != nil {
		return HistoryPage{}, err
	}
	defer rows.Close()
	out := []HistoryRow{}
	for rows.Next() {
		h, err := scanHistoryRow(rows)
		if err != nil {
			return HistoryPage{}, err
		}
		out = append(out, h)
	}
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	return HistoryPage{Items: out, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages}, rows.Err()
}

func (d *DB) ClearScanHistory(serverID string) error {
	_, err := d.sql.Exec(`DELETE FROM scan_history WHERE server_id=?`, serverID)
	return err
}

func (d *DB) QueryScanHistory(serverID string, limit int) ([]HistoryRow, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := d.sql.Query(`SELECT id, server_id, item_key, title, library, kind, status, detail, added_caps_json, missing_caps_json, bytes, queued_by, queued_reason, duration_ms, finished_at FROM scan_history WHERE server_id=? ORDER BY id DESC LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryRow{}
	for rows.Next() {
		h, err := scanHistoryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (d *DB) QueryBlocklistPage(serverID, search, library string, since int64, sort string, page, pageSize int) (ItemPage, error) {
	page, pageSize = clampPagination(page, pageSize)
	clauses := []string{"server_id=?", "skipped=1"}
	args := []any{serverID}
	if s := strings.TrimSpace(search); s != "" {
		appendTokenSearch(&clauses, &args, s, "grandparent_title || ' ' || parent_title || ' ' || title || ' ' || library || ' ' || blocklisted_by || ' ' || last_error")
	}
	if library != "" {
		appendInClause(&clauses, &args, "library", strings.Split(library, ","))
	}
	if since > 0 {
		clauses = append(clauses, "COALESCE(NULLIF(blocklisted_at,0), updated_at) >= ?")
		args = append(args, since)
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM items WHERE `+where, args...).Scan(&total); err != nil {
		return ItemPage{}, err
	}
	order := "COALESCE(NULLIF(blocklisted_at,0), updated_at) DESC, LOWER(title) ASC"
	switch sort {
	case "title_asc":
		order = "LOWER(title) ASC"
	case "title_desc":
		order = "LOWER(title) DESC"
	case "library_asc":
		order = "LOWER(library) ASC, LOWER(title) ASC"
	case "library_desc":
		order = "LOWER(library) DESC, LOWER(title) ASC"
	case "added_asc":
		order = "COALESCE(NULLIF(blocklisted_at,0), updated_at) ASC, LOWER(title) ASC"
	case "added_desc":
		order = "COALESCE(NULLIF(blocklisted_at,0), updated_at) DESC, LOWER(title) ASC"
	}
	offset := (page - 1) * pageSize
	items, err := d.queryMediaItems(`SELECT `+itemSelectCols+` FROM items WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, pageSize, offset)...)
	if err != nil {
		return ItemPage{}, err
	}
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	return ItemPage{Items: items, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages}, nil
}

func (d *DB) ClearBlocklist(serverID string) (int64, error) {
	res, err := d.sql.Exec(`UPDATE items SET skipped=0, status='pending' WHERE server_id=? AND skipped=1`, serverID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetItem fetches a single MediaItem by serverID and key.
func (d *DB) GetItem(serverID, key string) (*MediaItem, error) {
	rows, err := d.queryMediaItems(`SELECT `+itemSelectCols+` FROM items WHERE server_id=? AND item_key=? LIMIT 1`, serverID, key)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

var (
	rePlexEpisodeSub = regexp.MustCompile(`^(.+?)\s+S(\d+)\s+E(\d+)$`)
	rePlexSeasonSub  = regexp.MustCompile(`^(.+?)\s+S(\d+)$`)
)

// FindItemByActivitySubtitle matches Plex /activities subtitle strings such as
// "2 Broke Girls S01 E01", "2 Broke Girls S01", or "Inception" to a catalog MediaItem.
func (d *DB) FindItemByActivitySubtitle(serverID, subtitle string) *MediaItem {
	s := strings.TrimSpace(subtitle)
	if s == "" {
		return nil
	}
	if m := rePlexEpisodeSub.FindStringSubmatch(s); len(m) == 4 {
		show := strings.TrimSpace(m[1])
		season, _ := strconv.Atoi(m[2])
		ep, _ := strconv.Atoi(m[3])
		rows, err := d.queryMediaItems(`SELECT `+itemSelectCols+` FROM items WHERE server_id=? AND LOWER(grandparent_title)=LOWER(?) AND parent_index=? AND item_index=? LIMIT 1`, serverID, show, season, ep)
		if err == nil && len(rows) > 0 {
			return &rows[0]
		}
	}
	if m := rePlexSeasonSub.FindStringSubmatch(s); len(m) == 3 {
		show := strings.TrimSpace(m[1])
		season, _ := strconv.Atoi(m[2])
		rows, err := d.queryMediaItems(`SELECT `+itemSelectCols+` FROM items WHERE server_id=? AND LOWER(grandparent_title)=LOWER(?) AND parent_index=? ORDER BY CASE WHEN status='complete' THEN 1 ELSE 0 END, item_index ASC LIMIT 1`, serverID, show, season)
		if err == nil && len(rows) > 0 {
			return &rows[0]
		}
	}
	rows, err := d.queryMediaItems(`SELECT `+itemSelectCols+` FROM items WHERE server_id=? AND (LOWER(title)=LOWER(?) OR LOWER(grandparent_title || ' - ' || title)=LOWER(?)) LIMIT 1`, serverID, s, s)
	if err == nil && len(rows) > 0 {
		return &rows[0]
	}
	return nil
}

func (d *DB) SyncExternalActiveTitles(serverID string, subtitles []string) bool {
	resClear, errClear := d.sql.Exec(`UPDATE items SET status='pending' WHERE server_id=? AND status='active'`, serverID)
	var cleared int64
	if errClear == nil && resClear != nil {
		cleared, _ = resClear.RowsAffected()
	}
	marked := int64(0)
	for _, sub := range subtitles {
		it := d.FindItemByActivitySubtitle(serverID, sub)
		if it == nil || it.Status == "complete" {
			continue
		}
		res, err := d.sql.Exec(`UPDATE items SET status='active' WHERE server_id=? AND item_key=? AND status IN ('pending','retry')`, serverID, it.Key)
		if err == nil && res != nil {
			n, _ := res.RowsAffected()
			marked += n
		}
	}
	return cleared > 0 || marked > 0
}

func (d *DB) QueryBlocklist(serverID string, limit int) ([]MediaItem, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	return d.queryMediaItems(`SELECT `+itemSelectCols+` FROM items WHERE server_id=? AND skipped=1 ORDER BY updated_at DESC, LOWER(title) ASC LIMIT ?`, serverID, limit)
}

func (d *DB) GetQueuePreview(serverID string, limit int) ([]MediaItem, []MediaItem, int, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	var prioCount int
	_ = d.sql.QueryRow(`SELECT COUNT(*) FROM items WHERE server_id=? AND priority_pos > 0 AND skipped=0`, serverID).Scan(&prioCount)

	prio, err := d.queryMediaItems(`SELECT `+itemSelectCols+`
		FROM items WHERE server_id=? AND priority_pos > 0 AND skipped=0 ORDER BY priority_pos ASC LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, nil, 0, err
	}
	auto, err := d.queryMediaItems(`SELECT `+itemSelectCols+`
		FROM items WHERE server_id=? AND priority_pos = 0 AND skipped=0 AND status IN ('pending','retry') ORDER BY added_at ASC LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, nil, 0, err
	}
	return prio, auto, prioCount, nil
}
