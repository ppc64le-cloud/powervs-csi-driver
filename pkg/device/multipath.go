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
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

const (
	multipathd           = "multipathd"
	dmsetupcommand       = "dmsetup"
	majorMinorPattern    = "(.*)\\((?P<Major>\\d+),\\s+(?P<Minor>\\d+)\\)"
	orphanPathsPattern   = ".*\\s+(?P<host>\\d+):(?P<channel>\\d+):(?P<target>\\d+):(?P<lun>\\d+).*orphan"
	deviceDoesNotExist   = "No such device or address"
	scsiDeviceDeletePath = "/sys/class/scsi_device/%s:%s:%s:%s/device/delete"
)

var (
	showPathsFormat  = []string{"show", "paths", "raw", "format", "%w %d %t %i %o %T %z %s %m"}
	orphanPathRegexp = regexp.MustCompile(orphanPathsPattern)
)

// getPathsCount get number of slaves for a given device.
func getPathsCount(mapper string) (count int, err error) {
	// TODO: This can be achieved reading the full line processing instead of piped command
	statusCmd := fmt.Sprintf("dmsetup status --target multipath %s | awk 'BEGIN{RS=\" \";active=0}/[0-9]+:[0-9]+/{dev=1}/A/{if (dev == 1) active++; dev=0} END{ print active }'", mapper)

	outBytes, err := exec.Command("bash", "-c", statusCmd).CombinedOutput()
	out := strings.TrimSuffix(string(outBytes), "\n")
	if err != nil || isDmsetupStatusError(out) {
		return 0, fmt.Errorf("error while running dmsetup status command: %s : %v", out, err)
	}
	return strconv.Atoi(out)
}

// isDmsetupStatusError check for command failure or empty stdout msg.
func isDmsetupStatusError(msg string) bool {
	return msg == "" || strings.Contains(msg, "Command failed")
}

// isMultipathTimeoutError check for timeout or similar error msg.
func isMultipathTimeoutError(msg string) bool {
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "receiving packet")
}

// retryCleanupDevice retry for maxtries for device Cleanup.
func retryCleanupDevice(dev *Device) error {
	maxTries := 10
	var err error
	for try := 0; try < maxTries; try++ {
		err = multipathRemoveDmDevice(dev.Mapper)
		if err == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return err
}

// multipathDisableQueuing disable queueing on the multipath device.
func multipathDisableQueuing(mapper string) error {
	args := []string{"message", mapper, "0", "fail_if_no_path"}
	outBytes, err := exec.Command(dmsetupcommand, args...).CombinedOutput()
	out := string(outBytes)
	if err != nil {
		return fmt.Errorf("cannot disable queuing: %s : %v", out, err)
	}
	if strings.Contains(out, deviceDoesNotExist) {
		return fmt.Errorf("cannot disable queuing: %s", out)
	}
	return nil
}

// multipathRemoveDmDevice remove multipath device via dmsetup.
func multipathRemoveDmDevice(mapper string) error {
	if strings.HasSuffix(mapper, "mpatha") {
		klog.Warning("skipping remove mpatha which is root")
		return nil
	}

	// Any failure while performing disable queuing operation on the device map
	// will be ignored and will try to delete it.
	if err := multipathDisableQueuing(mapper); err != nil {
		klog.Warningf("failure while disabling queue for %s: %v", mapper, err)
	}

	args := []string{"remove", "--force", mapper}
	outBytes, err := exec.Command(dmsetupcommand, args...).CombinedOutput()
	out := string(outBytes)
	if err != nil {
		return fmt.Errorf("failed to remove device map for %s : %s : %v", mapper, out, err)
	}
	if isDmsetupRemoveError(out) {
		return fmt.Errorf("failed to remove device map for %s : %s", mapper, out)
	}
	return nil
}

// isDmsetupRemoveError check if dmsetup remove command did not return empty, "ok" or no device msg.
func isDmsetupRemoveError(msg string) bool {
	return msg != "" && !strings.Contains(msg, "ok") && !strings.Contains(msg, deviceDoesNotExist)
}

// cleanupOrphanPaths find orphan paths and remove them (best effort).
func cleanupOrphanPaths() {
	// run multipathd show paths and fetch orphan maps
	outBytes, err := exec.Command(multipathd, showPathsFormat...).CombinedOutput()
	if err != nil {
		klog.Warningf("failed to run multipathd %v, err: %s", showPathsFormat, err)
		return
	}
	out := string(outBytes)
	// rc can be 0 on the below error conditions as well
	if isMultipathTimeoutError(out) {
		klog.Warningf("failed to get multipathd %v, out %s", showPathsFormat, out)
		return
	}

	listOrphanPaths := orphanPathRegexp.FindAllString(out, -1)
	for _, orphanPath := range listOrphanPaths {
		result := findStringSubmatchMap(orphanPath, orphanPathRegexp)
		deletePath := fmt.Sprintf(scsiDeviceDeletePath, result["host"], result["channel"], result["target"], result["lun"])
		if err := deleteSdDevice(deletePath); err != nil {
			// ignore errors as its a best effort to cleanup all orphan maps
			klog.Warningf("error while deleting device: %v", err)
		}
	}
}
