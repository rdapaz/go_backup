package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"mybackup/core"
	"mybackup/core/agent"

	"github.com/google/uuid"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: backup-cli <backup|restore|register|agent> [flags]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "backup":
		runBackupCLI(os.Args[2:])
	case "restore":
		runRestoreCLI(os.Args[2:])
	case "register":
		runRegisterCLI(os.Args[2:])
	case "agent":
		runAgentCLI(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nUsage: backup-cli <backup|restore|register|agent> [flags]\n", os.Args[1])
		os.Exit(1)
	}
}

func runBackupCLI(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)

	var cfg core.BackupConfig
	fs.StringVar(&cfg.SrcDir, "src", "", "Source root directory to back up")
	fs.StringVar(&cfg.DstDir, "dst", "", "Destination root directory for backups")
	fs.StringVar(&cfg.Password, "password", "", "Password for archive (auto-generated if empty)")
	fs.StringVar(&cfg.PasswordHint, "hint", "", "Optional password hint")
	fs.StringVar(&cfg.Description, "desc", "", "Backup description (e.g. 'Documents backup 2026-01')")
	fs.StringVar(&cfg.Profile, "profile", core.ProfileAll, "Backup profile: all|documents|jetbrains|databases|photos")
	fs.IntVar(&cfg.Workers, "workers", runtime.NumCPU(), "Number of concurrent workers")
	fs.BoolVar(&cfg.KeepStage, "keep-stage", false, "Keep staging directory after archive creation")
	fs.Parse(args)

	if cfg.SrcDir == "" || cfg.DstDir == "" {
		fmt.Fprintln(os.Stderr, "Both -src and -dst are required.")
		fs.Usage()
		os.Exit(1)
	}

	if !core.IsValidProfile(cfg.Profile) {
		fmt.Fprintf(os.Stderr, "Invalid profile: %s\n", cfg.Profile)
		os.Exit(1)
	}

	log := func(msg string) {
		fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
	}

	ctx := context.Background()
	result, err := core.RunBackup(ctx, cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Backup failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("----- DASHLANE SECURE NOTE -----")
	fmt.Println("Title: Backup password -", filepath.Base(result.ArchivePath))
	fmt.Println("Archive:", result.ArchivePath)
	fmt.Println("Profile:", cfg.Profile)
	fmt.Println("Files:", result.FileCount)
	fmt.Println("Password:", result.Password)
	if cfg.PasswordHint != "" {
		fmt.Println("Hint:", cfg.PasswordHint)
	}
	fmt.Println("--------------------------------")
}

func runRestoreCLI(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)

	var cfg core.RestoreConfig
	fs.StringVar(&cfg.ArchivePath, "archive", "", "Path to .tar.zst.enc or .7z backup archive")
	fs.StringVar(&cfg.StageDir, "stage", "", "Path to extracted staging directory")
	fs.StringVar(&cfg.DstDir, "dst", "", "Destination root directory to restore into")
	fs.StringVar(&cfg.Password, "password", "", "Password for archive")
	fs.IntVar(&cfg.Workers, "workers", runtime.NumCPU(), "Number of concurrent workers")
	fs.Parse(args)

	if cfg.DstDir == "" {
		fmt.Fprintln(os.Stderr, "-dst is required.")
		fs.Usage()
		os.Exit(1)
	}

	if cfg.ArchivePath == "" && cfg.StageDir == "" {
		fmt.Fprintln(os.Stderr, "Either -archive or -stage is required.")
		os.Exit(1)
	}

	log := func(msg string) {
		fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
	}

	result, err := core.RunRestore(cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Restore error: %v\n", err)
		if result != nil {
			fmt.Fprintf(os.Stderr, "Restored %d files with %d errors\n", result.FileCount, result.ErrorCount)
		}
		os.Exit(1)
	}

	fmt.Printf("Restore complete: %d files\n", result.FileCount)
}

