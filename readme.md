# Go Backup

Parallelized encrypted backup tool with SHA-256 deduplication, AES-256 7z encryption, and SQLite metadata tracking.

## Features

- **Profile-based filtering**: documents, jetbrains, databases, photos
- **Content-addressed deduplication**: identical files stored once (SHA-256)
- **AES-256 encryption**: 7z archives with encrypted headers
- **Scrypt key derivation**: brute-force resistant password hashing
- **Parallel processing**: configurable worker goroutines
- **Atomic file operations**: crash-safe blob writes
- **GUI and CLI**: Fyne desktop app or command-line interface

## Project Structure

```
go_backup/
├── core/               # Reusable backup/restore library
│   ├── backup.go       # RunBackup pipeline
│   ├── restore.go      # RunRestore pipeline
│   ├── db.go           # SQLite schema and queries
│   ├── profiles.go     # Profile definitions and file filters
│   └── crypto.go       # Password generation and scrypt hashing
├── cmd/
│   ├── cli/main.go     # CLI entry point (backup/restore subcommands)
│   └── gui/main.go     # Fyne GUI application
└── go.mod
```

## Prerequisites

- **Go 1.20+**
- **7z** on PATH (e.g. 7-Zip installed)
- **CGo** enabled (required by SQLite driver and Fyne)

## Building

```bash
# CLI
go build -o backup-cli.exe ./cmd/cli/

# GUI
go build -o backup-gui.exe ./cmd/gui/
```

## Usage

### GUI

Launch `backup-gui.exe`. The app has two tabs:

- **Backup**: select source/destination folders, profile, password (auto-generated if empty), and start
- **Restore**: choose archive or staging directory, provide password, select destination, and restore

### CLI — Backup

```bash
backup-cli backup -src <sourceDir> -dst <destDir> -profile <profile> [options]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-src` | (required) | Source directory to back up |
| `-dst` | (required) | Destination for the archive |
| `-profile` | `documents` | `documents`, `jetbrains`, `databases`, or `photos` |
| `-password` | (auto-generated) | Password for the 7z archive |
| `-hint` | | Optional password hint |
| `-workers` | NumCPU | Parallel worker count |
| `-keep-stage` | `false` | Keep the staging directory after archiving |

**Examples:**

```bash
backup-cli backup -src "C:\Users\You\Pictures" -dst "D:\Backups" -profile photos -hint "All photos Mar 2026"
backup-cli backup -src "C:\Projects" -dst "D:\Backups" -profile jetbrains
backup-cli backup -src "C:\Users\You\Documents" -dst "D:\Backups" -profile documents
```

### CLI — Restore

```bash
backup-cli restore -dst <restoreDir> -archive <backup.7z> -password <pwd>
backup-cli restore -dst <restoreDir> -stage <stagingDir>
```

| Flag | Description |
|------|-------------|
| `-dst` | (required) Destination to restore files into |
| `-archive` | Path to .7z backup archive |
| `-password` | Password for the archive |
| `-stage` | Path to an already-extracted staging directory |
| `-workers` | Parallel worker count (default: NumCPU) |

## Profiles

| Profile | Included Extensions |
|---------|-------------------|
| `documents` | .doc, .docx, .xls, .xlsx, .ppt, .pptx, .vsd, .vsdx, .mpp, .pdf |
| `databases` | .sqlite, .sqlite3, .db, .mdb, .accdb |
| `photos` | .jpg, .jpeg, .png, .tif, .tiff, .bmp, .gif, .heic, .heif |
| `jetbrains` | All files except excluded dirs (.git, node_modules, .venv, etc.) and workspace.xml |

## Archive Format

Each backup produces an encrypted 7z archive containing:

```
<timestamp>_<suffix>/
├── backup.db           # SQLite metadata (backup record, file manifest, content hashes)
└── data/
    ├── <sha256hash1>   # Deduplicated content blobs
    ├── <sha256hash2>
    └── ...
```
