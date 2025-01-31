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
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/power/models"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
)

func (r *Remote) createPVSResources() (err error) {
	var image, network string

	if err = r.initPowerVSClients(); err != nil {
		return err
	}

	if image, err = getEnvVar("POWERVS_IMAGE"); err != nil {
		image = "CentOS-Stream-8"
		if err = r.createImage(image); err != nil {
			return fmt.Errorf("error while creating ibm pi image: %v", err)
		}
	}

	if network, err = getEnvVar("POWERVS_NETWORK"); err != nil {
		network = "pub-network"
		netID, err := r.createPublicNetwork(network)
		if err != nil {
			return fmt.Errorf("error while creating ibm pi network: %v", err)
		}
		// Use network ID instead of name (there are cache issue at PowerVS)
		network = netID
	}

	if err = r.createSSHKey(); err != nil {
		return fmt.Errorf("error while creating ibm pi ssh key: %v", err)
	}

	insID, pubIP, err := r.createInstance(image, network)
	if err != nil {
		return err
	}
	os.Setenv("POWERVS_INSTANCE_ID", insID)
	r.publicIP = pubIP

	return nil
}

func (r *Remote) initPowerVSClients() error {
	piSession, err := getIBMPISession()
	if err != nil {
		return fmt.Errorf("error while creating ibm pi session: %v", err)
	}
	piID, err := getPiID()
	if err != nil {
		return fmt.Errorf("error while getting ibm pi id: %v", err)
	}
	r.powervsClients.ic = instance.NewIBMPIInstanceClient(context.Background(), piSession, piID)
	r.powervsClients.imgc = instance.NewIBMPIImageClient(context.Background(), piSession, piID)
	r.powervsClients.nc = instance.NewIBMPINetworkClient(context.Background(), piSession, piID)
	r.powervsClients.kc = instance.NewIBMPIKeyClient(context.Background(), piSession, piID)
	return nil
}

func (r *Remote) createImage(image string) error {
	imgc := r.powervsClients.imgc
	if _, err := imgc.Get(image); err != nil {
		// If no image then copy one
		stockImages, err := imgc.GetAllStockImages(false, false)
		if err != nil {
			return err
		}
		var stockImageID string
		for _, sI := range stockImages.Images {
			if *sI.Name == image {
				stockImageID = *sI.ImageID
			}
		}
		if stockImageID == "" {
			return fmt.Errorf("cannot find image: %s", image)
		}
		if _, err := imgc.Create(&models.CreateImage{ImageID: stockImageID}); err != nil {
			return err
		}
	}
	return nil
}

// If given network name is not found in the network list,
// create a new public network with the same name and use it.
func (r *Remote) createPublicNetwork(network string) (string, error) {
	nc := r.powervsClients.nc
	netFound := ""
	nets, err := nc.GetAll()
	if err != nil {
		return "", err
	}
	for _, n := range nets.Networks {
		if *n.Name == network {
			netFound = *n.NetworkID
		}
	}

	// If no public network then create one
	if netFound == "" {
		netType := "pub-vlan"
		if _, err := nc.Create(&models.NetworkCreate{Name: network, Type: &netType}); err != nil {
			return "", err
		}
		net, err := waitForNetworkVLAN(network, nc)
		if err != nil {
			return "", err
		}
		netFound = *net.NetworkID
	}
	return netFound, nil
}

// Generate a new SSH key pair and create a SSHKey using the pub key.
func (r *Remote) createSSHKey() error {
	kc := r.powervsClients.kc
	sshKey := r.resName
	// Create SSH key pair
	out, err := runCommand("ssh-keygen", "-t", "rsa", "-f", sshDefaultKey, "-N", "")
	klog.Infof("ssh-keygen command output: %s err: %v", out, err)

	publicKey, err := os.ReadFile(sshDefaultKey + ".pub")
	if err != nil {
		return fmt.Errorf("error while creating and reading SSH key files: %v", err)
	}

	sshPubKey := string(publicKey)
	_, err = kc.Create(&models.SSHKey{Name: &sshKey, SSHKey: &sshPubKey})

	return err
}

