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

package driver

import (
	"context"
	"os"
	"strings"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	gcfg "gopkg.in/gcfg.v1"

	"k8s.io/klog/v2"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/util"
)

// Supported access modes.
const (
	SingleNodeWriter     = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	MultiNodeMultiWriter = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	MultiNodeReaderOnly  = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
)

var (
	// controllerCaps represents the capability of controller service.
	controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
	}
)

// controllerService represents the controller service of CSI driver.
type controllerService struct {
	cloud         cloud.Cloud
	driverOptions *Options
	volumeLocks   *util.VolumeLocks
	csi.UnimplementedControllerServer
}

// Provider holds information from the cloud provider.
type Provider struct {
	// PowerVSCloudInstanceID is IBM Power VS service instance id
	PowerVSCloudInstanceID string `gcfg:"powerVSCloudInstanceID"`
	// PowerVSZone is IBM Power VS service zone
	PowerVSZone string `gcfg:"powerVSZone"`
}

// CloudConfig is the ibm cloud provider config data.
type CloudConfig struct {
	// [provider] section
	Prov Provider `gcfg:"provider"`
}

var (
	NewPowerVSCloudFunc = cloud.NewPowerVSCloud
)

// newControllerService creates a new controller service and prints stack trace and osexit if failed to create the service.
func newControllerService(driverOptions *Options) controllerService {
	var cloudInstanceId, zone string

	// Following env vars will be checked before looking up the
	// cli options and metadata service if both are not set.
	// It will help with running the controller tests locally
	// on a powervs vm. Currently, it relies on k8s cluster node.
	cID := os.Getenv("POWERVS_CLOUD_INSTANCE_ID")
	z := os.Getenv("POWERVS_ZONE")

	if cID != "" && z != "" {
		klog.V(4).Info("using node info from environment variables")
		cloudInstanceId, zone = cID, z
	} else if driverOptions.cloudconfig != "" {
		var cloudConfig CloudConfig
		config, err := os.Open(driverOptions.cloudconfig)
		if err != nil {
			klog.Fatalf("Failed to get cloud config: %v", err)
		}
		defer config.Close()

		if err := gcfg.FatalOnly(gcfg.ReadInto(&cloudConfig, config)); err != nil {
			klog.Fatalf("Failed to read cloud config: %v", err)
		}
		cloudInstanceId = cloudConfig.Prov.PowerVSCloudInstanceID
		zone = cloudConfig.Prov.PowerVSZone
		if cloudInstanceId == "" || zone == "" {
			klog.Fatalf("Failed to read cloud config: %v", status.Errorf(codes.NotFound, "cloud instance id or zone is empty"))
		}
	} else {
		klog.V(4).Infof("retrieving node info from metadata service")
		metadata, err := cloud.NewMetadataService(cloud.DefaultKubernetesAPIClient, driverOptions.kubeconfig)
		if err != nil {
			klog.Fatalf("Failed to get metadata service: %v", err)
		}
		cloudInstanceId = metadata.GetCloudInstanceId()
		zone = metadata.GetZone()
	}

	c, err := NewPowerVSCloudFunc(cloudInstanceId, zone, driverOptions.debug)
	if err != nil {
		klog.Fatalf("Failed to get powervs cloud: %v", err)
	}

	return controllerService{
		cloud:         c,
		driverOptions: driverOptions,
		volumeLocks:   util.NewVolumeLocks(),
	}
}

