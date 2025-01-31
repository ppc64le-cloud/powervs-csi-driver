/*
Copyright 2021 The Kubernetes Authors.
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
	"errors"
	"reflect"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"go.uber.org/mock/gomock"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
	cloudmocks "sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud/mocks"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/device"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/driver/mocks"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/util"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// constants of keys in PublishContext.
const (
	// devicePathKey represents key for device path in PublishContext.
	// devicePath is the device path where the volume is attached to.
	DevicePathKey = "devicePath"
)

// constants of keys in VolumeContext.
const (
	// VolumeAttributePartition represents key for partition config in VolumeContext.
	// It represents the partition number on a device used to mount.
	VolumeAttributePartition = "partition"
	testDir                  = "./test"
)

var (
	volumeID = "voltest"
)

func TestNodeStageVolume(t *testing.T) {
	var (
		targetPath = "/tmp/test/path"
		devicePath = "/dev/fake"
		volumeWWN  = "fakewwn"

		stdVolCap = &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{
					FsType: FSTypeExt4,
				},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		}

		commonExpectMock = func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice) {
			// always use mocked device
			NewDevice = func(wwn string) device.LinuxDevice {
				return mockDevice
			}
			mockDevice.EXPECT().Populate(false).Return(nil)
			mockDevice.EXPECT().GetMapper().Return(devicePath).MinTimes(2)
			mockMounter.EXPECT().IsLikelyNotMountPoint(gomock.Any()).Return(true, nil)
		}
	)
	testCases := []struct {
		name         string
		request      *csi.NodeStageVolumeRequest
		expectMock   func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice)
		expectedCode codes.Code
		volumeLock   bool
	}{

		{
			name: "success normal",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability:  stdVolCap,
				VolumeId:          volumeID,
			},
			expectMock: func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice) {
				commonExpectMock(mockMounter, mockDevice)
				mockMounter.EXPECT().FormatAndMount(gomock.Eq(devicePath), gomock.Eq(targetPath), gomock.Any(), gomock.Any()).Return(nil)
				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(targetPath, 1, nil)
				mockMounter.EXPECT().ExistsPath(gomock.Any()).Return(true, nil)
			},
		},

		{
			name: "success normal [raw block]",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
				VolumeId: volumeID,
			},
			expectMock: func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice) {
				// always use mocked device
				NewDevice = func(wwn string) device.LinuxDevice {
					return mockDevice
				}

				mockMounter.EXPECT().IsLikelyNotMountPoint(gomock.Any()).Return(true, nil)
				mockMounter.EXPECT().FormatAndMount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			},
		},

		{
			name: "success with mount options",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							MountFlags: []string{"dirsync", "noexec"},
						},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
				VolumeId: volumeID,
			},
			expectMock: func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice) {
				commonExpectMock(mockMounter, mockDevice)
				mockMounter.EXPECT().FormatAndMount(gomock.Eq(devicePath), gomock.Eq(targetPath), gomock.Eq(FSTypeExt4), gomock.Eq([]string{"dirsync", "noexec"}))
				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(targetPath, 1, nil)
				mockMounter.EXPECT().ExistsPath(gomock.Any()).Return(true, nil)
			},
		},

		{
			name: "success fsType ext3",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: FSTypeExt3,
						},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
				VolumeId: volumeID,
			},
			expectMock: func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice) {
				commonExpectMock(mockMounter, mockDevice)
				mockMounter.EXPECT().FormatAndMount(gomock.Eq(devicePath), gomock.Eq(targetPath), gomock.Eq(FSTypeExt3), gomock.Any())
				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(targetPath, 1, nil)
				mockMounter.EXPECT().ExistsPath(gomock.Any()).Return(true, nil)
			},
		},

		{
			name: "success mount with default fsType ext4",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: "",
						},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
				VolumeId: volumeID,
			},
			expectMock: func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice) {
				commonExpectMock(mockMounter, mockDevice)
				mockMounter.EXPECT().FormatAndMount(gomock.Eq(devicePath), gomock.Eq(targetPath), gomock.Eq(FSTypeExt4), gomock.Any())
				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(targetPath, 1, nil)
				mockMounter.EXPECT().ExistsPath(gomock.Any()).Return(true, nil)
			},
		},

		{
			name: "success device already mounted at target",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability:  stdVolCap,
				VolumeId:          volumeID,
			},
			expectMock: func(mockMounter *mocks.MockMounter, mockDevice *mocks.MockLinuxDevice) {
				commonExpectMock(mockMounter, mockDevice)
				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(devicePath, 1, nil)
				mockMounter.EXPECT().FormatAndMount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			},
		},

		{
			name: "fail no VolumeId",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability:  stdVolCap,
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "fail no mount",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "fail no StagingTargetPath",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:   map[string]string{WWNKey: volumeWWN},
				VolumeCapability: stdVolCap,
				VolumeId:         volumeID,
			},
			expectedCode: codes.InvalidArgument,
		},

		{
			name: "fail no VolumeCapability",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeId:          volumeID,
			},
			expectedCode: codes.InvalidArgument,
		},

		{
			name: "fail invalid VolumeCapability",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
					},
				},
				VolumeId: volumeID,
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "fail no devicePath",
			request: &csi.NodeStageVolumeRequest{
				VolumeCapability: stdVolCap,
				VolumeId:         volumeID,
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "fail_if_volume_already_locked",
			request: &csi.NodeStageVolumeRequest{
				PublishContext:    map[string]string{WWNKey: volumeWWN},
				StagingTargetPath: targetPath,
				VolumeCapability:  stdVolCap,
				VolumeId:          volumeID,
			},
			volumeLock:   true,
			expectedCode: codes.Aborted,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtl := gomock.NewController(t)
			defer mockCtl.Finish()

			mockMounter := mocks.NewMockMounter(mockCtl)
			mockDevice := mocks.NewMockLinuxDevice(mockCtl)

			powervsDriver := &nodeService{
				mounter:     mockMounter,
				volumeLocks: util.NewVolumeLocks(),
			}

			if tc.volumeLock {
				powervsDriver.volumeLocks.TryAcquire(tc.request.VolumeId)
				defer powervsDriver.volumeLocks.Release(tc.request.VolumeId)
			}

			if tc.expectMock != nil {
				tc.expectMock(mockMounter, mockDevice)
			}

			_, err := powervsDriver.NodeStageVolume(context.TODO(), tc.request)
			if tc.expectedCode != codes.OK {
				expectErr(t, err, tc.expectedCode)
			} else if err != nil {
				t.Fatalf("Expect no error but got: %v", err)
			}
		})
	}
}

func TestNodeUnstageVolume(t *testing.T) {
	targetPath := "/test/path"
	devicePath := "/dev/fake"

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)
				mockDevice := mocks.NewMockLinuxDevice(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(devicePath, 1, nil)
				mockMounter.EXPECT().Unmount(gomock.Eq(targetPath)).Return(nil)
				mockMounter.EXPECT().IsMountPoint(gomock.Eq(targetPath)).Return(false, nil)

				mockDevice.EXPECT().GetMapper().Return(devicePath)
				mockDevice.EXPECT().Populate(false).Return(nil)
				mockDevice.EXPECT().DeleteDevice().Return(nil)

				req := &csi.NodeUnstageVolumeRequest{
					StagingTargetPath: targetPath,
					VolumeId:          volumeID,
				}

				GetDeviceWWN = func(pathName string) (string, error) {
					return "fakeWWN", nil
				}
				// always use mocked device
				NewDevice = func(wwn string) device.LinuxDevice {
					return mockDevice
				}

				_, err := powervsDriver.NodeUnstageVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success no device mounted at target",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}
				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(devicePath, 0, nil)

				req := &csi.NodeUnstageVolumeRequest{
					StagingTargetPath: targetPath,
					VolumeId:          volumeID,
				}
				_, err := powervsDriver.NodeUnstageVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success device mounted at multiple targets",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)
				mockDevice := mocks.NewMockLinuxDevice(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return(devicePath, 2, nil)
				mockMounter.EXPECT().Unmount(gomock.Eq(targetPath)).Return(nil)
				mockMounter.EXPECT().IsMountPoint(gomock.Eq(targetPath)).Return(false, nil)

				mockDevice.EXPECT().GetMapper().Return(devicePath)
				mockDevice.EXPECT().Populate(false).Return(nil)
				mockDevice.EXPECT().DeleteDevice().Return(nil)

				GetDeviceWWN = func(pathName string) (string, error) {
					return "fakeWWN", nil
				}
				// always use mocked device
				NewDevice = func(wwn string) device.LinuxDevice {
					return mockDevice
				}

				req := &csi.NodeUnstageVolumeRequest{
					StagingTargetPath: targetPath,
					VolumeId:          volumeID,
				}

				_, err := powervsDriver.NodeUnstageVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				req := &csi.NodeUnstageVolumeRequest{
					StagingTargetPath: targetPath,
				}

				_, err := powervsDriver.NodeUnstageVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no StagingTargetPath",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				req := &csi.NodeUnstageVolumeRequest{
					VolumeId: volumeID,
				}
				_, err := powervsDriver.NodeUnstageVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail GetDeviceName returns error",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				mockMounter.EXPECT().GetDeviceName(gomock.Eq(targetPath)).Return("", 0, errors.New("GetDeviceName failed"))

				req := &csi.NodeUnstageVolumeRequest{
					StagingTargetPath: targetPath,
					VolumeId:          volumeID,
				}

				_, err := powervsDriver.NodeUnstageVolume(context.TODO(), req)
				expectErr(t, err, codes.Internal)
			},
		},
		{
			name: "fail if volume is already locked",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				req := &csi.NodeUnstageVolumeRequest{
					StagingTargetPath: targetPath,
					VolumeId:          volumeID,
				}
				powervsDriver.volumeLocks.TryAcquire(req.VolumeId)
				defer powervsDriver.volumeLocks.Release(req.VolumeId)

				_, err := powervsDriver.NodeUnstageVolume(context.TODO(), req)
				expectErr(t, err, codes.Aborted)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodeExpandVolume(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	mockMounter := mocks.NewMockMounter(mockCtl)

	powervsDriver := &nodeService{
		mounter:     mockMounter,
		volumeLocks: util.NewVolumeLocks(),
	}

	tests := []struct {
		name               string
		request            *csi.NodeExpandVolumeRequest
		expectResponseCode codes.Code
		expectMock         func(mockMounter mocks.MockMounter)
		volumeLock         bool
	}{
		{
			name:               "fail missing volumeId",
			request:            &csi.NodeExpandVolumeRequest{},
			expectResponseCode: codes.InvalidArgument,
		},
		{
			name: "fail if volume already locked",
			request: &csi.NodeExpandVolumeRequest{
				VolumeId: "test-volume-id",
			},
			expectResponseCode: codes.InvalidArgument,
			volumeLock:         true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.expectMock != nil {
				test.expectMock(*mockMounter)
			}

			if test.volumeLock {
				powervsDriver.volumeLocks.TryAcquire(test.request.VolumeId)
				defer powervsDriver.volumeLocks.Release(test.request.VolumeId)
			}
			_, err := powervsDriver.NodeExpandVolume(context.Background(), test.request)
			if err != nil {
				if test.expectResponseCode != codes.OK {
					expectErr(t, err, test.expectResponseCode)
				} else {
					t.Fatalf("Expect no error but got: %v", err)
				}
			}
		})
	}
}

func TestNodePublishVolume(t *testing.T) {
	targetPath := "/tmp/test/path"
	stagingTargetPath := "/tmp/test/staging/path"
	devicePath := "/dev/fake"
	wwnValue := "testwwn12"
	stdVolCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{
			Block: &csi.VolumeCapability_BlockVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}

	stdVolContext := map[string]string{"partition": "1"}
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success normal [raw block]",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)
				mockDevice := mocks.NewMockLinuxDevice(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				mockMounter.EXPECT().ExistsPath(gomock.Any()).Return(true, nil).Times(2)
				mockMounter.EXPECT().MakeFile(targetPath).Return(nil)
				mockMounter.EXPECT().Mount(devicePath, targetPath, "", gomock.Any()).Return(nil)

				// always use mocked device
				NewDevice = func(wwn string) device.LinuxDevice {
					return mockDevice
				}

				mockDevice.EXPECT().GetMapper().Return(devicePath).MinTimes(2)
				mockDevice.EXPECT().Populate(false).Return(nil)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  stdVolCap,
					VolumeId:          volumeID,
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success normal with partition [raw block]",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)
				mockDevice := mocks.NewMockLinuxDevice(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				mockMounter.EXPECT().ExistsPath(gomock.Any()).Return(true, nil).Times(2)
				mockMounter.EXPECT().MakeFile(targetPath).Return(nil)
				mockMounter.EXPECT().Mount(devicePath, targetPath, "", gomock.Any()).Return(nil)

				// always use mocked device
				NewDevice = func(wwn string) device.LinuxDevice {
					return mockDevice
				}

				mockDevice.EXPECT().GetMapper().Return(devicePath).MinTimes(2)
				mockDevice.EXPECT().Populate(false).Return(nil)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  stdVolCap,
					VolumeId:          volumeID,
					VolumeContext:     stdVolContext,
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success normal with invalid partition config, will ignore the config [raw block]",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)
				mockDevice := mocks.NewMockLinuxDevice(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				mockMounter.EXPECT().ExistsPath(gomock.Any()).Return(true, nil).Times(2)
				mockMounter.EXPECT().MakeFile(targetPath).Return(nil)
				mockMounter.EXPECT().Mount(devicePath, targetPath, "", gomock.Any()).Return(nil)

				// always use mocked device
				NewDevice = func(wwn string) device.LinuxDevice {
					return mockDevice
				}

				mockDevice.EXPECT().GetMapper().Return(devicePath).MinTimes(2)
				mockDevice.EXPECT().Populate(false).Return(nil)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  stdVolCap,
					VolumeId:          volumeID,
					VolumeContext:     map[string]string{VolumeAttributePartition: "0"},
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "fail no device path [raw block]",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				req := &csi.NodePublishVolumeRequest{
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  stdVolCap,
					VolumeId:          volumeID,
					VolumeContext:     map[string]string{VolumeAttributePartition: "partition1"},
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  stdVolCap,
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no StagingTargetPath",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}
				req := &csi.NodePublishVolumeRequest{
					PublishContext:   map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					TargetPath:       targetPath,
					VolumeCapability: stdVolCap,
					VolumeId:         volumeID,
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no VolumeCapability",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					TargetPath:        targetPath,
					StagingTargetPath: stagingTargetPath,
					VolumeId:          volumeID,
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no TargetPath",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					StagingTargetPath: stagingTargetPath,
					VolumeCapability:  stdVolCap,
					VolumeId:          volumeID,
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail invalid VolumeCapability",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{DevicePathKey: devicePath, WWNKey: wwnValue},
					TargetPath:        targetPath,
					StagingTargetPath: stagingTargetPath,
					VolumeCapability: &csi.VolumeCapability{
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
						},
					},
					VolumeId: volumeID,
				}

				_, err := powervsDriver.NodePublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodeUnpublishVolume(t *testing.T) {
	targetPath := "/test/path"

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: targetPath,
					VolumeId:   "vol-test",
				}

				mockMounter.EXPECT().Unmount(targetPath).Return(nil)
				_, err := powervsDriver.NodeUnpublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},

		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter: mockMounter,
				}

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: targetPath,
				}

				_, err := powervsDriver.NodeUnpublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no TargetPath",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter: mockMounter,
				}

				req := &csi.NodeUnpublishVolumeRequest{
					VolumeId: "vol-test",
				}

				_, err := powervsDriver.NodeUnpublishVolume(context.TODO(), req)
				expectErr(t, err, codes.InvalidArgument)
			},
		},

		{
			name: "fail error on unmount",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: targetPath,
					VolumeId:   "vol-test",
				}

				mockMounter.EXPECT().Unmount(targetPath).Return(errors.New("test Unmount error"))
				_, err := powervsDriver.NodeUnpublishVolume(context.TODO(), req)
				expectErr(t, err, codes.Internal)
			},
		},
		{
			name: "fail if volume already locked",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockMounter := mocks.NewMockMounter(mockCtl)

				powervsDriver := &nodeService{
					mounter:     mockMounter,
					volumeLocks: util.NewVolumeLocks(),
				}

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: targetPath,
					VolumeId:   "vol-test",
				}

				powervsDriver.volumeLocks.TryAcquire(req.VolumeId)
				defer powervsDriver.volumeLocks.Release(req.VolumeId)

				_, err := powervsDriver.NodeUnpublishVolume(context.TODO(), req)
				checkExpectedErrorCode(t, err, codes.Aborted)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodeGetCapabilities(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	mockMounter := mocks.NewMockMounter(mockCtl)

	powervsDriver := nodeService{
		mounter: mockMounter,
	}

	caps := []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
				},
			},
		},
	}
	expResp := &csi.NodeGetCapabilitiesResponse{Capabilities: caps}

	req := &csi.NodeGetCapabilitiesRequest{}
	resp, err := powervsDriver.NodeGetCapabilities(context.TODO(), req)
	if err != nil {
		srvErr, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Could not get error status code from error: %v", srvErr)
		}
		t.Fatalf("Expected nil error, got %d message %s", srvErr.Code(), srvErr.Message())
	}
	if !reflect.DeepEqual(expResp, resp) {
		t.Fatalf("Expected response {%+v}, got {%+v}", expResp, resp)
	}
}

func TestNodeGetInfo(t *testing.T) {
	testCases := []struct {
		name              string
		instanceID        string
		instanceType      string
		availabilityZone  string
		volumeAttachLimit int64
		expMaxVolumes     int64
	}{
		{
			name:              "success normal",
			instanceID:        "i-123456789abcdef01",
			instanceType:      "t2.medium",
			availabilityZone:  "us-west-2b",
			volumeAttachLimit: 30,
			expMaxVolumes:     30,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtl := gomock.NewController(t)
			defer mockCtl.Finish()

			driverOptions := &Options{
				volumeAttachLimit: tc.volumeAttachLimit,
			}

			mockMounter := mocks.NewMockMounter(mockCtl)
			mockCloud := cloudmocks.NewMockCloud(mockCtl)

			mockCloud.EXPECT().GetPVMInstanceByID(tc.instanceID).Return(&cloud.PVMInstance{
				ID:       tc.instanceID,
				Name:     tc.name,
				DiskType: "tier3",
			}, nil)

			powervsDriver := &nodeService{
				mounter:       mockMounter,
				driverOptions: driverOptions,
				cloud:         mockCloud,
				pvmInstanceId: tc.instanceID,
			}

			resp, err := powervsDriver.NodeGetInfo(context.TODO(), &csi.NodeGetInfoRequest{})
			if err != nil {
				srvErr, ok := status.FromError(err)
				if !ok {
					t.Fatalf("Could not get error status code from error: %v", srvErr)
				}
				t.Fatalf("Expected nil error, got %d message %s", srvErr.Code(), srvErr.Message())
			}

			if resp.GetNodeId() != tc.instanceID {
				t.Fatalf("Expected node ID %q, got %q", tc.instanceID, resp.GetNodeId())
			}

			if resp.GetMaxVolumesPerNode() != tc.expMaxVolumes {
				t.Fatalf("Expected %d max volumes per node, got %d", tc.expMaxVolumes, resp.GetMaxVolumesPerNode())
			}
		})
	}
}

func TestNodeGetVolumeStats(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{name: "success block device volume",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockStatUtils := mocks.NewMockStatsUtils(mockCtl)

				volumePath := testDir
				var mockCapacity int64 = 100
				mockStatUtils.EXPECT().IsPathNotExist(volumePath).Return(false)
				mockStatUtils.EXPECT().IsBlockDevice(volumePath).Return(true, nil)
				mockStatUtils.EXPECT().DeviceInfo(volumePath).Return(mockCapacity, nil)
				driver := &nodeService{stats: mockStatUtils}

				req := csi.NodeGetVolumeStatsRequest{VolumeId: volumeID, VolumePath: volumePath}

				resp, err := driver.NodeGetVolumeStats(context.TODO(), &req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}

				if resp.Usage[0].Total != mockCapacity {
					t.Fatalf("Expected total capacity as %d, got %d", mockCapacity, resp.Usage[0].Total)
				}
			},
		}, {
			name: "failure path not exist",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockStatUtils := mocks.NewMockStatsUtils(mockCtl)

				volumePath := testDir
				mockStatUtils.EXPECT().IsPathNotExist(volumePath).Return(true)
				driver := &nodeService{stats: mockStatUtils}

				req := csi.NodeGetVolumeStatsRequest{VolumeId: volumeID, VolumePath: volumePath}

				_, err := driver.NodeGetVolumeStats(context.TODO(), &req)
				expectErr(t, err, codes.NotFound)
			},
		}, {
			name: "failure checking for block device",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockStatUtils := mocks.NewMockStatsUtils(mockCtl)

				volumePath := testDir
				mockStatUtils.EXPECT().IsPathNotExist(volumePath).Return(false)
				mockStatUtils.EXPECT().IsBlockDevice(volumePath).Return(false, errors.New("Error checking for block device"))
				driver := &nodeService{stats: mockStatUtils}

				req := csi.NodeGetVolumeStatsRequest{VolumeId: volumeID, VolumePath: volumePath}

				_, err := driver.NodeGetVolumeStats(context.TODO(), &req)
				expectErr(t, err, codes.Internal)
			},
		}, {
			name: "failure collecting block device info",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockStatUtils := mocks.NewMockStatsUtils(mockCtl)

				volumePath := "testDir"
				mockStatUtils.EXPECT().IsPathNotExist(volumePath).Return(false)
				mockStatUtils.EXPECT().IsBlockDevice(volumePath).Return(true, nil)
				mockStatUtils.EXPECT().DeviceInfo(volumePath).Return(int64(0), errors.New("Error collecting block device info"))

				driver := &nodeService{stats: mockStatUtils}

				req := csi.NodeGetVolumeStatsRequest{VolumeId: volumeID, VolumePath: volumePath}

				_, err := driver.NodeGetVolumeStats(context.TODO(), &req)
				expectErr(t, err, codes.Internal)
			},
		},
		{
			name: "failure collecting fs info",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockStatUtils := mocks.NewMockStatsUtils(mockCtl)

				volumePath := "testDir"
				mockStatUtils.EXPECT().IsPathNotExist(volumePath).Return(false)
				mockStatUtils.EXPECT().IsBlockDevice(volumePath).Return(false, nil)
				var statUnit int64 = 0
				mockStatUtils.EXPECT().FSInfo(volumePath).Return(statUnit, statUnit, statUnit, statUnit, statUnit, statUnit, errors.New("Error collecting FS Info"))
				driver := &nodeService{stats: mockStatUtils}

				req := csi.NodeGetVolumeStatsRequest{VolumeId: volumeID, VolumePath: volumePath}

				_, err := driver.NodeGetVolumeStats(context.TODO(), &req)

				expectErr(t, err, codes.Internal)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func expectErr(t *testing.T, actualErr error, expectedCode codes.Code) {
	if actualErr == nil {
		t.Fatalf("Expect error but got no error")
	}

	status, ok := status.FromError(actualErr)
	if !ok {
		t.Fatalf("Failed to get error status code from error: %v", actualErr)
	}

	if status.Code() != expectedCode {
		t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, status.Code(), status.Message())
	}
}