func runRegisterCLI(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)

	var broker string
	var port int
	var configDB string
	fs.StringVar(&broker, "broker", "localhost", "MQTT broker hostname or IP")
	fs.IntVar(&port, "port", 1883, "MQTT broker port")
	fs.StringVar(&configDB, "config-db", "agent.db", "Path to agent config database")
	fs.Parse(args)

	// Open or create config database
	cfg, err := agent.OpenConfig(configDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open config database: %v\n", err)
		os.Exit(1)
	}
	defer cfg.Close()

	// Generate or retrieve client UUID
	clientUUID := cfg.Get("client_uuid", "")
	if clientUUID == "" {
		clientUUID = uuid.New().String()
		cfg.Set("client_uuid", clientUUID)
		fmt.Printf("Generated client UUID: %s\n", clientUUID)
	} else {
		fmt.Printf("Using existing client UUID: %s\n", clientUUID)
	}

	// Store broker config
	cfg.Set("broker_address", broker)
	cfg.Set("broker_port", fmt.Sprintf("%d", port))
	cfg.Set("config_db_path", configDB)

	// Get hostname and OS info
	hostname, _ := os.Hostname()
	osInfo := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)

	// Connect to broker (no auth for registration)
	mqttClient := agent.NewMqttClient(broker, port, "", "", clientUUID)
	if err := mqttClient.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to broker at %s:%d: %v\n", broker, port, err)
		os.Exit(1)
	}

	// Listen for registration response
	done := make(chan bool, 1)
	mqttClient.SubscribeRegistrationResponse(func(resp agent.RegistrationResponse) {
		if resp.Approved {
			fmt.Printf("Registration approved! Name: %s\n", resp.ClientName)
			cfg.Set("mqtt_username", resp.MqttUsername)
			cfg.Set("mqtt_password", resp.MqttPassword)
			fmt.Println("MQTT credentials stored in config database.")
		} else {
			fmt.Println("Registration denied by orchestrator.")
		}
		done <- true
	})

	// Get local IP (best effort)
	ipAddr := "unknown"
	// Simple approach: use hostname resolution
	fmt.Printf("Sending registration request to %s:%d...\n", broker, port)

	reg := agent.RegistrationRequest{
		ClientUUID:      clientUUID,
		Hostname:        hostname,
		IPAddress:       ipAddr,
		OS:              osInfo,
		GoBackupVersion: "1.0.0",
	}

	if err := mqttClient.PublishRegistration(reg); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to send registration: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Registration request sent. Waiting for approval (60s timeout)...")

	select {
	case <-done:
		// Response received
	case <-time.After(60 * time.Second):
		fmt.Println("Timeout waiting for registration response.")
		fmt.Println("The orchestrator admin needs to approve the registration.")
		fmt.Println("You can try again later with the same command.")
	}

	mqttClient.Disconnect()
}

func runAgentCLI(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: backup-cli agent <start|run-schedule> [flags]")
		os.Exit(1)
	}

	switch args[0] {
	case "start":
		runAgentStart(args[1:])
	case "run-schedule":
		runAgentRunSchedule(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown agent command: %s\nUsage: backup-cli agent <start|run-schedule> [flags]\n", args[0])
		os.Exit(1)
	}
}

func runAgentStart(args []string) {
	fs := flag.NewFlagSet("agent start", flag.ExitOnError)
	var configDB string
	fs.StringVar(&configDB, "config-db", "agent.db", "Path to agent config database")
	fs.Parse(args)

	a, err := agent.NewAgent(configDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start agent: %v\n", err)
		os.Exit(1)
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down agent...")
		a.Stop()
	}()

	fmt.Println("Agent starting...")
	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Agent error: %v\n", err)
		os.Exit(1)
	}
}

func runAgentRunSchedule(args []string) {
	fs := flag.NewFlagSet("agent run-schedule", flag.ExitOnError)
	var configDB string
	var scheduleID int
	fs.StringVar(&configDB, "config-db", "agent.db", "Path to agent config database")
	fs.IntVar(&scheduleID, "id", 0, "Schedule ID to execute")
	fs.Parse(args)

	if scheduleID <= 0 {
		fmt.Fprintln(os.Stderr, "-id is required and must be positive")
		os.Exit(1)
	}

	a, err := agent.NewAgent(configDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}

	// Connect to MQTT briefly for status reporting
	// Agent will queue the report if connection fails (offline resilience)

	if err := a.RunSchedule(scheduleID); err != nil {
		fmt.Fprintf(os.Stderr, "Schedule execution failed: %v\n", err)
		os.Exit(1)
	}
}
