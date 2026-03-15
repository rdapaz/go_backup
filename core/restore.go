package core

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type RestoreConfig struct {
	ArchivePath string // set for archive mode
	StageDir    string // set for stage mode
	DstDir      string
	Password    string // required for archive mode
	Workers     int
}

type RestoreResult struct {
	FileCount  int
	ErrorCount int
}

func RunRestore(cfg RestoreConfig, log LogFunc) (*RestoreResult, error) {
	if log == nil {
		log = func(string) {}
	}

	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers()
	}

	dstRootAbs, err := filepath.Abs(cfg.DstDir)
	if err != nil {
		return nil, fmt.Errorf("resolve dst: %w", err)
	}

	var stageDirAbs string

	if cfg.ArchivePath != "" {
		if cfg.Password == "" {
			return nil, fmt.Errorf("password is required when restoring from archive")
		}
		archivePathAbs, err := filepath.Abs(cfg.ArchivePath)
		if err != nil {
			return nil, fmt.Errorf("resolve archive: %w", err)
		}

		stageDirAbs, err = extractArchiveToTemp(archivePathAbs, cfg.Password, log)
		if err != nil {
			return nil, fmt.Errorf("extract archive: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(filepath.Dir(stageDirAbs)); err != nil {
				log(fmt.Sprintf("Warning: failed to remove temp dir: %v", err))
			}
		}()
	} else if cfg.StageDir != "" {
		stageDirAbs, err = filepath.Abs(cfg.StageDir)
		if err != nil {
			return nil, fmt.Errorf("resolve stage: %w", err)
		}
	} else {
		return nil, fmt.Errorf("either ArchivePath or StageDir must be set")
	}

	log(fmt.Sprintf("Restoring from: %s", stageDirAbs))
	log(fmt.Sprintf("Restoring into: %s", dstRootAbs))

	return restoreFromStage(stageDirAbs, dstRootAbs, cfg.Workers, log)
}

func extractArchiveToTemp(archivePath, password string, log LogFunc) (string, error) {
	if _, err := exec.LookPath("7z"); err != nil {
		return "", fmt.Errorf("7z not found on PATH: %w", err)
	}

	tempBase, err := os.MkdirTemp("", "backup-restore-*")
	if err != nil {
		return "", err
	}

	archiveBase := strings.TrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath))

	args := []string{
		"x",
		"-p" + password,
		archivePath,
		"-o" + tempBase,
		"-y",
	}

	cmd := exec.Command("7z", args...)
	log(fmt.Sprintf("Running: 7z %s", strings.Join(args, " ")))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("7z extract failed: %w\n%s", err, string(output))
	}

	// Try expected path first
	stageDir := filepath.Join(tempBase, archiveBase)
	if _, err := os.Stat(stageDir); err == nil {
		return stageDir, nil
	}

	// Fallback: look for backup.db
	if _, err := os.Stat(filepath.Join(tempBase, "backup.db")); err == nil {
		return tempBase, nil
	}

	entries, err := os.ReadDir(tempBase)
	if err != nil {
		return "", fmt.Errorf("cannot locate stage dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(tempBase, e.Name())
		if _, err := os.Stat(filepath.Join(candidate, "backup.db")); err == nil {
			return candidate, nil
		}
	}

	return "", errors.New("could not locate staging directory with backup.db after extraction")
}

func restoreFromStage(stageDir, dstRoot string, workers int, log LogFunc) (*RestoreResult, error) {
	dbPath := filepath.Join(stageDir, "backup.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	backup, err := LoadBackupRecord(db)
	if err != nil {
		return nil, fmt.Errorf("read backup record: %w", err)
	}

	log(fmt.Sprintf("Backup: %s  Profile: %s  Created: %s", backup.ArchiveName, backup.Profile, backup.CreatedAt))

	records, err := LoadFileRecords(db, backup.ID)
	if err != nil {
		return nil, fmt.Errorf("load files: %w", err)
	}

	log(fmt.Sprintf("Restoring %d files...", len(records)))

	type restoreResult struct {
		RelPath string
		Err     error
	}

	jobCh := make(chan FileRecord, workers*2)
	resultCh := make(chan restoreResult, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rec := range jobCh {
				err := restoreOneFile(stageDir, dstRoot, rec)
				resultCh <- restoreResult{RelPath: rec.RelPath, Err: err}
			}
		}()
	}

	go func() {
		for _, r := range records {
			jobCh <- r
		}
		close(jobCh)
		wg.Wait()
		close(resultCh)
	}()

	var count, errCount int
	for res := range resultCh {
		if res.Err != nil {
			errCount++
			log(fmt.Sprintf("Error restoring %s: %v", res.RelPath, res.Err))
		} else {
			count++
			if count%500 == 0 {
				log(fmt.Sprintf("Restored %d files...", count))
			}
		}
	}

	log(fmt.Sprintf("Restored %d files (%d errors)", count, errCount))

	result := &RestoreResult{FileCount: count, ErrorCount: errCount}
	if errCount > 0 {
		return result, fmt.Errorf("restore completed with %d errors", errCount)
	}
	return result, nil
}

func restoreOneFile(stageDir, dstRoot string, rec FileRecord) error {
	srcPath := filepath.Join(stageDir, "data", rec.ContentHash)
	dstPath := filepath.Join(dstRoot, filepath.FromSlash(rec.RelPath))

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open blob: %w", err)
	}
	defer srcFile.Close()

	tmpDst := dstPath + ".tmp"
	dstFile, err := os.Create(tmpDst)
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		os.Remove(tmpDst)
		return fmt.Errorf("copy: %w", err)
	}
	if err := dstFile.Close(); err != nil {
		os.Remove(tmpDst)
		return fmt.Errorf("close dst: %w", err)
	}

	if err := os.Rename(tmpDst, dstPath); err != nil {
		os.Remove(tmpDst)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}
