// backup.go
package main

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
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

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/scrypt"
)

// Profiles
const (
	ProfileDocuments = "documents"
	ProfileJetBrains = "jetbrains"
	ProfileDatabases = "databases"
	ProfilePhotos    = "photos"
)

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

// Extension filters for profiles
var documentExts = map[string]struct{}{
	".doc":  {},
	".docx": {},
	".xls":  {},
	".xlsx": {},
	".ppt":  {},
	".pptx": {},
	".vsd":  {},
	".vsdx": {},
	".mpp":  {},
	".pdf":  {},
}

var databaseExts = map[string]struct{}{
	".sqlite":  {},
	".sqlite3": {},
	".db":      {},
	".mdb":     {},
	".accdb":   {},
}

var photoExts = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".tif":  {},
	".tiff": {},
	".bmp":  {},
	".gif":  {},
	".heic": {},
	".heif": {},
}

// JetBrains exclusions (we backup *projects*, but skip heavy/cache dirs)
var jbExcludeDirNames = map[string]struct{}{
	".git":         {},
	".svn":         {},
	".hg":          {},
	".idea":        {}, // simplify: skip IDE config entirely for now
	"node_modules": {},
	"__pycache__":  {},
	".venv":        {},
	"venv":         {},
	"target":       {},
	"build":        {},
	"dist":         {},
	".gradle":      {},
	".m2":          {},
}

var jbExcludeFileNames = map[string]struct{}{
	"workspace.xml": {},
}

// ----------------- main -------------------

func main() {
	var (
		srcDir       string
		dstDir       string
		password     string
		passwordHint string
		profile      string
		workers      int
		keepStage    bool
	)

	flag.StringVar(&srcDir, "src", "", "Source root directory to back up")
	flag.StringVar(&dstDir, "dst", "", "Destination root directory for backups")
	flag.StringVar(&password, "password", "", "Password for 7z archive (if empty, a strong one is generated)")
	flag.StringVar(&passwordHint, "hint", "", "Optional password hint (stored in DB and included in Dashlane note)")
	flag.StringVar(&profile, "profile", ProfileDocuments, "Backup profile: documents | jetbrains | databases | photos")
	flag.IntVar(&workers, "workers", runtime.NumCPU(), "Number of concurrent worker goroutines")
	flag.BoolVar(&keepStage, "keep-stage", false, "Keep staging directory after creating 7z archive")
	flag.Parse()

	if srcDir == "" || dstDir == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -src <sourceDir> -dst <destDir> -profile <documents|jetbrains|databases|photos> [-password <pwd>] [-hint <hint>] [-workers N] [--keep-stage]\n", os.Args[0])
		os.Exit(1)
	}

	switch profile {
	case ProfileDocuments, ProfileJetBrains, ProfileDatabases, ProfilePhotos:
		// ok
	default:
		fmt.Fprintf(os.Stderr, "Invalid profile: %s (expected documents|jetbrains|databases|photos)\n", profile)
		os.Exit(1)
	}

	srcDirAbs, err := filepath.Abs(srcDir)
	checkErr("resolve src", err)

	dstDirAbs, err := filepath.Abs(dstDir)
	checkErr("resolve dst", err)

	if _, err := os.Stat(srcDirAbs); err != nil {
		checkErr("stat src", err)
	}
	if err := os.MkdirAll(dstDirAbs, 0o755); err != nil {
		checkErr("mkdir dst", err)
	}

	// Determine archive base name (timestamp + random suffix)
	archiveBase := generateArchiveBaseName()
	stageDir := filepath.Join(dstDirAbs, archiveBase)
	dataDir := filepath.Join(stageDir, "data")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		checkErr("mkdir stage", err)
	}

	// Initialize SQLite DB inside staging dir
	dbPath := filepath.Join(stageDir, "backup.db")
	db, err := initDB(dbPath)
	checkErr("init db", err)
	defer db.Close()

	// Prepare password
	if password == "" {
		gen, err := generateStrongPassword()
		checkErr("generate password", err)
		password = gen
		fmt.Println("Generated strong backup password (save this in Dashlane):")
		fmt.Println(password)
	}

	backupID, err := createBackupRecord(db, archiveBase, profile, passwordHint)
	checkErr("create backup record", err)

	if err := storeBackupPasswordHash(db, backupID, password); err != nil {
		checkErr("store backup password hash", err)
	}

	fmt.Printf("Starting backup\n  Source: %s\n  Destination: %s\n  Profile: %s\n  Stage: %s\n  Archive base: %s\n",
		srcDirAbs, dstDirAbs, profile, stageDir, archiveBase)

	ctx := context.Background()
	if err := runBackup(ctx, srcDirAbs, stageDir, dataDir, db, backupID, workers, profile); err != nil {
		checkErr("backup", err)
	}

	// ✅ Close DB here so 7z can read backup.db on Windows
	if err := db.Close(); err != nil {
		checkErr("close db", err)
	}

	fmt.Println("File staging & metadata complete, creating 7z archive...")

	archivePath := filepath.Join(dstDirAbs, archiveBase+".7z")
	if err := create7zArchive(dstDirAbs, archiveBase, archivePath, password); err != nil {
		checkErr("7z archive", err)
	}

	fmt.Printf("Archive created: %s\n", archivePath)

	// Output Dashlane secure note template (Option A)
	printDashlaneNote(archivePath, profile, password, passwordHint)

	if !keepStage {
		fmt.Printf("Removing staging directory: %s\n", stageDir)
		if err := os.RemoveAll(stageDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove staging directory: %v\n", err)
		}
	} else {
		fmt.Printf("Staging directory kept at: %s\n", stageDir)
	}
}

