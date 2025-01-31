/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package device

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

var (
	scanLock = &sync.Mutex{}
)

type LinuxDevice interface {
	// GetDevice() bool
	DeleteDevice() (err error)
	CreateDevice() (err error)
	GetMapper() string
	Populate(bool) error
}

// Device struct.
type Device struct {
	Mapper string `json:"mapper,omitempty"`
	WWN    string `json:"wwn,omitempty"`
	Slaves int    `json:"slaves,omitempty"`
}

// NewLinuxDevice new device with given wwn.
func NewLinuxDevice(wwn string) LinuxDevice {
	return &Device{
		WWN: wwn,
	}
}

func (d *Device) GetMapper() string {
	return d.Mapper
}

// Populate get all linux Devices.
func (d *Device) Populate(needActivePath bool) error {
	args := []string{"ls", "--target", "multipath"}
	outBytes, err := exec.Command(dmsetupcommand, args...).CombinedOutput()
	out := string(outBytes)
	if err != nil {
		return fmt.Errorf("failed to retrieve multipath devices: %s", out)
	}

	r, err := regexp.Compile(majorMinorPattern)
	if err != nil {
		return fmt.Errorf("unable to compile regex with %s", majorMinorPattern)
	}
	listOut := r.FindAllString(out, -1)

	for _, line := range listOut {
		result := findStringSubmatchMap(line, r)

		tmpPathname := "dm-" + result["Minor"]
		uuid, err := getUUID(tmpPathname)
		if err != nil {
			klog.Warning(err)
			continue
		}
		mapName, err := getMpathName(tmpPathname)
		if err != nil {
			return err
		}

		tmpWWID := strings.TrimPrefix(uuid, "mpath-")
		tmpWWN := tmpWWID[1:] // truncate scsi-id prefix

		// get the active count; if 0 then cleanup the dm
		slavesCount, err := getPathsCount(mapName)
		if err != nil {
			return fmt.Errorf("unable to count slaves for device %s: %v", d.WWN, err)
		}

		if slavesCount == 0 {
			klog.Warningf("cleaning mapper %s as no active disks present", mapName)
			_ = multipathRemoveDmDevice(mapName)
			// even if wwn matches but no slaves lets skip
		} else if strings.EqualFold(d.WWN, tmpWWN) {
			// if atleast 1 active slave present then use it
			d.Mapper = "/dev/mapper/" + mapName
			d.Slaves = slavesCount
			break
		}
	}

	return nil
}

// DeleteDevice delete the multipath device.
func (d *Device) DeleteDevice() (err error) {
	if err := retryCleanupDevice(d); err != nil {
		klog.Warningf("error while deleting multipath device %s: %v", d.Mapper, err)
		return err
	}
	d.Mapper = ""
	d.Slaves = 0
	return nil
}

// CreateDevice attach and create linux devices to host.
func (d *Device) CreateDevice() (err error) {
	if err = d.createLinuxDevice(); err != nil {
		klog.Errorf("unable to create device for wwn %s", d.WWN)
		return err
	}

	if d.Mapper == "" {
		return fmt.Errorf("unable to find the device for wwn %s", d.WWN)
	}
	return nil
}

// scsiHostRescanWithLock: scans all scsi hosts with locks
// This is calling scsiHostRescan
// which works in a way that only 1 scan will run at a time
// and other requests will wait till the scan is complete
// but will not scan again as it is already scanned.
func scsiHostRescanWithLock() (err error) {
	start := time.Now()
	var scan bool = true

	for {
		if scanLock.TryLock() {
			func() {
				defer scanLock.Unlock()
				if scan {
					// always clean orphan paths before scanning hosts
					cleanupOrphanPaths()
					err = scsiHostRescan()
				}
			}()
			return
		} else {
			if time.Since(start) > time.Minute {
				// Scanning usually takes < 30s. If wait is more than a min then return.
				return
			}

			// Already locked, wait for it to complete and don't scan again.
			scan = false
			time.Sleep(5 * time.Second)
		}
	}
}

// createLinuxDevice: attaches and creates a new linux device
// Try device discovery; retry every 5 sec if no device found or have 0 slaves
// In between checks we will try to cleanup:
// a. faulty and orphan paths (quick)
// b. stale paths (time consuming)
// Since there can be parallel requests for device discovery we are not
// allowing cleanup process to run for each everytime.
// For 'a' we will not cleanup if it was already run within last 10 secs.
// For 'b' we will not cleanup if it was already run within last 25 secs.
func (d *Device) createLinuxDevice() (err error) {
	// Start a Countdown ticker
	for i := 0; i <= 10; i++ {
		if err = scsiHostRescanWithLock(); err != nil {
			return err
		}
		// wait for device to appear after rescan
		time.Sleep(time.Second * 1)

		err := d.Populate(true)
		if err != nil {
			return err
		}
		if d.Slaves > 0 {
			// populated device with atleast 1 slave; job done
			return nil
		}

		// some resting time
		time.Sleep(time.Second * 5)
	}

	// Reached here signifies the device was not found, throw an error
	return fmt.Errorf("fc device not found for wwn %s", d.WWN)
}

// scsiHostRescan: scans all scsi hosts.
func scsiHostRescan() error {
	scsiPath := "/sys/class/scsi_host/"
	dirs, err := os.ReadDir(scsiPath)
	if err != nil {
		return err
	}
	for _, f := range dirs {
		name := filepath.Clean(scsiPath + f.Name() + "/scan")
		data := []byte("- - -")
		if err := os.WriteFile(name, data, 0666); err != nil {
			return fmt.Errorf("scsi host rescan failed: %v", err)
		}
	}

	return nil
}

func GetDeviceWWN(pathName string) (wwn string, err error) {
	if strings.HasPrefix(pathName, "/dev/mapper/") {
		// get dm path
		pathName, err = filepath.EvalSymlinks(pathName)
		if err != nil {
			return "", err
		}
	}
	pathName = strings.TrimPrefix(pathName, "/dev/")

	uuid, err := getUUID(pathName)

	tmpWWID := strings.TrimPrefix(uuid, "mpath-")
	wwn = tmpWWID[1:] // truncate scsi-id prefix

	return wwn, err
}
