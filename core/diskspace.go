package core

import (
	"fmt"
	"os"
	"path/filepath"
)

// DiskUsage holds free and total disk space in bytes.
type DiskUsage struct {
	Free  uint64
	Total uint64
}

// EstimateSourceSize walks the source directory and returns the total size
// of files that would be backed up (respecting profile and blocklist).
// This is a quick estimate — actual archive size will be smaller due to
// compression and deduplication, but it's a good upper bound for disk space.
func EstimateSourceSize(srcDir, profile string, blocklist []string) (uint64, int64, error) {
	srcAbs, err := filepath.Abs(srcDir)
	if err != nil {
		return 0, 0, err
	}

	blockSet := make(map[string]struct{}, len(blocklist))
	for _, b := range blocklist {
		blockSet[b] = struct{}{}
	}

	var totalSize uint64
	var fileCount int64

	err = filepath.WalkDir(srcAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			if _, blocked := blockSet[d.Name()]; blocked {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if !ShouldBackup(path, profile) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		totalSize += uint64(info.Size())
		fileCount++
		return nil
	})

	return totalSize, fileCount, err
}

// FormatBytes returns a human-readable size string.
func FormatBytes(b uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