// generateArchiveBaseName returns yyyymmdd_hhmmss_xxxx
func generateArchiveBaseName() string {
	now := time.Now()
	ts := now.Format("20060102_150405")
	mrand.Seed(now.UnixNano())
	suffix := mrand.Intn(10000)
	return fmt.Sprintf("%s_%04d", ts, suffix)
}

func checkErr(context string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s error: %v\n", context, err)
		os.Exit(1)
	}
}

// ----------------- DB -------------------

func initDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	schema := `
CREATE TABLE IF NOT EXISTS backups (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    archive_name  TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    profile       TEXT NOT NULL,
    password_hash TEXT,
    kdf_salt      BLOB,
    password_hint TEXT
);

CREATE TABLE IF NOT EXISTS contents (
    hash          TEXT PRIMARY KEY,
    size_bytes    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS files (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    backup_id    INTEGER NOT NULL,
    rel_path     TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL,
    mod_time_utc TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    FOREIGN KEY(backup_id) REFERENCES backups(id),
    FOREIGN KEY(content_hash) REFERENCES contents(hash)
);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func createBackupRecord(db *sql.DB, archiveName, profile, passwordHint string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(`INSERT INTO backups (archive_name, created_at, profile, password_hint) VALUES (?, ?, ?, ?)`,
		archiveName, now, profile, passwordHint)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func storeBackupPasswordHash(db *sql.DB, backupID int64, password string) error {
	const (
		N      = 1 << 15
		r      = 8
		p      = 1
		keyLen = 32
	)

	salt := make([]byte, 16)
	if _, err := crand.Read(salt); err != nil {
		return err
	}

	derived, err := scrypt.Key([]byte(password), salt, N, r, p, keyLen)
	if err != nil {
		return err
	}

	hashB64 := base64.StdEncoding.EncodeToString(derived)

	_, err = db.Exec(`UPDATE backups SET password_hash = ?, kdf_salt = ? WHERE id = ?`,
		hashB64, salt, backupID)
	return err
}

// ----------------- Password & Dashlane helpers -------------------

func generateStrongPassword() (string, error) {
	// 32 random bytes, base64-url encoded (~43 chars)
	buf := make([]byte, 32)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func printDashlaneNote(archivePath, profile, password, hint string) {
	fmt.Println()
	fmt.Println("----- DASHLANE SECURE NOTE (copy into Dashlane) -----")
	fmt.Println("Title: Backup password -", filepath.Base(archivePath))
	fmt.Println("Category: Backup")
	fmt.Println()
	fmt.Println("Archive:", archivePath)
	fmt.Println("Profile:", profile)
	fmt.Println("Created:", time.Now().Format(time.RFC3339))
	fmt.Println()
	fmt.Println("Password:", password)
	if hint != "" {
		fmt.Println("Hint:", hint)
	}
	fmt.Println("-----------------------------------------------------")
	fmt.Println("Paste the above into a new Secure Note in Dashlane.")
}

// ----------------- Backup pipeline -------------------

func runBackup(
	ctx context.Context,
	srcDir, stageDir, dataDir string,
	db *sql.DB,
	backupID int64,
	workers int,
	profile string,
) error {
	jobs := make(chan fileJob, workers*2)
	results := make(chan fileResult, workers*2)
	var wg sync.WaitGroup

	var contentMap sync.Map // hash -> struct{} for dedupe

	if workers <= 0 {
		workers = 1
	}

	// Worker goroutines
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for job := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				res := processFileJob(job, dataDir, &contentMap)
				results <- res
			}
		}(i)
	}

	// DB writer goroutine
	dbErrCh := make(chan error, 1)
	go func() {
		dbErrCh <- writeResultsToDB(db, backupID, results)
	}()

	// Walk filesystem

	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Ignore permission-related errors and keep walking.
			// On Windows, "Access is denied." often comes through here.
			if errors.Is(err, os.ErrPermission) {
				fmt.Fprintf(os.Stderr, "Skipping (no permission): %s\n", path)
				return nil
			}
			if pe, ok := err.(*os.PathError); ok {
				msg := strings.ToLower(pe.Err.Error())
				if strings.Contains(msg, "access is denied") {
					fmt.Fprintf(os.Stderr, "Skipping (access denied): %s\n", path)
					return nil
				}
			}
			// Anything else is a real error.
			return err
		}

		// JetBrains profile: skip heavy/cache dirs early
		if profile == ProfileJetBrains && d.IsDir() {
			if _, skip := jbExcludeDirNames[d.Name()]; skip {
				return fs.SkipDir
			}
		}

		if d.IsDir() {
			return nil
		}

		if !shouldBackup(path, profile) {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		job := fileJob{
			AbsPath: path,
			RelPath: rel,
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case jobs <- job:
			// just enqueued
		}

		return nil
	})

	// Close jobs and wait for workers
	close(jobs)
	wg.Wait()
	close(results)

	if walkErr != nil && !errors.Is(walkErr, fs.SkipDir) {
		return fmt.Errorf("walk error: %w", walkErr)
	}

	if err := <-dbErrCh; err != nil {
		fmt.Fprintf(os.Stderr, "DB writer error: %v\n", err)
		return fmt.Errorf("db writer error: %w", err)
	}

	return nil
}

func shouldBackup(path string, profile string) bool {
	switch profile {

	case ProfileDocuments:
		ext := strings.ToLower(filepath.Ext(path))
		_, ok := documentExts[ext]
		return ok

	case ProfileDatabases:
		ext := strings.ToLower(filepath.Ext(path))
		_, ok := databaseExts[ext]
		return ok

	case ProfilePhotos:
		ext := strings.ToLower(filepath.Ext(path))
		_, ok := photoExts[ext]
		return ok

	case ProfileJetBrains:
		// We already skip undesired dirs in WalkDir.
		base := filepath.Base(path)
		if _, skip := jbExcludeFileNames[base]; skip {
			return false
		}
		return true
	}

	// default if profile is somehow unknown
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
	isNewContent := !loaded

	if isNewContent {
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
		IsNewContent: isNewContent,
		Err:          nil,
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

	sum := h.Sum(nil)
	return hex.EncodeToString(sum), nil
}

func copyFileToBlob(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		// already exists
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
func writeResultsToDB(db *sql.DB, backupID int64, results <-chan fileResult) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	insertContent, err := tx.Prepare(`INSERT OR IGNORE INTO contents (hash, size_bytes) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertContent.Close()

	insertFile, err := tx.Prepare(`INSERT INTO files (backup_id, rel_path, size_bytes, mod_time_utc, content_hash) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertFile.Close()

	var (
		count    int64
		lastTime = time.Now()
		firstErr error // only for DB-level errors
	)

	for res := range results {
		// 🟡 File-level error (stat/hash/copy/etc.): log and skip, but KEEP DRAINING.
		if res.Err != nil {
			// res.AbsPath is available from processFileJob
			fmt.Fprintf(os.Stderr, "Skipping file due to processing error: %s (%v)\n", res.AbsPath, res.Err)
			continue
		}

		// 🔴 DB-level error: record once, but still drain remaining results.
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
			fmt.Printf("Processed %d files...\n", count)
			lastTime = time.Now()
		}
	}

	// If there was a DB error, abort the transaction (via Rollback defer) and bubble it up.
	if firstErr != nil {
		return firstErr
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	fmt.Printf("Finished writing %d file records to DB\n", count)
	return nil
}

// ----------------- 7z integration -------------------

func create7zArchive(dstDir, stageFolderName, archivePath, password string) error {
	if _, err := exec.LookPath("7z"); err != nil {
		return fmt.Errorf("7z not found on PATH: %w", err)
	}

	args := []string{
		"a",
		"-t7z",
		"-p" + password,
		"-mhe=on", // encrypt headers
		archivePath,
		stageFolderName,
	}

	cmd := exec.Command("7z", args...)
	cmd.Dir = dstDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Running: 7z %s (in %s)\n", strings.Join(args, " "), dstDir)
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}
