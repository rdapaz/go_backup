package agent

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// AgentConfig manages the local SQLite database for agent state.
type AgentConfig struct {
	db *sql.DB
}

// OpenConfig opens (or creates) the agent configuration database.
func OpenConfig(dbPath string) (*AgentConfig, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open config db: %w", err)
	}

	if err := ensureAgentSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &AgentConfig{db: db}, nil
}

// Close closes the database connection.
func (c *AgentConfig) Close() error {
	return c.db.Close()
}

func ensureAgentSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS pending_reports (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			payload     TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			retry_count INTEGER DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS local_schedules (
			id          INTEGER PRIMARY KEY,
			profile     TEXT NOT NULL,
			src_dir     TEXT NOT NULL,
			dst_dir     TEXT NOT NULL,
			cron_expr   TEXT NOT NULL,
			enabled     INTEGER NOT NULL DEFAULT 1,
			updated_at  TEXT NOT NULL
		);
	`)
	return err
}

// -- Config key-value store ------------------------------------------------------

// Get retrieves a config value, returning defaultVal if not found.
func (c *AgentConfig) Get(key, defaultVal string) string {
	var val string
	err := c.db.QueryRow("SELECT value FROM agent_config WHERE key = ?", key).Scan(&val)
	if err != nil {
		return defaultVal
	}
	return val
}

// Set stores a config value (upsert).
func (c *AgentConfig) Set(key, value string) error {
	_, err := c.db.Exec(
		`INSERT INTO agent_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = ?`,
		key, value, value,
	)
	return err
}

// -- Pending reports -------------------------------------------------------------

// QueueReport stores a backup status report for later delivery.
func (c *AgentConfig) QueueReport(payload []byte) error {
	_, err := c.db.Exec(
		"INSERT INTO pending_reports (payload, created_at) VALUES (?, ?)",
		string(payload), time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// PendingReport represents a queued report.
type PendingReport struct {
	ID      int64
	Payload string
}

// GetPendingReports retrieves all unsent reports.
func (c *AgentConfig) GetPendingReports() ([]PendingReport, error) {
	rows, err := c.db.Query(
		"SELECT id, payload FROM pending_reports ORDER BY id ASC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []PendingReport
	for rows.Next() {
		var r PendingReport
		if err := rows.Scan(&r.ID, &r.Payload); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

// DeletePendingReport removes a report after successful delivery.
func (c *AgentConfig) DeletePendingReport(id int64) error {
	_, err := c.db.Exec("DELETE FROM pending_reports WHERE id = ?", id)
	return err
}

// -- Local schedules -------------------------------------------------------------

// LocalSchedule represents a backup schedule stored locally.
type LocalSchedule struct {
	ID       int
	Profile  string
	SrcDir   string
	DstDir   string
	CronExpr string
	Enabled  bool
}

// GetLocalSchedules retrieves all local schedules.
func (c *AgentConfig) GetLocalSchedules() ([]LocalSchedule, error) {
	rows, err := c.db.Query(
		"SELECT id, profile, src_dir, dst_dir, cron_expr, enabled FROM local_schedules ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []LocalSchedule
	for rows.Next() {
		var s LocalSchedule
		var enabled int
		if err := rows.Scan(&s.ID, &s.Profile, &s.SrcDir, &s.DstDir, &s.CronExpr, &enabled); err != nil {
			return nil, err
		}
		s.Enabled = enabled != 0
		schedules = append(schedules, s)
	}
	return schedules, rows.Err()
}

// GetLocalSchedule retrieves a single schedule by ID.
func (c *AgentConfig) GetLocalSchedule(id int) (*LocalSchedule, error) {
	var s LocalSchedule
	var enabled int
	err := c.db.QueryRow(
		"SELECT id, profile, src_dir, dst_dir, cron_expr, enabled FROM local_schedules WHERE id = ?",
		id,
	).Scan(&s.ID, &s.Profile, &s.SrcDir, &s.DstDir, &s.CronExpr, &enabled)
	if err != nil {
		return nil, err
	}
	s.Enabled = enabled != 0
	return &s, nil
}

// SyncSchedules replaces all local schedules with the given list.
func (c *AgentConfig) SyncSchedules(entries []ScheduleEntry) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Remove schedules not in the new list
	if _, err := tx.Exec("DELETE FROM local_schedules"); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, e := range entries {
		enabled := 0
		if e.Enabled {
			enabled = 1
		}
		_, err := tx.Exec(
			`INSERT INTO local_schedules (id, profile, src_dir, dst_dir, cron_expr, enabled, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.Profile, e.SrcDir, e.DstDir, e.CronExpr, enabled, now,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}
