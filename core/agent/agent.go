package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"mybackup/core"
)

// Agent manages the MQTT connection and backup execution.
type Agent struct {
	config   *AgentConfig
	mqtt     *MqttClient
	ctx      context.Context
	cancelFn context.CancelFunc
	startAt  time.Time
}

// NewAgent creates a new agent from the local config database.
func NewAgent(configDBPath string) (*Agent, error) {
	cfg, err := OpenConfig(configDBPath)
	if err != nil {
		return nil, fmt.Errorf("open agent config: %w", err)
	}

	clientUUID := cfg.Get("client_uuid", "")
	if clientUUID == "" {
		cfg.Close()
		return nil, fmt.Errorf("agent not registered (no client_uuid in config)")
	}

	brokerAddr := cfg.Get("broker_address", "localhost")
	brokerPort := 1883
	fmt.Sscanf(cfg.Get("broker_port", "1883"), "%d", &brokerPort)
	mqttUser := cfg.Get("mqtt_username", "")
	mqttPass := cfg.Get("mqtt_password", "")

	mqttClient := NewMqttClient(brokerAddr, brokerPort, mqttUser, mqttPass, clientUUID)

	return &Agent{
		config:  cfg,
		mqtt:    mqttClient,
		startAt: time.Now(),
	}, nil
}

// Run starts the agent main loop: connects to MQTT, sends heartbeats,
// listens for commands, and drains any pending reports.
func (a *Agent) Run() error {
	a.ctx, a.cancelFn = context.WithCancel(context.Background())

	// Set up handlers
	a.mqtt.SetCommandHandler(a.handleBackupCommand)
	a.mqtt.SetScheduleHandler(a.handleScheduleSync)

	// Connect to broker
	if err := a.mqtt.Connect(); err != nil {
		return fmt.Errorf("connect to broker: %w", err)
	}
	log.Printf("[agent] connected, client_uuid=%s", a.config.Get("client_uuid", ""))

	// Drain pending reports
	a.syncPendingReports()

	// Heartbeat loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Send initial heartbeat
	a.sendHeartbeat()

	for {
		select {
		case <-a.ctx.Done():
			a.mqtt.Disconnect()
			a.config.Close()
			return nil
		case <-ticker.C:
			a.sendHeartbeat()
		}
	}
}

// Stop signals the agent to shut down.
func (a *Agent) Stop() {
	if a.cancelFn != nil {
		a.cancelFn()
	}
}

func (a *Agent) sendHeartbeat() {
	uptime := int64(time.Since(a.startAt).Seconds())
	a.mqtt.PublishHeartbeat(Heartbeat{
		Status:        "idle",
		UptimeSeconds: uptime,
	})
}

func (a *Agent) handleBackupCommand(cmd BackupCommand) {
	if cmd.Config == nil {
		log.Printf("[agent] ignoring command with nil config, action=%s", cmd.Action)
		return
	}

	switch cmd.Action {
	case "start_backup":
		log.Printf("[agent] received backup command: profile=%s src=%s",
			cmd.Config.Profile, cmd.Config.SrcDir)
		go a.executeBackup(cmd)
	case "start_restore":
		log.Printf("[agent] received restore command: archive=%s dst=%s",
			cmd.Config.ArchivePath, cmd.Config.DstDir)
		go a.executeRestore(cmd)
	default:
		log.Printf("[agent] ignoring unknown command action=%s", cmd.Action)
	}
}

func (a *Agent) executeScheduledBackup(cmd BackupCommand) {
	a.executeBackupWithMethod(cmd, "scheduled")
}

func (a *Agent) executeBackup(cmd BackupCommand) {
	a.executeBackupWithMethod(cmd, "orchestrator")
}

