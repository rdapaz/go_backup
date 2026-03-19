//go:build !windows

package core

import "syscall"

// FreeDiskSpace returns the free disk space for the filesystem containing the given path.
func FreeDiskSpace(path string) (*DiskUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}
	return &DiskUsage{
		Free:  stat.Bavail * uint64(stat.Bsize),
		Total: stat.Blocks * uint64(stat.Bsize),
	}, nil
}
