package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"mybackup/core"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: backup-cli <backup|restore> [flags]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "backup":
		runBackupCLI(os.Args[2:])
	case "restore":
		runRestoreCLI(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nUsage: backup-cli <backup|restore> [flags]\n", os.Args[1])
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