func (a *Agent) executeBackupWithMethod(cmd BackupCommand, method string) {
	cfg := core.BackupConfig{
		SrcDir:       cmd.Config.SrcDir,
		DstDir:       cmd.Config.DstDir,
		Profile:      cmd.Config.Profile,
		Password:     cmd.Config.Password,
		PasswordHint: cmd.Config.PasswordHint,
		Description:  cmd.Config.Description,
		Workers:      cmd.Config.Workers,
		Blocklist:    cmd.Config.Blocklist,
	}

	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}

	startedAt := time.Now().UTC().Format(time.RFC3339)

	logFn := func(msg string) {
		log.Printf("[backup] %s", msg)
	}

	result, err := core.RunBackup(a.ctx, cfg, logFn)

	completedAt := time.Now().UTC().Format(time.RFC3339)

	status := BackupStatus{
		CommandID:   cmd.CommandID,
		Method:      method,
		Profile:     cfg.Profile,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}

	if err != nil {
		status.Status = "failure"
		status.ErrorMessage = err.Error()
		log.Printf("[agent] backup failed: %v", err)
	} else {
		status.Status = "success"
		status.ArchivePath = result.ArchivePath
		status.ArchivePassword = result.Password
		status.FileCount = result.FileCount
		log.Printf("[agent] backup complete: %d files -> %s", result.FileCount, result.ArchivePath)
	}

	a.reportStatus(status)
}

func (a *Agent) executeRestore(cmd BackupCommand) {
	cfg := core.RestoreConfig{
		ArchivePath: cmd.Config.ArchivePath,
		DstDir:      cmd.Config.DstDir,
		Password:    cmd.Config.Password,
		Workers:     cmd.Config.Workers,
	}

	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}

	startedAt := time.Now().UTC().Format(time.RFC3339)

	logFn := func(msg string) {
		log.Printf("[restore] %s", msg)
	}

	result, err := core.RunRestore(cfg, logFn)

	completedAt := time.Now().UTC().Format(time.RFC3339)

	status := BackupStatus{
		CommandID:   cmd.CommandID,
		Method:      "orchestrator",
		Profile:     "restore",
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}

	if err != nil {
		status.Status = "failure"
		status.ErrorMessage = err.Error()
		log.Printf("[agent] restore failed: %v", err)
	} else {
		status.Status = "success"
		status.FileCount = int64(result.FileCount)
		log.Printf("[agent] restore complete: %d files", result.FileCount)
	}

	a.reportStatus(status)
}

func (a *Agent) reportStatus(status BackupStatus) {
	if a.mqtt.IsConnected() {
		if err := a.mqtt.PublishBackupStatus(status); err != nil {
			log.Printf("[agent] failed to publish status, queuing: %v", err)
			a.queueStatus(status)
		}
	} else {
		log.Printf("[agent] broker unreachable, queuing status report")
		a.queueStatus(status)
	}
}

func (a *Agent) queueStatus(status BackupStatus) {
	data, err := json.Marshal(status)
	if err != nil {
		log.Printf("[agent] failed to marshal status for queue: %v", err)
		return
	}
	if err := a.config.QueueReport(data); err != nil {
		log.Printf("[agent] failed to queue report: %v", err)
	}
}

func (a *Agent) syncPendingReports() {
	reports, err := a.config.GetPendingReports()
	if err != nil {
		log.Printf("[agent] failed to get pending reports: %v", err)
		return
	}
	if len(reports) == 0 {
		return
	}

	log.Printf("[agent] draining %d pending reports", len(reports))
	for _, r := range reports {
		var status BackupStatus
		if err := json.Unmarshal([]byte(r.Payload), &status); err != nil {
			log.Printf("[agent] invalid pending report id=%d, deleting: %v", r.ID, err)
			a.config.DeletePendingReport(r.ID)
			continue
		}

		if err := a.mqtt.PublishBackupStatus(status); err != nil {
			log.Printf("[agent] failed to send pending report id=%d: %v", r.ID, err)
			break // Stop draining if broker is unavailable
		}
		a.config.DeletePendingReport(r.ID)
	}
}

