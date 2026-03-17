package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	mrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type BackupConfig struct {
	SrcDir       string
	DstDir       string
	Profile      string
	Password     string
	PasswordHint string
	Workers      int
	KeepStage    bool
	Blocklist    []string // directory names to skip during walk
}

type BackupResult struct {
	ArchivePath  string
	Password     string
	FileCount    int64
	StageDirPath string // populated even on failure so user can recover
}

type fileJob struct {
	AbsPath string
	RelPath string
}

type fileResult struct {
	RelPath      string
	AbsPath      string
	Size         int64
	ModTime      time.Time
	HashHex      string
	IsNewContent bool
	Err          error
}

// LogFunc is called by the backup/restore pipeline to report progress.
type LogFunc func(msg string)

func DefaultWorkers() int {
	return runtime.NumCPU()
}

// RunBackup executes the full backup pipeline and returns the result.
func RunBackup(ctx context.Context, cfg BackupConfig, log LogFunc) (*BackupResult, error) {
	if log == nil {
		log = func(string) {}
	}

	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers()
	}

	srcDirAbs, err := filepath.Abs(cfg.SrcDir)
	if err != nil {
		return nil, fmt.Errorf("resolve src: %w", err)
	}
	dstDirAbs, err := filepath.Abs(cfg.DstDir)
	if err != nil {
		return nil, fmt.Errorf("resolve dst: %w", err)
	}

	if _, err := os.Stat(srcDirAbs); err != nil {
		return nil, fmt.Errorf("stat src: %w", err)
	}
	if err := os.MkdirAll(dstDirAbs, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir dst: %w", err)
	}

	archiveBase := generateArchiveBaseName()
	stageDir := filepath.Join(dstDirAbs, archiveBase)
	dataDir := filepath.Join(stageDir, "data")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir stage: %w", err)
	}

	dbPath := filepath.Join(stageDir, "backup.db")
	db, err := InitDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}

	password := cfg.Password
	if password == "" {
		gen, err := GenerateStrongPassword()
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("generate password: %w", err)
		}
		password = gen
		log(fmt.Sprintf("Generated strong password: %s", password))
	}

	backupID, err := CreateBackupRecord(db, archiveBase, cfg.Profile, cfg.PasswordHint)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create backup record: %w", err)
	}

	if err := StoreBackupPasswordHash(db, backupID, password); err != nil {
		db.Close()
		return nil, fmt.Errorf("store password hash: %w", err)
	}

	// Build blocklist lookup set
	blockSet := make(map[string]struct{}, len(cfg.Blocklist))
	for _, b := range cfg.Blocklist {
		blockSet[b] = struct{}{}
	}

	log(fmt.Sprintf("Starting backup  Source: %s  Dest: %s  Profile: %s", srcDirAbs, dstDirAbs, cfg.Profile))
	if len(blockSet) > 0 {
		log(fmt.Sprintf("Blocklist: %d directory names will be skipped", len(blockSet)))
	}

	fileCount, err := runBackupPipeline(ctx, srcDirAbs, stageDir, dataDir, db, backupID, cfg.Workers, cfg.Profile, blockSet, log)
	if err != nil {
		db.Close()
		return &BackupResult{StageDirPath: stageDir, FileCount: 0, Password: password}, err
	}

	// Close DB before 7z reads it (Windows file lock)
	if err := db.Close(); err != nil {
		return &BackupResult{StageDirPath: stageDir, FileCount: fileCount, Password: password},
			fmt.Errorf("close db: %w", err)
	}

	log("File staging complete, creating 7z archive...")

	archivePath := filepath.Join(dstDirAbs, archiveBase+".7z")
	if err := Create7zArchive(dstDirAbs, archiveBase, archivePath, password, log); err != nil {
		log(fmt.Sprintf("WARNING: 7z failed but staging directory preserved at: %s", stageDir))
		log("You can restore directly from the staging directory using the Restore tab.")
		return &BackupResult{StageDirPath: stageDir, FileCount: fileCount, Password: password},
			fmt.Errorf("7z archive: %w", err)
	}

	log(fmt.Sprintf("Archive created: %s", archivePath))

	if !cfg.KeepStage {
		log("Removing staging directory...")
		if err := os.RemoveAll(stageDir); err != nil {
			log(fmt.Sprintf("Warning: failed to remove staging dir: %v", err))
		}
	}

	return &BackupResult{
		ArchivePath:  archivePath,
		Password:     password,
		FileCount:    fileCount,
		StageDirPath: stageDir,
	}, nil
}

func generateArchiveBaseName() string {
	now := time.Now()
	ts := now.Format("20060102_150405")
	suffix := mrand.Intn(10000)
	return fmt.Sprintf("%s_%04d", ts, suffix)
}

