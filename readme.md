readme.md
---------

# Go Backup Tool

Parallelized encrypted backup tool with:
- SHA-256 deduplication
- 7z AES-256 encryption
- SQLite metadata
- Profiles: documents, jetbrains, databases, photos
- Windows/macOS/Linux compatible

## Usage

.\backup.exe -src "C:\Users\RicardodaPaz\Pictures" -dst "D:\Backups" -profile photos -hint "All photos Jan 2026"


C:\Users\<YourUser>\Pictures
C:\Users\<YourUser>\OneDrive\Pictures
C:\Users\<YourUser>\Downloads
C:\Users\<YourUser>\Desktop
C:\Users\<YourUser>\iCloudPhotos
C:\Users\<YourUser>\Google Photos


backup.exe -src C:\Users\RicardodaPaz\Pictures   -profile photos
backup.exe -src C:\Users\RicardodaPaz\Downloads  -profile photos
backup.exe -src C:\Users\RicardodaPaz\Desktop    -profile photos

