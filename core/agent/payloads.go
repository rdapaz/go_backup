// Package agent implements the MQTT-based agent for orchestrator communication.
package agent

import (
	"encoding/json"
	"fmt"
	"time"
)

const SchemaVersion = 1

// Envelope is the common MQTT message wrapper.
type Envelope struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// Wrap creates a JSON envelope for the given message type and payload.
func Wrap(msgType string, payload interface{}) ([]byte, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	env := Envelope{
		Version:   SchemaVersion,
		Type:      msgType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload:   p,
	}
	return json.Marshal(env)
}

// Unwrap parses a JSON envelope.
func Unwrap(data []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return &env, nil
}

// -- Registration ----------------------------------------------------------------

type RegistrationRequest struct {
	ClientUUID      string `json:"client_uuid"`
	Hostname        string `json:"hostname"`
	IPAddress       string `json:"ip_address"`
	OS              string `json:"os"`
	GoBackupVersion string `json:"go_backup_version"`
}

type RegistrationResponse struct {
	Approved     bool   `json:"approved"`
	MqttUsername string `json:"mqtt_username"`
	MqttPassword string `json:"mqtt_password"`
	ClientName   string `json:"client_name"`
}

// -- Heartbeat -------------------------------------------------------------------

type Heartbeat struct {
	Status        string `json:"status"`         // idle / backing_up / offline
	UptimeSeconds int64  `json:"uptime_seconds"`
	ActiveBackup  string `json:"active_backup"`  // profile name or empty
}

// -- Backup Command --------------------------------------------------------------

type BackupCommandConfig struct {
	SrcDir       string   `json:"src_dir"`
	DstDir       string   `json:"dst_dir"`
	Profile      string   `json:"profile"`
	Password     string   `json:"password"`
	PasswordHint string   `json:"password_hint"`
	Description  string   `json:"description"`
	Workers      int      `json:"workers"`
	Blocklist    []string `json:"blocklist"`
	ArchivePath  string   `json:"archive_path,omitempty"` // for restore
}

type BackupCommand struct {
	CommandID string              `json:"command_id"`
	Action    string              `json:"action"` // start_backup
	Config    *BackupCommandConfig `json:"config"`
}

// -- Backup Status ---------------------------------------------------------------

type BackupStatus struct {
	CommandID       string `json:"command_id"`
	Status          string `json:"status"`        // success / failure / in_progress / cancelled
	Method          string `json:"method"`        // orchestrator / manual / scheduled
	Profile         string `json:"profile"`
	StartedAt       string `json:"started_at"`
	CompletedAt     string `json:"completed_at"`
	ArchivePath     string `json:"archive_path"`
	ArchivePassword string `json:"archive_password,omitempty"` // sent securely for orchestrator storage
	FileCount       int64  `json:"file_count"`
	ErrorMessage    string `json:"error_message"`
}

// -- Schedule Sync ---------------------------------------------------------------

type ScheduleEntry struct {
	ID           int    `json:"id"`
	Profile      string `json:"profile"`
	SrcDir       string `json:"src_dir"`
	DstDir       string `json:"dst_dir"`
	CronExpr     string `json:"cron_expr"`
	Enabled      bool   `json:"enabled"`
	PasswordHint string `json:"password_hint"`
}

type ScheduleSync struct {
	Schedules []ScheduleEntry `json:"schedules"`
}
