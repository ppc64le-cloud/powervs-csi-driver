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
	"bufio"
	"fmt"
	"os"
	"regexp"
)

// findStringSubmatchMap: find and build  the map of named groups.
func findStringSubmatchMap(s string, r *regexp.Regexp) map[string]string {
	captures := make(map[string]string)
	match := r.FindStringSubmatch(s)
	if match == nil {
		return captures
	}
	for i, name := range r.SubexpNames() {
		if i != 0 {
			captures[name] = match[i]
		}
	}
	return captures
}

// readFirstLine: read the file line no. 1.
func readFirstLine(filePath string) (string, error) {
	file, err := os.Open(filePath)
	a := ""
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		a = scanner.Text()
		break
	}
	if err = scanner.Err(); err != nil {
		return "", err
	}
	return a, err
}

func getMpathName(pathname string) (string, error) {
	fileName := fmt.Sprintf("/sys/block/%s/dm/name", pathname)
	return readFirstLine(fileName)
}

func getUUID(pathname string) (string, error) {
	fileName := fmt.Sprintf("/sys/block/%s/dm/uuid", pathname)
	return readFirstLine(fileName)
}

// deleteSdDevice: delete the scsi device by writing "1".
func deleteSdDevice(deletePath string) (err error) {
	// deletePath for deleting the device
	err = os.WriteFile(deletePath, []byte("1"), 0644)
	if err != nil {
		err = fmt.Errorf("error writing to file %s: %v", deletePath, err)
		return err
	}
	return nil
}
