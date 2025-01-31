/*
Copyright 2022 The Kubernetes Authors.

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
	"errors"

	"github.com/IBM-Cloud/power-go-client/power/models"

	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	PowerVSInstanceStateSHUTOFF  = "SHUTOFF"
	PowerVSInstanceStateACTIVE   = "ACTIVE"
	PowerVSInstanceStateERROR    = "ERROR"
	PowerVSInstanceHealthWARNING = "WARNING"
	PowerVSInstanceHealthOK      = "OK"
	StoragePoolAffinity          = false
)

type NodeUpdateScopeParams struct {
	InstanceId        string
	ServiceInstanceId string
	Zone              string
}

type NodeUpdateScope struct {
	Cloud
	InstanceId        string
	ServiceInstanceId string
	Zone              string
}

func NewNodeUpdateScope(params NodeUpdateScopeParams) (scope *NodeUpdateScope, err error) {
	if params.ServiceInstanceId == "" {
		err = errors.New("ServiceInstanceId is required when creating a NodeUpdateScope")
		return
	}
	if params.InstanceId == "" {
		err = errors.New("InstanceId is required when creating a NodeUpdateScope")
		return
	}
	if params.Zone == "" {
		err = errors.New("zone is required when creating a NodeUpdateScope")
		return
	}
	c, err := NewPowerVSCloud(params.ServiceInstanceId, params.Zone, false)
	if err != nil {
		klog.Errorf("Failed to get powervs cloud: %v", err)
		return
	}

	return &NodeUpdateScope{
		ServiceInstanceId: params.ServiceInstanceId,
		InstanceId:        params.InstanceId,
		Zone:              params.Zone,
		Cloud:             c,
	}, nil
}

func (p *powerVSCloud) GetPVMInstanceDetails(instanceID string) (*models.PVMInstance, error) {
	return p.pvmInstancesClient.Get(instanceID)
}

func (p *powerVSCloud) UpdateStoragePoolAffinity(instanceID string) error {
	_, err := p.pvmInstancesClient.Update(instanceID,
		&models.PVMInstanceUpdate{
			StoragePoolAffinity: ptr.To(StoragePoolAffinity),
		})
	return err
}
