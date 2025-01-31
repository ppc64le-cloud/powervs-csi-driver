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
	"path/filepath"
	"strings"
	"time"

	"github.com/IBM-Cloud/power-go-client/clients/instance"

	"k8s.io/klog/v2"
)

const (
	osLinux         = "linux"
	arch            = "ppc64le"
	binName         = "ibm-powervs-block-csi-driver"
	binPath         = "bin/" + binName
	tarName         = "powervs-csi-driver-binary.tar.gz"
	timestampFormat = "20060102T150405"
	sshDefaultKey   = "/tmp/id_rsa"
	sshUser         = "root"
	outputFile      = "prog.out"
)

type Remote struct {
	resName        string
	publicIP       string
	sshPID         int
	tarPath        string
	remoteDir      string
	powervsClients PowerVSClients
}

type PowerVSClients struct {
	ic   *instance.IBMPIInstanceClient
	imgc *instance.IBMPIImageClient
	nc   *instance.IBMPINetworkClient
	kc   *instance.IBMPIKeyClient
}

func NewRemote() *Remote {
	return &Remote{
		resName: "csi-test-" + time.Now().Format(timestampFormat),
	}
}

func (r *Remote) SetupNewDriver(endpoint string) (err error) {
	if err = r.createDriverArchive(); err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(r.tarPath); err != nil {
			klog.Warningf("failed to remove archive file %s: %v", r.tarPath, err)
		}
	}()

	if err = r.createPVSResources(); err != nil {
		return err
	}

	if err = r.uploadAndRun(endpoint); err != nil {
		return err
	}

	if err = r.createSSHTunnel(endpoint); err != nil {
		return fmt.Errorf("SSH Tunnel pid %v encountered error: %v", r.sshPID, err.Error())
	}

	return nil
}

// Create binary and archive it.
func (r *Remote) createDriverArchive() (err error) {
	tarDir, err := os.MkdirTemp("", "powervscsidriver")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory %v", err)
	}
	defer os.RemoveAll(tarDir)

	err = setupBinariesLocally(tarDir)
	if err != nil {
		return fmt.Errorf("failed to setup test package %s: %v", tarDir, err)
	}

	// Fetch tar path at current dir
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory %v", err)
	}
	r.tarPath = filepath.Join(dir, tarName)

	return nil
}

func setupBinariesLocally(tarDir string) error {
	cmd := exec.Command("make", "GOOS="+osLinux, "GOARCH="+arch, "driver")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run make driver: %s: %v", string(out), err)
	}

	// Copy binaries
	bPath := "../../" + binPath
	if _, err := os.Stat(bPath); err != nil {
		return fmt.Errorf("failed to locate test binary %s: %v", binPath, err)
	}
	out, err = exec.Command("cp", bPath, tarDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to copy %s: %s: %v", bPath, string(out), err)
	}

	// Build the tar
	out, err = exec.Command("tar", "-zcvf", tarName, "-C", tarDir, ".").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to build tar: %s: %v", string(out), err)
	}

	return nil
}

