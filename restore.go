// restore.go
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

type restoreJob struct {
	RelPath string
	Hash    string
	Size    int64
}

type restoreResult struct {
	RelPath string
	Err     error
}

func main() {
	var (
		archivePath string
		stageDir    string
		dstRoot     string
		password    string
		workers     int
	)

	flag.StringVar(&archivePath, "archive", "", "Path to .7z backup archive (optional if -stage is used)")
	flag.StringVar(&stageDir, "stage", "", "Path to extracted staging directory (optional if -archive is used)")
	flag.StringVar(&dstRoot, "dst", "", "Destination root directory to restore into")
	flag.StringVar(&password, "password", "", "Password for .7z archive (required if -archive is used)")
	flag.IntVar(&workers, "workers", runtime.NumCPU(), "Number of concurrent worker goroutines")
	flag.Parse()

	if dstRoot == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -dst <restoreRoot> [-archive <backup.7z> -password <pwd> | -stage <stagingDir>] [-workers N]\n", os.Args[0])
		os.Exit(1)
	}

	if archivePath == "" && stageDir == "" {
		fmt.Fprintln(os.Stderr, "Either -archive or -stage must be provided.")
		os.Exit(1)
	}
	if archivePath != "" && stageDir != "" {
		fmt.Fprintln(os.Stderr, "Provide only one of -archive or -stage, not both.")
		os.Exit(1)
	}

	dstRootAbs, err := filepath.Abs(dstRoot)
	checkErr("resolve dst", err)

	var stageDirAbs string

	if archivePath != "" {
		if password == "" {
			fmt.Fprintln(os.Stderr, "Password is required when using -archive.")
			os.Exit(1)
		}
		archivePathAbs, err := filepath.Abs(archivePath)
		checkErr("resolve archive", err)

		stageDirAbs, err = extractArchiveToTemp(archivePathAbs, password)
		checkErr("extract archive", err)
		defer func() {
			// clean up temp dir
			if err := os.RemoveAll(filepath.Dir(stageDirAbs)); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to remove temp extraction dir: %v\n", err)
			}
		}()
	} else {
		stageDirAbs, err = filepath.Abs(stageDir)
		checkErr("resolve stage", err)
	}

	fmt.Printf("Restoring from stage: %s\n", stageDirAbs)
	fmt.Printf("Restoring into: %s\n", dstRootAbs)

	if err := restoreFromStage(stageDirAbs, dstRootAbs, workers); err != nil {
		checkErr("restore", err)
	}

	fmt.Println("Restore completed successfully.")
}

func checkErr(ctx string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s error: %v\n", ctx, err)
		os.Exit(1)
	}
}