func runBackupPipeline(
	ctx context.Context,
	srcDir, stageDir, dataDir string,
	db *sql.DB,
	backupID int64,
	workers int,
	profile string,
	blockSet map[string]struct{},
	log LogFunc,
) (int64, error) {
	jobs := make(chan fileJob, workers*2)
	results := make(chan fileResult, workers*2)
	var wg sync.WaitGroup
	var contentMap sync.Map

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				results <- processFileJob(job, dataDir, &contentMap)
			}
		}()
	}

	dbErrCh := make(chan error, 1)
	countCh := make(chan int64, 1)
	go func() {
		count, err := writeResultsToDB(db, backupID, results, log)
		countCh <- count
		dbErrCh <- err
	}()

	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				log(fmt.Sprintf("Skipping (no permission): %s", path))
				return nil
			}
			if pe, ok := err.(*os.PathError); ok {
				msg := strings.ToLower(pe.Err.Error())
				if strings.Contains(msg, "access is denied") {
					log(fmt.Sprintf("Skipping (access denied): %s", path))
					return nil
				}
			}
			return err
		}

		if d.IsDir() {
			// Blocklist applies to all profiles
			if _, skip := blockSet[d.Name()]; skip {
				return fs.SkipDir
			}
			// JetBrains-specific directory exclusions
			if profile == ProfileJetBrains {
				if _, skip := JBExcludeDirNames[d.Name()]; skip {
					return fs.SkipDir
				}
			}
			return nil
		}

		if !ShouldBackup(path, profile) {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case jobs <- fileJob{AbsPath: path, RelPath: rel}:
		}
		return nil
	})

	close(jobs)
	wg.Wait()
	close(results)

	if walkErr != nil && !errors.Is(walkErr, fs.SkipDir) {
		return 0, fmt.Errorf("walk error: %w", walkErr)
	}

	count := <-countCh
	if err := <-dbErrCh; err != nil {
		return count, fmt.Errorf("db writer error: %w", err)
	}

	return count, nil
}

func ShouldBackup(path string, profile string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch profile {
	case ProfileDocuments:
		_, ok := DocumentExts[ext]
		return ok
	case ProfileDatabases:
		_, ok := DatabaseExts[ext]
		return ok
	case ProfilePhotos:
		_, ok := PhotoExts[ext]
		return ok
	case ProfileJetBrains:
		if _, skip := JBExcludeFileNames[filepath.Base(path)]; skip {
			return false
		}
		return true
	case ProfileAll:
		// Back up everything — the blocklist handles directory filtering
		return true
	}
	return false
}

func processFileJob(job fileJob, dataDir string, contentMap *sync.Map) fileResult {
	info, err := os.Stat(job.AbsPath)
	if err != nil {
		return fileResult{Err: fmt.Errorf("stat: %w", err), AbsPath: job.AbsPath, RelPath: job.RelPath}
	}
	if !info.Mode().IsRegular() {
		return fileResult{Err: fmt.Errorf("not a regular file"), AbsPath: job.AbsPath, RelPath: job.RelPath}
	}

	hashHex, err := hashFile(job.AbsPath)
	if err != nil {
		return fileResult{Err: fmt.Errorf("hash: %w", err), AbsPath: job.AbsPath, RelPath: job.RelPath}
	}

	_, loaded := contentMap.LoadOrStore(hashHex, struct{}{})
	isNew := !loaded

	if isNew {
		if err := copyFileToBlob(job.AbsPath, filepath.Join(dataDir, hashHex)); err != nil {
			return fileResult{Err: fmt.Errorf("copy blob: %w", err), AbsPath: job.AbsPath, RelPath: job.RelPath}
		}
	}

	return fileResult{
		RelPath:      filepath.ToSlash(job.RelPath),
		AbsPath:      job.AbsPath,
		Size:         info.Size(),
		ModTime:      info.ModTime().UTC(),
		HashHex:      hashHex,
		IsNewContent: isNew,
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFileToBlob(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	tmpDst := dst + ".tmp"
	dstFile, err := os.Create(tmpDst)
	if err != nil {
		return err
	}

	_, err = io.Copy(dstFile, srcFile)
	closeErr := dstFile.Close()
	if err != nil {
		os.Remove(tmpDst)
		return err
	}
	if closeErr != nil {
		os.Remove(tmpDst)
		return closeErr
	}

	return os.Rename(tmpDst, dst)
}

func writeResultsToDB(db *sql.DB, backupID int64, results <-chan fileResult, log LogFunc) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	insertContent, err := tx.Prepare(`INSERT OR IGNORE INTO contents (hash, size_bytes) VALUES (?, ?)`)
	if err != nil {
		return 0, err
	}
	defer insertContent.Close()

	insertFile, err := tx.Prepare(`INSERT INTO files (backup_id, rel_path, size_bytes, mod_time_utc, content_hash) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer insertFile.Close()

	var (
		count    int64
		lastTime = time.Now()
		firstErr error
	)

	for res := range results {
		if res.Err != nil {
			log(fmt.Sprintf("Skipping: %s (%v)", res.AbsPath, res.Err))
			continue
		}
		if firstErr != nil {
			continue
		}

		if res.IsNewContent {
			if _, err := insertContent.Exec(res.HashHex, res.Size); err != nil {
				firstErr = fmt.Errorf("insert content: %w", err)
				continue
			}
		}

		if _, err := insertFile.Exec(backupID, res.RelPath, res.Size, res.ModTime.Format(time.RFC3339), res.HashHex); err != nil {
			firstErr = fmt.Errorf("insert file: %w", err)
			continue
		}

		count++
		if time.Since(lastTime) > 5*time.Second {
			log(fmt.Sprintf("Processed %d files...", count))
			lastTime = time.Now()
		}
	}

	if firstErr != nil {
		return count, firstErr
	}

	if err := tx.Commit(); err != nil {
		return count, err
	}

	log(fmt.Sprintf("Finished writing %d file records to DB", count))
	return count, nil
}

func Create7zArchive(dstDir, stageFolderName, archivePath, password string, log LogFunc) error {
	if _, err := exec.LookPath("7z"); err != nil {
		return fmt.Errorf("7z not found on PATH: %w", err)
	}

	args := []string{
		"a", "-t7z",
		"-p" + password,
		"-mhe=on",
		archivePath,
		stageFolderName,
	}

	cmd := exec.Command("7z", args...)
	cmd.Dir = dstDir

	log(fmt.Sprintf("Running: 7z %s", strings.Join(args, " ")))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("7z failed: %w\n%s", err, string(output))
	}

	return nil
}
