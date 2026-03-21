package core

const (
	ProfileDocuments = "documents"
	ProfileJetBrains = "jetbrains"
	ProfileDatabases = "databases"
	ProfilePhotos    = "photos"
	ProfileEbooks    = "ebooks"
	ProfileAll       = "all"
)

var ValidProfiles = []string{ProfileAll, ProfileDocuments, ProfileJetBrains, ProfileDatabases, ProfilePhotos, ProfileEbooks}

var DocumentExts = map[string]struct{}{
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

var DatabaseExts = map[string]struct{}{
	".sqlite":  {},
	".sqlite3": {},
	".db":      {},
	".mdb":     {},
	".accdb":   {},
}

var PhotoExts = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".tif":  {},
	".tiff": {},
	".bmp":  {},
	".gif":  {},
	".heic": {},
	".heif": {},
	".webp": {},
	".raw":  {},
	".cr2":  {},
	".nef":  {},
	".arw":  {},
	".dng":  {},
	".svg":  {},
}

var EbookExts = map[string]struct{}{
	".epub": {},
	".mobi": {},
	".azw":  {},
	".azw3": {},
	".pdf":  {},
	".cbz":  {},
	".cbr":  {},
}

var JBExcludeDirNames = map[string]struct{}{
	".git":         {},
	".svn":         {},
	".hg":          {},
	".idea":        {},
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

var JBExcludeFileNames = map[string]struct{}{
	"workspace.xml": {},
}

func IsValidProfile(p string) bool {
	switch p {
	case ProfileDocuments, ProfileJetBrains, ProfileDatabases, ProfilePhotos, ProfileEbooks, ProfileAll:
		return true
	}
	return false
}

// DefaultBlocklistDirs contains directory names that are skipped by default
// during backup scans. These are common Windows system/app directories inside
// user profiles that contain images, documents etc. that are not user content.
var DefaultBlocklistDirs = []string{
	// Windows system/app directories
	"AppData",
	"Application Data",
	"Local Settings",
	"$Recycle.Bin",
	"System Volume Information",
	"Cookies",
	"NetHood",
	"PrintHood",
	"Recent",
	"SendTo",
	"Start Menu",
	"Templates",
	"MicrosoftEdgeBackups",
	"Thumbs.db",
	// Version control
	".git",
	".svn",
	".hg",
	// Python
	".venv",
	"venv",
	"env",
	"__pycache__",
	".tox",
	".mypy_cache",
	".pytest_cache",
	".eggs",
	// Node / JS
	"node_modules",
	".npm",
	// Go
	"vendor",
	// .NET
	"bin",
	"obj",
	// JVM
	".gradle",
	".m2",
	"target",
	"build",
	// IDE / editor
	".vscode",
	".idea",
	// General caches & config
	".cache",
	".config",
	".local",
	".nuget",
	".dotnet",
	".thumbnails",
	".pants.d",
	"dist",
}