func (d *controllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.V(4).Infof("CreateVolume: called with args %+v", req)
	volName := req.GetName()
	if volName == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume name not provided")
	}

	if acquired := d.volumeLocks.TryAcquire(volName); !acquired {
		return nil, status.Errorf(codes.Aborted, util.VolumeOperationAlreadyExistsFmt, volName)
	}
	defer d.volumeLocks.Release(volName)

	volSizeBytes, err := getVolSizeBytes(req)
	if err != nil {
		return nil, err
	}

	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities not provided")
	}

	if !isValidVolumeCapabilities(volCaps) {
		modes := util.GetAccessModes(volCaps)
		stringModes := strings.Join(*modes, ", ")
		errString := "Volume capabilities " + stringModes + " not supported. Only AccessModes [ReadWriteOnce], [ReadWriteMany], [ReadOnlyMany] supported."
		return nil, status.Error(codes.InvalidArgument, errString)
	}

	volumeType := cloud.DefaultVolumeType

	for key, value := range req.GetParameters() {
		switch strings.ToLower(key) {
		case VolumeTypeKey:
			volumeType = value
		default:
			return nil, status.Errorf(codes.InvalidArgument, "Invalid parameter key %s for CreateVolume", key)
		}
	}

	shareable := isShareableVolume(volCaps)
	opts := &cloud.DiskOptions{
		Shareable:     shareable,
		CapacityBytes: volSizeBytes,
		VolumeType:    volumeType,
	}

	if req.GetVolumeContentSource() != nil {
		volumeSource := req.VolumeContentSource
		switch volumeSource.Type.(type) {
		case *csi.VolumeContentSource_Volume:
			diskDetails, _ := d.cloud.GetDiskByNamePrefix("clone-" + volName)
			if diskDetails != nil {
				err := verifyVolumeDetails(opts, diskDetails)
				if err != nil {
					return nil, err
				}
				return newCreateVolumeResponse(diskDetails, req.VolumeContentSource), nil
			}
			if srcVolume := volumeSource.GetVolume(); srcVolume != nil {
				srcVolumeID := srcVolume.GetVolumeId()
				diskDetails, err := d.cloud.GetDiskByID(srcVolumeID)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Could not get the source volume %q: %v", srcVolumeID, err)
				}
				if util.GiBToBytes(diskDetails.CapacityGiB) != volSizeBytes {
					return nil, status.Errorf(codes.Internal, "Cannot clone volume %v, source volume size is not equal to the clone volume", srcVolumeID)
				}
				err = verifyVolumeDetails(opts, diskDetails)
				if err != nil {
					return nil, err
				}
				diskFromSourceVolume, err := d.cloud.CloneDisk(srcVolumeID, volName)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Could not create volume %q: %v", volName, err)
				}

				cloneDiskDetails, err := d.cloud.GetDiskByID(diskFromSourceVolume.VolumeID)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Could not create volume %q: %v", volName, err)
				}
				return newCreateVolumeResponse(cloneDiskDetails, req.VolumeContentSource), nil
			}
		default:
			return nil, status.Errorf(codes.InvalidArgument, "%v not a proper volume source", volumeSource)
		}
	}

	// check if disk exists
	// disk exists only if previous createVolume request fails due to any network/tcp error
	diskDetails, _ := d.cloud.GetDiskByName(volName)
	if diskDetails != nil {
		// wait for volume to be available as the volume already exists
		err := verifyVolumeDetails(opts, diskDetails)
		if err != nil {
			return nil, err
		}
		err = d.cloud.WaitForVolumeState(diskDetails.VolumeID, cloud.VolumeAvailableState)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Disk already exists and not in expected state")
		}
		return newCreateVolumeResponse(diskDetails, req.VolumeContentSource), nil
	}

	disk, err := d.cloud.CreateDisk(volName, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not create volume %q: %v", volName, err)
	}
	return newCreateVolumeResponse(disk, req.VolumeContentSource), nil
}

