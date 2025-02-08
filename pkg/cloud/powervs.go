/*
Copyright 2019 The Kubernetes Authors.

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

package cloud

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/util"
)

var _ Cloud = &powerVSCloud{}

const (
	PollTimeout          = 120 * time.Second
	PollInterval         = 5 * time.Second
	VolumeInUseState     = "in-use"
	VolumeAvailableState = "available"
)

type powerVSCloud struct {
	pvmInstancesClient *instance.IBMPIInstanceClient
	volClient          *instance.IBMPIVolumeClient
	cloneVolumeClient  *instance.IBMPICloneVolumeClient
}

type PVMInstance struct {
	ID       string
	DiskType string
	Name     string
}

func NewPowerVSCloud(cloudInstanceID, zone string, debug bool) (Cloud, error) {
	return newPowerVSCloud(cloudInstanceID, zone, debug)
}

func newPowerVSCloud(cloudInstanceID, zone string, debug bool) (Cloud, error) {
	apikey, err := readCredentials()
	if err != nil {
		return nil, err
	}

	authenticator := &core.IamAuthenticator{ApiKey: apikey, URL: os.Getenv("IBMCLOUD_IAM_API_ENDPOINT")}

	serviceClientOptions := &resourcecontrollerv2.ResourceControllerV2Options{
		Authenticator: authenticator,
		URL:           os.Getenv("IBMCLOUD_RESOURCE_CONTROLLER_ENDPOINT"),
	}
	serviceClient, err := resourcecontrollerv2.NewResourceControllerV2(serviceClientOptions)
	if err != nil {
		return nil, fmt.Errorf("errored while creating NewResourceControllerV2: %v", err)
	}
	resourceInstance, _, err := serviceClient.GetResourceInstance(&resourcecontrollerv2.GetResourceInstanceOptions{
		ID: &cloudInstanceID,
	})

	if err != nil {
		return nil, fmt.Errorf("errored while getting the Power VS service instance with ID: %s, err: %v", cloudInstanceID, err)
	}

	piOptions := ibmpisession.IBMPIOptions{Authenticator: authenticator, Debug: debug, UserAccount: *resourceInstance.AccountID, Zone: zone}
	piSession, err := ibmpisession.NewIBMPISession(&piOptions)
	if err != nil {
		return nil, err
	}

	backgroundContext := context.Background()
	volClient := instance.NewIBMPIVolumeClient(backgroundContext, piSession, cloudInstanceID)
	pvmInstancesClient := instance.NewIBMPIInstanceClient(backgroundContext, piSession, cloudInstanceID)
	cloneVolumeClient := instance.NewIBMPICloneVolumeClient(backgroundContext, piSession, cloudInstanceID)

	return &powerVSCloud{
		pvmInstancesClient: pvmInstancesClient,
		volClient:          volClient,
		cloneVolumeClient:  cloneVolumeClient,
	}, nil
}

func (p *powerVSCloud) GetPVMInstanceByName(name string) (*PVMInstance, error) {
	in, err := p.pvmInstancesClient.GetAll()
	if err != nil {
		return nil, err
	}
	for _, pvmInstance := range in.PvmInstances {
		if name == *pvmInstance.ServerName {
			return &PVMInstance{
				ID:       *pvmInstance.PvmInstanceID,
				DiskType: pvmInstance.StorageType,
				Name:     *pvmInstance.ServerName,
			}, nil
		}
	}
	return nil, ErrNotFound
}

func (p *powerVSCloud) GetPVMInstanceByID(instanceID string) (*PVMInstance, error) {
	in, err := p.pvmInstancesClient.Get(instanceID)
	if err != nil {
		return nil, err
	}

	return &PVMInstance{
		ID:       *in.PvmInstanceID,
		DiskType: *in.StorageType,
		Name:     *in.ServerName,
	}, nil
}

func (p *powerVSCloud) CreateDisk(volumeName string, diskOptions *DiskOptions) (disk *Disk, err error) {
	var volumeType string
	capacityGiB := util.BytesToGiB(diskOptions.CapacityBytes)

	switch diskOptions.VolumeType {
	case VolumeTypeTier1, VolumeTypeTier3:
		volumeType = diskOptions.VolumeType
	case "":
		volumeType = DefaultVolumeType
	default:
		return nil, fmt.Errorf("invalid PowerVS VolumeType %q", diskOptions.VolumeType)
	}

	dataVolume := &models.CreateDataVolume{
		Name:      &volumeName,
		Size:      ptr.To[float64](float64(capacityGiB)),
		Shareable: &diskOptions.Shareable,
		DiskType:  volumeType,
	}

	v, err := p.volClient.CreateVolume(dataVolume)
	if err != nil {
		return nil, err
	}

	err = p.WaitForVolumeState(*v.VolumeID, VolumeAvailableState)
	if err != nil {
		return nil, err
	}

	return &Disk{CapacityGiB: capacityGiB, VolumeID: *v.VolumeID, DiskType: v.DiskType, WWN: strings.ToLower(v.Wwn)}, nil
}

func (p *powerVSCloud) DeleteDisk(volumeID string) (err error) {
	return p.volClient.DeleteVolume(volumeID)
}

func (p *powerVSCloud) AttachDisk(volumeID string, nodeID string) (err error) {
	if err = p.volClient.Attach(nodeID, volumeID); err != nil {
		return err
	}
	return p.WaitForVolumeState(volumeID, VolumeInUseState)
}

func (p *powerVSCloud) DetachDisk(volumeID string, nodeID string) (err error) {
	if err = p.volClient.Detach(nodeID, volumeID); err != nil {
		return err
	}
	return p.WaitForVolumeState(volumeID, VolumeAvailableState)
}

// IsAttached returns an error if a volume isn't attached to a node, else nil.
func (p *powerVSCloud) IsAttached(volumeID string, nodeID string) (err error) {
	_, err = p.volClient.CheckVolumeAttach(nodeID, volumeID)
	return err
}

func (p *powerVSCloud) ResizeDisk(volumeID string, reqSize int64) (newSize int64, err error) {
	disk, err := p.GetDiskByID(volumeID)
	if err != nil {
		return 0, err
	}

	dataVolume := &models.UpdateVolume{
		Name:      &disk.Name,
		Size:      float64(util.BytesToGiB(reqSize)),
		Shareable: &disk.Shareable,
	}

	v, err := p.volClient.UpdateVolume(volumeID, dataVolume)
	if err != nil {
		return 0, err
	}
	return int64(*v.Size), nil
}

func (p *powerVSCloud) CloneDisk(sourceVolumeID string, cloneVolumeName string) (disk *Disk, err error) {
	_, err = p.GetDiskByID(sourceVolumeID)
	if err != nil {
		return nil, err
	}
	cloneVolumeReq := &models.VolumesCloneAsyncRequest{
		Name:      &cloneVolumeName,
		VolumeIDs: []string{sourceVolumeID},
	}
	cloneTaskRef, err := p.cloneVolumeClient.Create(cloneVolumeReq)
	if err != nil {
		return nil, err
	}
	cloneTaskId := cloneTaskRef.CloneTaskID
	err = p.WaitForCloneStatus(*cloneTaskId)
	if err != nil {
		return nil, err
	}
	clonedVolumeDetails, err := p.cloneVolumeClient.Get(*cloneTaskId)
	if err != nil {
		return nil, err
	}
	clonedVolumeID := clonedVolumeDetails.ClonedVolumes[0].ClonedVolumeID
	err = p.WaitForVolumeState(clonedVolumeID, VolumeAvailableState)
	if err != nil {
		return nil, err
	}
	return p.GetDiskByID(clonedVolumeID)
}

func (p *powerVSCloud) WaitForVolumeState(volumeID, state string) error {
	ctx := context.Background()
	return wait.PollUntilContextTimeout(ctx, PollInterval, PollTimeout, true, func(ctx context.Context) (bool, error) {
		v, err := p.volClient.Get(volumeID)
		if err != nil {
			return false, err
		}
		spew.Dump(v)
		return v.State == state, nil
	})
}

func (p *powerVSCloud) WaitForCloneStatus(cloneTaskId string) error {
	err := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		c, err := p.cloneVolumeClient.Get(cloneTaskId)
		if err != nil {
			return false, err
		}
		spew.Dump(*c)
		return *c.Status == "completed", nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (p *powerVSCloud) GetDiskByName(name string) (disk *Disk, err error) {
	vols, err := p.volClient.GetAll()
	if err != nil {
		return nil, err
	}
	for _, v := range vols.Volumes {
		if name == *v.Name {
			return &Disk{
				Name:        *v.Name,
				DiskType:    *v.DiskType,
				VolumeID:    *v.VolumeID,
				WWN:         strings.ToLower(*v.Wwn),
				Shareable:   *v.Shareable,
				CapacityGiB: int64(*v.Size),
			}, nil
		}
	}

	return nil, ErrNotFound
}

func (p *powerVSCloud) GetDiskByNamePrefix(namePrefix string) (disk *Disk, err error) {
	vols, err := p.volClient.GetAll()
	if err != nil {
		return nil, err
	}
	for _, v := range vols.Volumes {
		if strings.HasPrefix(*v.Name, namePrefix) {
			return &Disk{
				Name:        *v.Name,
				DiskType:    *v.DiskType,
				VolumeID:    *v.VolumeID,
				WWN:         strings.ToLower(*v.Wwn),
				Shareable:   *v.Shareable,
				CapacityGiB: int64(*v.Size),
			}, nil
		}
	}

	return nil, ErrNotFound
}

func (p *powerVSCloud) GetDiskByID(volumeID string) (disk *Disk, err error) {
	v, err := p.volClient.Get(volumeID)
	if err != nil {
		if strings.Contains(err.Error(), "Resource not found") {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Disk{
		Name:        *v.Name,
		DiskType:    v.DiskType,
		VolumeID:    *v.VolumeID,
		WWN:         strings.ToLower(v.Wwn),
		Shareable:   *v.Shareable,
		CapacityGiB: int64(*v.Size),
	}, nil
}

func readCredentials() (string, error) {
	apiKey, err := readCredentialsFromFile()
	if err != nil {
		return "", err
	}
	if apiKey != "" {
		return apiKey, nil
	}

	klog.Info("Falling back to read IBMCLOUD_API_KEY environment variable for the key")
	apiKey = os.Getenv("IBMCLOUD_API_KEY")
	if apiKey == "" {
		return "", errors.New("IBMCLOUD_API_KEY is not provided")
	}

	return apiKey, nil
}

func readCredentialsFromFile() (string, error) {
	apiKeyPath := os.Getenv("API_KEY_PATH")
	if apiKeyPath == "" {
		klog.Warning("API_KEY_PATH is undefined")
		return "", nil
	}

	byteData, err := os.ReadFile(apiKeyPath)
	if err != nil {
		return "", fmt.Errorf("error reading apikey: %v", err)
	}
	return string(byteData), nil
}
