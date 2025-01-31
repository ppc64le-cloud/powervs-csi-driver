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
	"errors"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/util"
)

// PowerVS volume types.
const (
	VolumeTypeTier1 = "tier1"
	VolumeTypeTier3 = "tier3"
)

var (
	ValidVolumeTypes = []string{
		VolumeTypeTier1,
		VolumeTypeTier3,
	}
)

// Defaults.
const (
	// DefaultVolumeSize represents the default volume size.
	DefaultVolumeSize int64 = 10 * util.GiB
	// DefaultVolumeType specifies which storage to use for newly created Volumes.
	DefaultVolumeType = VolumeTypeTier1
)

var (
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = errors.New("resource was not found")

	// ErrAlreadyExists is returned when a resource is already existent.
	ErrAlreadyExists = errors.New("resource already exists")
)

// Disk represents a PowerVS volume.
type Disk struct {
	VolumeID    string
	DiskType    string
	WWN         string
	Name        string
	Shareable   bool
	CapacityGiB int64
}

// DiskOptions represents parameters to create an PowerVS volume.
type DiskOptions struct {
	// PowerVS options
	Shareable bool
	// CapacityGigaBytes float64
	CapacityBytes int64
	VolumeType    string
}