// Upload archive to instance and run binaries.
// TODO: grep pid of driver and kill it later.
// TODO: use random port.
// May be track the job id if given.
func (r *Remote) uploadAndRun(endpoint string) error {
	r.remoteDir = filepath.Join("/tmp", "powervs-csi-"+time.Now().Format(timestampFormat))

	klog.Infof("Staging test on %s", r.remoteDir)
	if output, err := r.runSSHCommand("mkdir", r.remoteDir); err != nil {
		return fmt.Errorf("failed to create remote directory %s on the instance: %s: %v", r.remoteDir, output, err)
	}

	klog.Infof("Copying test archive %s to %s:%s/", r.tarPath, r.publicIP, r.remoteDir)
	if output, err := runCommand("scp", "-i", sshDefaultKey, r.tarPath, fmt.Sprintf("%s@%s:%s/", sshUser, r.publicIP, r.remoteDir)); err != nil {
		return fmt.Errorf("failed to copy test archive: %s: %v", output, err)
	}

	klog.Infof("Extract test archive at %s:%s/", r.publicIP, r.remoteDir)
	extractCmd := fmt.Sprintf("'cd %s && tar -xzvf ./%s'", r.remoteDir, tarName)
	if output, err := r.runSSHCommand("sh", "-c", extractCmd); err != nil {
		return fmt.Errorf("failed to extract test archive: %s: %v", output, err)
	}

	klog.Infof("Run the driver on %s", r.publicIP)
	// TODO: env file
	exportStr := fmt.Sprintf("export IBMCLOUD_API_KEY=%s;", os.Getenv("IBMCLOUD_API_KEY"))
	exportStr += fmt.Sprintf("export POWERVS_CLOUD_INSTANCE_ID=%s;", os.Getenv("POWERVS_CLOUD_INSTANCE_ID"))
	exportStr += fmt.Sprintf("export POWERVS_ZONE=%s;", os.Getenv("POWERVS_ZONE"))
	exportStr += fmt.Sprintf("export POWERVS_INSTANCE_ID=%s;", os.Getenv("POWERVS_INSTANCE_ID"))

	// Below vars needed for staging env
	if iamEndpoint, err := getEnvVar("IBMCLOUD_IAM_API_ENDPOINT"); err == nil {
		exportStr += fmt.Sprintf("export IBMCLOUD_IAM_API_ENDPOINT=%s;", iamEndpoint)
	}
	if piEndpoint, err := getEnvVar("IBMCLOUD_POWER_API_ENDPOINT"); err == nil {
		exportStr += fmt.Sprintf("export IBMCLOUD_POWER_API_ENDPOINT=%s;", piEndpoint)
	}
	if rcEndpoint, err := getEnvVar("IBMCLOUD_RESOURCE_CONTROLLER_ENDPOINT"); err == nil {
		exportStr += fmt.Sprintf("export IBMCLOUD_RESOURCE_CONTROLLER_ENDPOINT=%s;", rcEndpoint)
	}

	runCmd := fmt.Sprintf("'%s /usr/bin/nohup %s/%s -v=6 --endpoint=%s 2> %s/%s < /dev/null > /dev/null &'",
		exportStr, r.remoteDir, binName, endpoint, r.remoteDir, outputFile)
	if output, err := r.runSSHCommand("sh", "-c", runCmd); err != nil {
		return fmt.Errorf("failed to run the driver: %s: %v", output, err)
	}

	return nil
}

func (r *Remote) runSSHCommand(arg ...string) (string, error) {
	return runRemoteCommand(r.publicIP, arg...)
}

func (r *Remote) createSSHTunnel(endpoint string) error {
	port := endpoint[strings.LastIndex(endpoint, ":")+1:]

	args := []string{"-i", sshDefaultKey, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-nNT", "-L", fmt.Sprintf("%s:localhost:%s", port, port), fmt.Sprintf("%s@%s", sshUser, r.publicIP)}

	klog.Infof("Executing SSH command: ssh %v", args)
	sshCmd := exec.Command("ssh", args...)
	err := sshCmd.Start()
	if err != nil {
		return err
	}
	r.sshPID = sshCmd.Process.Pid
	return nil
}

func (r *Remote) TeardownDriver() {
	// sshPID=0 means something went wrong, no ssh tunnel created
	if r.sshPID != 0 {
		// Close the SSH tunnel
		err := findAndKillProcess(r.sshPID)
		if err != nil {
			klog.Warningf("failed to find ssh PID, might leave behind some stale tunnel process: %v", err)
		}
	}

	// Kill the driver process?
	// killCmd := "$'kill $(ps -ef | grep ibm-powervs-block-csi-driver | grep -v grep | awk \\'{print $2}\\')'"
	// if output, err := r.runSSHCommand("sh", "-c", killCmd); err != nil {
	// 	klog.Warningf("failed to kill remote driver, might leave behind some stale driver process: %s: %v", output, err)
	// }

	r.printRemoteLog()

	// Delete any created ssh key files and tarball
	os.Remove(sshDefaultKey)
	os.Remove(sshDefaultKey + ".pub")
	os.Remove(r.tarPath)

	r.destroyPVSResources()
}

func (r *Remote) printRemoteLog() {
	if r.publicIP == "" || r.remoteDir == "" {
		return
	}
	klog.Infof("printing remote log from %s/%s:", r.remoteDir, outputFile)
	out, _ := r.runSSHCommand("cat", fmt.Sprintf("%s/%s", r.remoteDir, outputFile))
	klog.Info(out)
}
