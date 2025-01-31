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

package remote

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM/go-sdk-core/v5/core"

	"k8s.io/klog/v2"
)

func runRemoteCommand(publicIP string, arg ...string) (string, error) {
	args := []string{"-i", sshDefaultKey, "-o", "StrictHostKeyChecking no", fmt.Sprintf("%s@%s", sshUser, publicIP), "--"}
	args = append(args, arg...)

	// Should we print?; May contain sensitive information
	klog.Infof("Executing SSH command: ssh %v", args)
	return runCommand("ssh", args...)
}

func runCommand(cmd string, args ...string) (string, error) {
	output, err := exec.Command(cmd, args...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command [%s %s] failed with error: %v", cmd, strings.Join(args, " "), err.Error())
	}
	return string(output), err
}

func getEnvVar(envName string) (string, error) {
	ret := os.Getenv(envName)
	if ret == "" {
		return "", fmt.Errorf("missing %s env variable", envName)
	}
	return ret, nil
}

func getIBMPISession() (*ibmpisession.IBMPISession, error) {
	var err error
	var apiKey, accountID, zone string

	if apiKey, err = getEnvVar("IBMCLOUD_API_KEY"); err != nil {
		return nil, err
	}
	if accountID, err = getEnvVar("IBMCLOUD_ACCOUNT_ID"); err != nil {
		return nil, err
	}
	if zone, err = getEnvVar("POWERVS_ZONE"); err != nil {
		return nil, err
	}

	o := &ibmpisession.IBMPIOptions{
		Authenticator: &core.IamAuthenticator{ApiKey: apiKey, URL: os.Getenv("IBMCLOUD_IAM_API_ENDPOINT")},
		UserAccount:   accountID,
		Zone:          zone,
		Debug:         true,
	}

	return ibmpisession.NewIBMPISession(o)
}

func getPiID() (string, error) {
	return getEnvVar("POWERVS_CLOUD_INSTANCE_ID")
}

func findAndKillProcess(sshPID int) error {
	proc, err := os.FindProcess(sshPID)
	if err != nil {
		return fmt.Errorf("unable to find ssh tunnel process %v: %v", sshPID, err)
	}
	if err = proc.Kill(); err != nil {
		return fmt.Errorf("failed to kill ssh tunnel process %v: %v", sshPID, err)
	}
	return nil
}
