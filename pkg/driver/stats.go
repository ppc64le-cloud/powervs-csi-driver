package driver

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"k8s.io/kubernetes/pkg/volume/util/fs"
)

// StatsUtils.
type StatsUtils interface {
	FSInfo(path string) (int64, int64, int64, int64, int64, int64, error)
	IsBlockDevice(devicePath string) (bool, error)
	DeviceInfo(devicePath string) (int64, error)
	IsPathNotExist(path string) bool
}

// VolumeStatUtils.
type VolumeStatUtils struct {
}

// IsPathNotExist returns true if a particular path does not exists.
func (su *VolumeStatUtils) IsPathNotExist(path string) bool {
	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if err != nil {
		if os.IsNotExist(err) {
			return true
		}
	}
	return false
}

// IsBlockDevice returns true if the path provided is a block device.
func (su *VolumeStatUtils) IsBlockDevice(devicePath string) (bool, error) {
	var stat unix.Stat_t
	err := unix.Stat(devicePath, &stat)
	if err != nil {
		return false, err
	}

	return (stat.Mode & unix.S_IFMT) == unix.S_IFBLK, nil
}

// DeviceInfo returns the size of the block device in bytes.
func (su *VolumeStatUtils) DeviceInfo(devicePath string) (int64, error) {
	output, err := exec.Command("blockdev", "--getsize64", devicePath).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get size of block volume at path %s: output: %s, err: %v", devicePath, string(output), err)
	}
	strOut := strings.TrimSpace(string(output))
	gotSizeBytes, err := strconv.ParseInt(strOut, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse size '%s' into int", strOut)
	}

	return gotSizeBytes, nil
}

// FSInfo returns the information related to the FS.
func (su *VolumeStatUtils) FSInfo(path string) (int64, int64, int64, int64, int64, int64, error) {
	return fs.Info(path)
}
