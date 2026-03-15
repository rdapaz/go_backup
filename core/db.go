package core

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
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

func InitDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func CreateBackupRecord(db *sql.DB, archiveName, profile, passwordHint string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(`INSERT INTO backups (archive_name, created_at, profile, password_hint) VALUES (?, ?, ?, ?)`,
		archiveName, now, profile, passwordHint)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func StoreBackupPasswordHash(db *sql.DB, backupID int64, password string) error {
	hashB64, salt, err := DerivePasswordHash(password)
	if err != nil {
		return err
	}

	_, err = db.Exec(`UPDATE backups SET password_hash = ?, kdf_salt = ? WHERE id = ?`,
		hashB64, salt, backupID)
	return err
}

// BackupRecord represents a row from the backups table.
type BackupRecord struct {
	ID           int64
	ArchiveName  string
	CreatedAt    string
	Profile      string
	PasswordHint string
}

// FileRecord represents a row from the files table.
type FileRecord struct {
	RelPath     string
	ContentHash string
	SizeBytes   int64
}

func LoadBackupRecord(db *sql.DB) (BackupRecord, error) {
	var b BackupRecord
	row := db.QueryRow(`SELECT id, archive_name, profile, created_at, COALESCE(password_hint,'') FROM backups ORDER BY id LIMIT 1`)
	err := row.Scan(&b.ID, &b.ArchiveName, &b.Profile, &b.CreatedAt, &b.PasswordHint)
	return b, err
}

func LoadFileRecords(db *sql.DB, backupID int64) ([]FileRecord, error) {
	rows, err := db.Query(`SELECT rel_path, content_hash, size_bytes FROM files WHERE backup_id = ?`, backupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []FileRecord
	for rows.Next() {
		var r FileRecord
		if err := rows.Scan(&r.RelPath, &r.ContentHash, &r.SizeBytes); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}
