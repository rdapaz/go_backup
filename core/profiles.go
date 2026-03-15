package core

const (
	ProfileDocuments = "documents"
	ProfileJetBrains = "jetbrains"
	ProfileDatabases = "databases"
	ProfilePhotos    = "photos"
)

var ValidProfiles = []string{ProfileDocuments, ProfileJetBrains, ProfileDatabases, ProfilePhotos}

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
	case ProfileDocuments, ProfileJetBrains, ProfileDatabases, ProfilePhotos:
		return true
	}
	return false
}