func (d *controllerService) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.V(4).Infof("DeleteVolume: called with args: %+v", req)
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, util.VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	if _, err := d.cloud.GetDiskByID(volumeID); err != nil {
		if err == cloud.ErrNotFound {
			klog.V(4).Info("DeleteVolume: volume not found, returning with success")
			return &csi.DeleteVolumeResponse{}, nil
		}
	}

	if err := d.cloud.DeleteDisk(volumeID); err != nil {
		return nil, status.Errorf(codes.Internal, "Could not delete volume ID %q: %v", volumeID, err)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (d *controllerService) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerPublishVolume: called with args %+v", req)
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, util.VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	nodeID := req.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Node ID not provided")
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not provided")
	}

	caps := []*csi.VolumeCapability{volCap}
	if !isValidVolumeCapabilities(caps) {
		modes := util.GetAccessModes(caps)
		stringModes := strings.Join(*modes, ", ")
		errString := "Volume capabilities " + stringModes + " not supported. Only AccessModes [ReadWriteOnce], [ReadWriteMany], [ReadOnlyMany] supported."
		return nil, status.Error(codes.InvalidArgument, errString)
	}

	if _, err := d.cloud.GetPVMInstanceByID(nodeID); err != nil {
		return nil, status.Errorf(codes.NotFound, "Instance %q not found, err: %v", nodeID, err)
	}

	disk, err := d.cloud.GetDiskByID(volumeID)

	if err != nil {
		if err == cloud.ErrNotFound {
			return nil, status.Error(codes.NotFound, "Volume not found")
		}
		return nil, status.Errorf(codes.Internal, "Could not get volume with ID %q: %v", volumeID, err)
	}

	pvInfo := map[string]string{WWNKey: disk.WWN}

	if err = d.cloud.IsAttached(volumeID, nodeID); err == nil {
		klog.V(5).Infof("ControllerPublishVolume: volume %s already attached to node %s, returning success", volumeID, nodeID)
		return &csi.ControllerPublishVolumeResponse{PublishContext: pvInfo}, nil
	}

	err = d.cloud.AttachDisk(volumeID, nodeID)
	if err != nil {
		if err == cloud.ErrAlreadyExists {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "Could not attach volume %q to node %q: %v", volumeID, nodeID, err)
	}
	klog.V(5).Infof("ControllerPublishVolume: volume %s attached to node %s", volumeID, nodeID)

	return &csi.ControllerPublishVolumeResponse{PublishContext: pvInfo}, nil
}

func (d *controllerService) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerUnpublishVolume: called with args %+v", req)
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, util.VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	nodeID := req.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Node ID not provided")
	}

	if _, err := d.cloud.GetDiskByID(volumeID); err != nil {
		if err == cloud.ErrNotFound {
			klog.V(4).Info("ControllerUnpublishVolume: volume not found, returning with success")
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
	}

	if err := d.cloud.IsAttached(volumeID, nodeID); err != nil {
		klog.V(4).Infof("ControllerUnpublishVolume: volume %s is not attached to %s, err: %v, returning with success", volumeID, nodeID, err)
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	if err := d.cloud.DetachDisk(volumeID, nodeID); err != nil {
		return nil, status.Errorf(codes.Internal, "Could not detach volume %q from node %q: %v", volumeID, nodeID, err)
	}
	klog.V(5).Infof("ControllerUnpublishVolume: volume %s detached from node %s", volumeID, nodeID)

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (d *controllerService) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.V(4).Infof("ControllerGetCapabilities: called with args %+v", req)
	caps := make([]*csi.ControllerServiceCapability, 0, len(controllerCaps))
	for _, cap := range controllerCaps {
		c := &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		}
		caps = append(caps, c)
	}
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (d *controllerService) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	klog.V(4).Infof("GetCapacity: called with args %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func (d *controllerService) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	klog.V(4).Infof("ListVolumes: called with args %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func (d *controllerService) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.V(4).Infof("ValidateVolumeCapabilities: called with args %+v", req)
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities not provided")
	}

	if _, err := d.cloud.GetDiskByID(volumeID); err != nil {
		if err == cloud.ErrNotFound {
			return nil, status.Error(codes.NotFound, "Volume not found")
		}
		return nil, status.Errorf(codes.Internal, "Could not get volume with ID %q: %v", volumeID, err)
	}

	var confirmed *csi.ValidateVolumeCapabilitiesResponse_Confirmed
	if isValidVolumeCapabilities(volCaps) {
		confirmed = &csi.ValidateVolumeCapabilitiesResponse_Confirmed{VolumeCapabilities: volCaps}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: confirmed,
	}, nil
}