func extractArchiveToTemp(archivePath, password string) (string, error) {
	if _, err := exec.LookPath("7z"); err != nil {
		return "", fmt.Errorf("7z not found on PATH: %w", err)
	}

	tempBase, err := os.MkdirTemp("", "backup-restore-*")
	if err != nil {
		return "", err
	}

	archiveBase := stringsTrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath))
	outDir := tempBase

	args := []string{
		"x",
		"-p" + password,
		archivePath,
		"-o" + outDir,
		"-y",
	}

	cmd := exec.Command("7z", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Running: 7z %s\n", stringsJoin(args, " "))
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// We expect the stage directory to be <outDir>/<archiveBase>
	stageDir := filepath.Join(outDir, archiveBase)
	if _, err := os.Stat(stageDir); err == nil {
		return stageDir, nil
	}

	// Fallback: if that doesn't exist, maybe the archive root is the stage dir itself.
	// Look for backup.db in outDir or its immediate subdirs.
	dbPath := filepath.Join(outDir, "backup.db")
	if _, err := os.Stat(dbPath); err == nil {
		return outDir, nil
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		return "", fmt.Errorf("cannot locate stage dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(outDir, e.Name())
		dbPath := filepath.Join(candidate, "backup.db")
		if _, err := os.Stat(dbPath); err == nil {
			return candidate, nil
		}
	}

	return "", errors.New("could not locate staging directory with backup.db after extraction")
}

func restoreFromStage(stageDir, dstRoot string, workers int) error {
	dbPath := filepath.Join(stageDir, "backup.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// Single backup per DB (by design), but query anyway.
	var backupID int64
	var archiveName, profile, createdAt string
	row := db.QueryRow(`SELECT id, archive_name, profile, created_at FROM backups ORDER BY id LIMIT 1`)
	if err := row.Scan(&backupID, &archiveName, &profile, &createdAt); err != nil {
		return fmt.Errorf("read backup record: %w", err)
	}

	fmt.Printf("Backup metadata:\n  ID: %d\n  Archive name: %s\n  Profile: %s\n  Created: %s\n", backupID, archiveName, profile, createdAt)

	// Load all file records
	rows, err := db.Query(`SELECT rel_path, content_hash, size_bytes FROM files WHERE backup_id = ?`, backupID)
	if err != nil {
		return fmt.Errorf("query files: %w", err)
	}
	defer rows.Close()

	var jobs []restoreJob
	for rows.Next() {
		var j restoreJob
		if err := rows.Scan(&j.RelPath, &j.Hash, &j.Size); err != nil {
			return fmt.Errorf("scan file row: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows error: %w", err)
	}

	fmt.Printf("Restoring %d files...\n", len(jobs))

	if workers <= 0 {
		workers = 1
	}

	jobCh := make(chan restoreJob, workers*2)
	resultCh := make(chan restoreResult, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for job := range jobCh {
				res := restoreOneFile(stageDir, dstRoot, job)
				resultCh <- res
			}
		}(i)
	}

	go func() {
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
		wg.Wait()
		close(resultCh)
	}()

	var (
		count int
		errs  []error
	)
	for res := range resultCh {
		if res.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", res.RelPath, res.Err))
		} else {
			count++
			if count%500 == 0 {
				fmt.Printf("Restored %d files...\n", count)
			}
		}
	}

	fmt.Printf("Restored %d files\n", count)
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "Encountered %d errors during restore. First few:\n", len(errs))
		for i := 0; i < len(errs) && i < 10; i++ {
			fmt.Fprintf(os.Stderr, "  %v\n", errs[i])
		}
		return fmt.Errorf("restore completed with errors")
	}

	return nil
}

func restoreOneFile(stageDir, dstRoot string, job restoreJob) restoreResult {
	srcPath := filepath.Join(stageDir, "data", job.Hash)
	dstPath := filepath.Join(dstRoot, filepath.FromSlash(job.RelPath))

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return restoreResult{RelPath: job.RelPath, Err: fmt.Errorf("mkdir: %w", err)}
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return restoreResult{RelPath: job.RelPath, Err: fmt.Errorf("open blob: %w", err)}
	}
	defer srcFile.Close()

	tmpDst := dstPath + ".tmp"
	dstFile, err := os.Create(tmpDst)
	if err != nil {
		return restoreResult{RelPath: job.RelPath, Err: fmt.Errorf("create dst: %w", err)}
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		os.Remove(tmpDst)
		return restoreResult{RelPath: job.RelPath, Err: fmt.Errorf("copy: %w", err)}
	}
	if err := dstFile.Close(); err != nil {
		os.Remove(tmpDst)
		return restoreResult{RelPath: job.RelPath, Err: fmt.Errorf("close dst: %w", err)}
	}

	if err := os.Rename(tmpDst, dstPath); err != nil {
		os.Remove(tmpDst)
		return restoreResult{RelPath: job.RelPath, Err: fmt.Errorf("rename: %w", err)}
	}

	return restoreResult{RelPath: job.RelPath, Err: nil}
}

// ---- small helpers (no strings.Builder requirement) ----

func stringsTrimSuffix(s, suffix string) string {
	if len(suffix) == 0 || !stringsHasSuffix(s, suffix) {
		return s
	}
	return s[:len(s)-len(suffix)]
}

func stringsHasSuffix(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

func stringsJoin(elems []string, sep string) string {
	if len(elems) == 0 {
		return ""
	}
	if len(elems) == 1 {
		return elems[0]
	}
	n := len(sep) * (len(elems) - 1)
	for _, s := range elems {
		n += len(s)
	}
	var b []byte
	b = make([]byte, 0, n)
	b = append(b, elems[0]...)
	for _, s := range elems[1:] {
		b = append(b, sep...)
		b = append(b, s...)
	}
	return string(b)
}