// Create a pvm instance.
func (r *Remote) createInstance(image, network string) (string, string, error) {
	ic := r.powervsClients.ic
	name := r.resName
	memory := 4.0
	processors := 0.5
	procType := "shared"
	sysType := "e980"
	storageType := "tier1"

	nets := []*models.PVMInstanceAddNetwork{{NetworkID: &network}}
	req := &models.PVMInstanceCreate{
		ImageID:     &image,
		KeyPairName: name,
		Networks:    nets,
		ServerName:  &name,
		Memory:      &memory,
		Processors:  &processors,
		ProcType:    &procType,
		SysType:     sysType,
		StorageType: storageType,
	}
	resp, err := ic.Create(req)
	if err != nil {
		return "", "", fmt.Errorf("error while creating pvm instance: %v", err)
	}

	insID := ""
	for _, in := range *resp {
		insID = *in.PvmInstanceID
	}

	if insID == "" {
		return "", "", fmt.Errorf("error while fetching pvm instance: %v", err)
	}

	in, err := waitForInstanceHealth(insID, ic)
	if err != nil {
		return "", "", fmt.Errorf("error while waiting for pvm instance status: %v", err)
	}

	publicIP := ""
	insNets := in.Networks
	for _, net := range insNets {
		publicIP = net.ExternalIP
	}

	if publicIP == "" {
		return "", "", errors.New("error while getting pvm instance public IP")
	}

	err = waitForInstanceSSH(publicIP)
	if err != nil {
		return "", "", fmt.Errorf("error while waiting for pvm instance ssh connection: %v", err)
	}

	return insID, publicIP, err
}

// Wait till VLAN is attached to the network.
func waitForNetworkVLAN(netID string, nc *instance.IBMPINetworkClient) (*models.Network, error) {
	var network *models.Network
	err := wait.PollUntilContextTimeout(context.Background(), 15*time.Second, 5*time.Minute, true, func(context.Context) (bool, error) {
		var err error
		network, err = nc.Get(netID)
		if err != nil || network == nil {
			return false, err
		}
		if network.VlanID != nil {
			return true, nil
		}
		return false, nil
	})

	if err != nil || network == nil {
		return nil, fmt.Errorf("failed to get target instance status: %v", err)
	}
	return network, err
}

// Wait for VM status is ACTIVE and VM Health status to be OK/Warning.
func waitForInstanceHealth(insID string, ic *instance.IBMPIInstanceClient) (*models.PVMInstance, error) {
	var pvm *models.PVMInstance
	err := wait.PollUntilContextTimeout(context.Background(), 30*time.Second, 45*time.Minute, true, func(context.Context) (bool, error) {
		var err error
		pvm, err = ic.Get(insID)
		if err != nil {
			return false, err
		}
		if *pvm.Status == cloud.PowerVSInstanceStateERROR {
			if pvm.Fault != nil {
				return false, fmt.Errorf("failed to create a PowerVS instance: %s", pvm.Fault.Message)
			}
			return false, errors.New("failed to create a PowerVS instance")
		}
		// Check for `instanceReadyStatus` health status and also the final health status "OK"
		if *pvm.Status == cloud.PowerVSInstanceStateACTIVE && (pvm.Health.Status == cloud.PowerVSInstanceHealthWARNING || pvm.Health.Status == cloud.PowerVSInstanceHealthOK) {
			return true, nil
		}

		return false, nil
	})

	if err != nil || pvm == nil {
		return nil, fmt.Errorf("failed to get target instance status: %v", err)
	}
	return pvm, err
}

// Wait till SSH test is complete.
func waitForInstanceSSH(publicIP string) error {
	err := wait.PollUntilContextTimeout(context.Background(), 20*time.Second, 30*time.Minute, true, func(context.Context) (bool, error) {
		var err error
		outp, err := runRemoteCommand(publicIP, "hostname")
		klog.Infof("out: %s err: %v", outp, err)
		if err != nil {
			return false, err
		}
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("failed to get ssh connection: %v", err)
	}

	return err
}

// Delete the VM and SSH key.
func (r *Remote) destroyPVSResources() {
	if r.powervsClients.ic == nil || r.powervsClients.kc == nil {
		klog.Warning("failed to retrieve PowerVS instance and key clients, ignoring")
		return
	}

	err := r.powervsClients.kc.Delete(r.resName)
	if err != nil {
		klog.Warningf("failed to destroy pvs resources, might leave behind some stale instances: %v", err)
	}

	err = r.powervsClients.ic.Delete(r.resName)
	if err != nil {
		klog.Warningf("failed to destroy pvs resources, might leave behind some stale ssh keys: %v", err)
	}
}
