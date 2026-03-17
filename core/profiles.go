package core

const (
	ProfileDocuments = "documents"
	ProfileJetBrains = "jetbrains"
	ProfileDatabases = "databases"
	ProfilePhotos    = "photos"
	ProfileAll       = "all"
)

var ValidProfiles = []string{ProfileAll, ProfileDocuments, ProfileJetBrains, ProfileDatabases, ProfilePhotos}

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
	case ProfileDocuments, ProfileJetBrains, ProfileDatabases, ProfilePhotos, ProfileAll:
		return true
	}
	return false
}

// DefaultBlocklistDirs contains directory names that are skipped by default
// during backup scans. These are common Windows system/app directories inside
// user profiles that contain images, documents etc. that are not user content.
var DefaultBlocklistDirs = []string{
	"AppData",
	"Application Data",
	"Local Settings",
	"$Recycle.Bin",
	"System Volume Information",
	".cache",
	".vscode",
	".config",
	".local",
	".npm",
	".nuget",
	".dotnet",
	".gradle",
	".m2",
	"node_modules",
	"__pycache__",
	".git",
	".svn",
	".hg",
	"Cookies",
	"NetHood",
	"PrintHood",
	"Recent",
	"SendTo",
	"Start Menu",
	"Templates",
	"MicrosoftEdgeBackups",
	".thumbnails",
	"Thumbs.db",
}