func (a *Agent) handleScheduleSync(sync ScheduleSync) {
	log.Printf("[agent] received schedule sync: %d schedules", len(sync.Schedules))
	for i, s := range sync.Schedules {
		log.Printf("[agent]   schedule[%d]: id=%d profile=%s cron=%s enabled=%v", i, s.ID, s.Profile, s.CronExpr, s.Enabled)
	}
	if err := a.config.SyncSchedules(sync.Schedules); err != nil {
		log.Printf("[agent] failed to sync schedules: %v", err)
		return
	}
	log.Printf("[agent] schedules saved to local db, updating task scheduler...")
	if err := a.UpdateTaskScheduler(); err != nil {
		log.Printf("[agent] failed to update task scheduler: %v", err)
	}
}

// RunSchedule executes a single scheduled backup by schedule ID.
// This is called by Windows Task Scheduler.
func (a *Agent) RunSchedule(scheduleID int) error {
	// Ensure context is initialised (Run() is not called in one-shot mode)
	if a.ctx == nil {
		a.ctx, a.cancelFn = context.WithCancel(context.Background())
	}

	// Connect to MQTT for status reporting (best-effort)
	a.mqtt.SetCommandHandler(func(cmd BackupCommand) {})
	a.mqtt.SetScheduleHandler(func(sync ScheduleSync) {})
	if err := a.mqtt.Connect(); err != nil {
		log.Printf("[agent] broker unavailable, will queue status: %v", err)
	}

	sched, err := a.config.GetLocalSchedule(scheduleID)
	if err != nil {
		return fmt.Errorf("schedule %d not found: %w", scheduleID, err)
	}

	if !sched.Enabled {
		log.Printf("[agent] schedule %d is disabled, skipping", scheduleID)
		return nil
	}

	log.Printf("[agent] running schedule %d: profile=%s src=%s", scheduleID, sched.Profile, sched.SrcDir)

	cmd := BackupCommand{
		CommandID: fmt.Sprintf("sched-%d-%d", scheduleID, time.Now().Unix()),
		Action:    "start_backup",
		Config: &BackupCommandConfig{
			SrcDir:  sched.SrcDir,
			DstDir:  sched.DstDir,
			Profile: sched.Profile,
			Workers: 4,
		},
	}

	// Execute synchronously (Task Scheduler waits for completion)
	a.executeScheduledBackup(cmd)

	// Clean up
	a.mqtt.Disconnect()
	a.config.Close()
	return nil
}

// ReportManualBackup reports a manual backup result to the orchestrator.
// Called from CLI/GUI backup path when agent config exists.
func ReportManualBackup(configDBPath string, result core.BackupResult, profile string, err error) {
	cfg, openErr := OpenConfig(configDBPath)
	if openErr != nil {
		return // No agent config, nothing to report
	}
	defer cfg.Close()

	clientUUID := cfg.Get("client_uuid", "")
	if clientUUID == "" {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	status := BackupStatus{
		Method:      "manual",
		Profile:     profile,
		StartedAt:   now,
		CompletedAt: now,
	}

	if err != nil {
		status.Status = "failure"
		status.ErrorMessage = err.Error()
	} else {
		status.Status = "success"
		status.ArchivePath = result.ArchivePath
		status.FileCount = result.FileCount
	}

	data, marshalErr := json.Marshal(status)
	if marshalErr != nil {
		return
	}

	// Try to publish directly; fall back to queue
	brokerAddr := cfg.Get("broker_address", "localhost")
	brokerPort := 1883
	fmt.Sscanf(cfg.Get("broker_port", "1883"), "%d", &brokerPort)

	mqttClient := NewMqttClient(brokerAddr, brokerPort,
		cfg.Get("mqtt_username", ""), cfg.Get("mqtt_password", ""), clientUUID)

	if connectErr := mqttClient.Connect(); connectErr == nil {
		mqttClient.PublishBackupStatus(status)
		mqttClient.Disconnect()
	} else {
		cfg.QueueReport(data)
	}
}