func (d *controllerService) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	klog.V(4).Infof("ControllerExpandVolume: called with args %+v", req)
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, util.VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	capRange := req.GetCapacityRange()
	if capRange == nil {
		return nil, status.Error(codes.InvalidArgument, "Capacity range not provided")
	}

	newSize := util.RoundUpBytes(capRange.GetRequiredBytes())
	maxVolSize := capRange.GetLimitBytes()
	if maxVolSize > 0 && maxVolSize < newSize {
		return nil, status.Error(codes.InvalidArgument, "After round-up, volume size exceeds the limit specified")
	}

	actualSizeGiB, err := d.cloud.ResizeDisk(volumeID, newSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not resize volume %q: %v", volumeID, err)
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         util.GiBToBytes(actualSizeGiB),
		NodeExpansionRequired: true,
	}, nil
}

func (d *controllerService) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	klog.V(4).Infof("ControllerGetVolume: called with args %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func (d *controllerService) ControllerModifyVolume(ctx context.Context, req *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	klog.V(4).InfoS("ControllerModifyVolume: called with args %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) bool {
	for _, c := range volCaps {
		mode := c.AccessMode.GetMode()
		if mode != SingleNodeWriter && mode != MultiNodeMultiWriter && mode != MultiNodeReaderOnly {
			return false
		}
	}
	return true
}

// Check if the volume is shareable.
func isShareableVolume(volCaps []*csi.VolumeCapability) bool {
	for _, c := range volCaps {
		mode := c.AccessMode.GetMode()
		if mode == MultiNodeMultiWriter || mode == MultiNodeReaderOnly {
			return true
		}
	}
	return false
}

func (d *controllerService) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(4).Infof("CreateSnapshot: called with args %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func (d *controllerService) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).Infof("DeleteSnapshot: called with args %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func (d *controllerService) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(4).Infof("ListSnapshots: called with args %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

func newCreateVolumeResponse(disk *cloud.Disk, src *csi.VolumeContentSource) *csi.CreateVolumeResponse {
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      disk.VolumeID,
			CapacityBytes: util.GiBToBytes(disk.CapacityGiB),
			VolumeContext: map[string]string{},
			ContentSource: src,
		},
	}
}

func getVolSizeBytes(req *csi.CreateVolumeRequest) (int64, error) {
	var volSizeBytes int64
	capRange := req.GetCapacityRange()
	if capRange == nil {
		volSizeBytes = cloud.DefaultVolumeSize
	} else {
		volSizeBytes = util.RoundUpBytes(capRange.GetRequiredBytes())
		maxVolSize := capRange.GetLimitBytes()
		if maxVolSize > 0 && maxVolSize < volSizeBytes {
			return 0, status.Error(codes.InvalidArgument, "After round-up, volume size exceeds the limit specified")
		}
	}
	return volSizeBytes, nil
}

func verifyVolumeDetails(payload *cloud.DiskOptions, diskDetails *cloud.Disk) error {
	if payload.Shareable != diskDetails.Shareable {
		return status.Errorf(codes.Internal, "shareable in payload and shareable in disk details don't match")
	}
	if payload.VolumeType != diskDetails.DiskType {
		return status.Errorf(codes.Internal, "TYPE in payload and disktype in disk details don't match")
	}
	capacityGIB := util.BytesToGiB(payload.CapacityBytes)
	if capacityGIB != diskDetails.CapacityGiB {
		return status.Errorf(codes.Internal, "capacityBytes in payload and capacityGIB in disk details don't match")
	}
	return nil
}
