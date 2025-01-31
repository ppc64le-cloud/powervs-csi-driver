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
	"errors"
	"reflect"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
	mocks "sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud/mocks"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/util"
)

const (
	expInstanceID = "i-123456789abcdef01"
)

func TestCreateVolume(t *testing.T) {
	stdVolCap := []*csi.VolumeCapability{
		{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	stdVolSize := int64(5 * 1024 * 1024 * 1024)
	stdCapRange := &csi.CapacityRange{RequiredBytes: stdVolSize}
	stdParams := map[string]string{
		"type": cloud.DefaultVolumeType,
	}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{

		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(stdVolSize),
					DiskType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), gomock.Any()).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "success normal with datasource PVC",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "clone-volume-name",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Volume{
							Volume: &csi.VolumeContentSource_VolumeSource{
								VolumeId: "test-volume-src-100",
							},
						},
					},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(stdVolSize),
					DiskType:    cloud.DefaultVolumeType,
				}
				mockSrcDisk := &cloud.Disk{
					VolumeID:    "test-volume-src-100",
					CapacityGiB: util.BytesToGiB(stdVolSize),
					DiskType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByNamePrefix(gomock.Eq("clone-"+req.Name)).Return(nil, nil)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq(mockSrcDisk.VolumeID)).Return(mockSrcDisk, nil)
				mockCloud.EXPECT().CloneDisk(gomock.Eq(mockSrcDisk.VolumeID), gomock.Eq(req.Name)).Return(mockDisk, nil)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq(mockDisk.VolumeID)).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "Create PVC with Data source - volume already exists",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "clone-volume-name",
					CapacityRange:      &csi.CapacityRange{RequiredBytes: stdVolSize},
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Volume{
							Volume: &csi.VolumeContentSource_VolumeSource{
								VolumeId: "test-volume-src-100",
							},
						},
					},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(stdVolSize),
					DiskType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByNamePrefix(gomock.Eq("clone-"+req.Name)).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:          "random-vol-name",
					CapacityRange: stdCapRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Mount{
								Mount: &csi.VolumeCapability_MountVolume{},
							},
							AccessMode: &csi.VolumeCapability_AccessMode{
								Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
							},
						},
					},
					Parameters: stdParams,
				}

				ctx := context.Background()
				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(stdVolSize),
					Shareable:   true,
				}
				mockDiskOpts := &cloud.DiskOptions{
					Shareable:     true,
					CapacityBytes: stdVolSize,
					VolumeType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), mockDiskOpts).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				_, err := powervsDriver.CreateVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:          "random-vol-name",
					CapacityRange: stdCapRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Mount{
								Mount: &csi.VolumeCapability_MountVolume{},
							},
							AccessMode: &csi.VolumeCapability_AccessMode{
								Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
							},
						},
					},
					Parameters: stdParams,
				}

				ctx := context.Background()
				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(stdVolSize),
					Shareable:   true,
					DiskType:    cloud.DefaultVolumeType,
				}
				mockDiskOpts := &cloud.DiskOptions{
					Shareable:     true,
					CapacityBytes: stdVolSize,
					VolumeType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), mockDiskOpts).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				_, err := powervsDriver.CreateVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:          "random-vol-name",
					CapacityRange: stdCapRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Mount{
								Mount: &csi.VolumeCapability_MountVolume{},
							},
							AccessMode: &csi.VolumeCapability_AccessMode{
								Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
							},
						},
					},
					Parameters: stdParams,
				}

				ctx := context.Background()
				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(stdVolSize),
					Shareable:   false,
					DiskType:    cloud.DefaultVolumeType,
				}
				mockDiskOpts := &cloud.DiskOptions{
					Shareable:     false,
					CapacityBytes: stdVolSize,
					VolumeType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), mockDiskOpts).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				_, err := powervsDriver.CreateVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},

		{
			name: "fail no name",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.InvalidArgument {
						t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.InvalidArgument)
				}
			},
		},

		{
			name: "success same name and same capacity",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "test-vol",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
				}
				extraReq := &csi.CreateVolumeRequest{
					Name:               "test-vol",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
				}
				expVol := &csi.Volume{
					CapacityBytes: stdVolSize,
					VolumeId:      "test-vol",
					VolumeContext: map[string]string{},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(stdVolSize),
					DiskType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), gomock.Any()).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}

				// Subsequent call returns the created disk
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(mockDisk, nil)
				mockCloud.EXPECT().WaitForVolumeState(gomock.Any(), gomock.Any()).Return(nil)
				resp, err := powervsDriver.CreateVolume(ctx, extraReq)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}

				vol := resp.GetVolume()
				if vol == nil {
					t.Fatalf("Expected volume %v, got nil", expVol)
				}

				if vol.GetCapacityBytes() != expVol.GetCapacityBytes() {
					t.Fatalf("Expected volume capacity bytes: %v, got: %v", expVol.GetCapacityBytes(), vol.GetCapacityBytes())
				}

				if vol.GetVolumeId() != expVol.GetVolumeId() {
					t.Fatalf("Expected volume id: %v, got: %v", expVol.GetVolumeId(), vol.GetVolumeId())
				}

				if expVol.GetAccessibleTopology() != nil {
					if !reflect.DeepEqual(expVol.GetAccessibleTopology(), vol.GetAccessibleTopology()) {
						t.Fatalf("Expected AccessibleTopology to be %+v, got: %+v", expVol.GetAccessibleTopology(), vol.GetAccessibleTopology())
					}
				}

				for expKey, expVal := range expVol.GetVolumeContext() {
					ctx := vol.GetVolumeContext()
					if gotVal, ok := ctx[expKey]; !ok || gotVal != expVal {
						t.Fatalf("Expected volume context for key %v: %v, got: %v", expKey, expVal, gotVal)
					}
				}
			},
		},

		{
			name: "success no capacity range",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "test-vol",
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
				}
				expVol := &csi.Volume{
					CapacityBytes: cloud.DefaultVolumeSize,
					VolumeId:      "vol-test",
					VolumeContext: map[string]string{},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(cloud.DefaultVolumeSize),
					DiskType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), gomock.Any()).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				resp, err := powervsDriver.CreateVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}

				vol := resp.GetVolume()
				if vol == nil {
					t.Fatalf("Expected volume %v, got nil", expVol)
				}

				if vol.GetCapacityBytes() != expVol.GetCapacityBytes() {
					t.Fatalf("Expected volume capacity bytes: %v, got: %v", expVol.GetCapacityBytes(), vol.GetCapacityBytes())
				}

				for expKey, expVal := range expVol.GetVolumeContext() {
					ctx := vol.GetVolumeContext()
					if gotVal, ok := ctx[expKey]; !ok || gotVal != expVal {
						t.Fatalf("Expected volume context for key %v: %v, got: %v", expKey, expVal, gotVal)
					}
				}
			},
		},
		{
			name: "success with correct round up",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "vol-test",
					CapacityRange:      &csi.CapacityRange{RequiredBytes: 1073741825},
					VolumeCapabilities: stdVolCap,
					Parameters:         stdParams,
				}
				expVol := &csi.Volume{
					CapacityBytes: 2147483648, // 1 GiB + 1 byte = 2 GiB
					VolumeId:      "vol-test",
					VolumeContext: map[string]string{},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(expVol.CapacityBytes),
					DiskType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), gomock.Any()).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				resp, err := powervsDriver.CreateVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}

				vol := resp.GetVolume()
				if vol == nil {
					t.Fatalf("Expected volume %v, got nil", expVol)
				}

				if vol.GetCapacityBytes() != expVol.GetCapacityBytes() {
					t.Fatalf("Expected volume capacity bytes: %v, got: %v", expVol.GetCapacityBytes(), vol.GetCapacityBytes())
				}
			},
		},
		{
			name: "success with volume type tier1",
			testFunc: func(t *testing.T) {
				// iops 5000 requires at least 10GB
				volSize := int64(20 * 1024 * 1024 * 1024)
				capRange := &csi.CapacityRange{RequiredBytes: volSize}
				req := &csi.CreateVolumeRequest{
					Name:               "vol-test",
					CapacityRange:      capRange,
					VolumeCapabilities: stdVolCap,
					Parameters: map[string]string{
						VolumeTypeKey: cloud.VolumeTypeTier1,
					},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(volSize),
					DiskType:    cloud.VolumeTypeTier1,
				}

				mockDiskOpts := &cloud.DiskOptions{
					CapacityBytes: volSize,
					VolumeType:    cloud.VolumeTypeTier1,
				}
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), mockDiskOpts).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "success with volume type tier3",
			testFunc: func(t *testing.T) {
				// iops 5000 requires at least 10GB
				volSize := int64(20 * 1024 * 1024 * 1024)
				capRange := &csi.CapacityRange{RequiredBytes: volSize}
				req := &csi.CreateVolumeRequest{
					Name:               "vol-test",
					CapacityRange:      capRange,
					VolumeCapabilities: stdVolCap,
					Parameters: map[string]string{
						VolumeTypeKey: cloud.VolumeTypeTier3,
					},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(volSize),
					DiskType:    cloud.VolumeTypeTier3,
				}
				mockDiskOpts := &cloud.DiskOptions{
					CapacityBytes: volSize,
					VolumeType:    cloud.VolumeTypeTier3,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), mockDiskOpts).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "fail with invalid volume parameter",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "vol-test",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters: map[string]string{
						VolumeTypeKey: cloud.VolumeTypeTier1,
						"unknownKey":  "unknownValue",
					},
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				_, err := powervsDriver.CreateVolume(ctx, req)
				if err == nil {
					t.Fatalf("Expected CreateVolume to fail but got no error")
				}

				srvErr, ok := status.FromError(err)
				if !ok {
					t.Fatalf("Could not get error status code from error: %v", srvErr)
				}
				if srvErr.Code() != codes.InvalidArgument {
					t.Fatalf("Expect InvalidArgument but got: %s", srvErr.Code())
				}
			},
		},
		{
			name: "Pass with no volume type parameter",
			testFunc: func(t *testing.T) {
				// iops 5000 requires at least 10GB
				volSize := int64(20 * 1024 * 1024 * 1024)
				capRange := &csi.CapacityRange{RequiredBytes: volSize}
				req := &csi.CreateVolumeRequest{
					Name:               "vol-test",
					CapacityRange:      capRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         map[string]string{},
				}

				ctx := context.Background()

				mockDisk := &cloud.Disk{
					VolumeID:    req.Name,
					CapacityGiB: util.BytesToGiB(volSize),
					DiskType:    cloud.DefaultVolumeType,
				}

				mockDiskOpts := &cloud.DiskOptions{
					CapacityBytes: volSize,
					VolumeType:    cloud.DefaultVolumeType,
				}

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByName(gomock.Eq(req.Name)).Return(nil, nil)
				mockCloud.EXPECT().CreateDisk(gomock.Eq(req.Name), mockDiskOpts).Return(mockDisk, nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.CreateVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
			},
		},
		{
			name: "fail locked volume request",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         nil,
				}

				ctx := context.Background()
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				powervsDriver.volumeLocks.TryAcquire(req.Name)
				defer powervsDriver.volumeLocks.Release(req.Name)

				_, err := powervsDriver.CreateVolume(ctx, req)
				checkExpectedErrorCode(t, err, codes.Aborted)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestDeleteVolume(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "vol-test",
				}
				expResp := &csi.DeleteVolumeResponse{}

				ctx := context.Background()
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().DeleteDisk(gomock.Eq(req.VolumeId)).Return(nil)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq(req.VolumeId)).Return(nil, nil)
				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}
				resp, err := powervsDriver.DeleteVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
				if !reflect.DeepEqual(resp, expResp) {
					t.Fatalf("Expected resp to be %+v, got: %+v", expResp, resp)
				}
			},
		},
		{
			name: "success invalid volume id",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "invalid-volume-name",
				}
				expResp := &csi.DeleteVolumeResponse{}

				ctx := context.Background()
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq(req.VolumeId)).Return(nil, cloud.ErrNotFound)
				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}
				resp, err := powervsDriver.DeleteVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					t.Fatalf("Unexpected error: %v", srvErr.Code())
				}
				if !reflect.DeepEqual(resp, expResp) {
					t.Fatalf("Expected resp to be %+v, got: %+v", expResp, resp)
				}
			},
		},
		{
			name: "fail delete disk",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "test-vol",
				}

				ctx := context.Background()
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().DeleteDisk(gomock.Eq(req.VolumeId)).Return(errors.New("DeleteDisk could not delete volume"))
				mockCloud.EXPECT().GetDiskByID(gomock.Eq(req.VolumeId)).Return(nil, nil)
				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}
				resp, err := powervsDriver.DeleteVolume(ctx, req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.Internal {
						t.Fatalf("Unexpected error: %v", srvErr.Code())
					}
				} else {
					t.Fatalf("Expected error, got nil")
				}

				if resp != nil {
					t.Fatalf("Expected resp to be nil, got: %+v", resp)
				}
			},
		},
		{
			name: "fail if volume is already locked",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "vol-test",
				}

				ctx := context.Background()
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				powervsDriver.volumeLocks.TryAcquire(req.VolumeId)
				defer powervsDriver.volumeLocks.Release(req.VolumeId)

				_, err := powervsDriver.DeleteVolume(ctx, req)
				checkExpectedErrorCode(t, err, codes.Aborted)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestControllerPublishVolume(t *testing.T) {
	stdVolCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	expDevicePath := "/dev/xvda"
	volumeName := "vol-test"

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{

		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{
					NodeId:           expInstanceID,
					VolumeCapability: stdVolCap,
					VolumeId:         volumeName,
				}
				expResp := &csi.ControllerPublishVolumeResponse{
					PublishContext: map[string]string{WWNKey: expDevicePath},
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetPVMInstanceByID(gomock.Eq(expInstanceID)).Return(nil, nil)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq(volumeName)).Return(&cloud.Disk{WWN: expDevicePath}, nil)
				mockCloud.EXPECT().IsAttached(gomock.Eq(volumeName), gomock.Eq(expInstanceID)).Return(errors.New("the disk is unattached"))
				mockCloud.EXPECT().AttachDisk(gomock.Eq(volumeName), gomock.Eq(expInstanceID)).Return(nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				resp, err := powervsDriver.ControllerPublishVolume(ctx, req)
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}

				if !reflect.DeepEqual(resp, expResp) {
					t.Fatalf("Expected resp to be %+v, got: %+v", expResp, resp)
				}
			},
		},

		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerPublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.InvalidArgument {
						t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.InvalidArgument)
				}
			},
		},

		{
			name: "fail no NodeId",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{
					VolumeId: "vol-test",
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerPublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.InvalidArgument {
						t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.InvalidArgument)
				}
			},
		},

		{
			name: "fail no VolumeCapability",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{
					NodeId:   expInstanceID,
					VolumeId: "vol-test",
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerPublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.InvalidArgument {
						t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.InvalidArgument)
				}
			},
		},

		{
			name: "fail invalid VolumeCapability",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{
					NodeId: expInstanceID,
					VolumeCapability: &csi.VolumeCapability{
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
						},
					},
					VolumeId: "vol-test",
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerPublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.InvalidArgument {
						t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.InvalidArgument)
				}
			},
		},

		{
			name: "fail instance not found",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{
					NodeId:           "does-not-exist",
					VolumeId:         "vol-test",
					VolumeCapability: stdVolCap,
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetPVMInstanceByID(gomock.Eq("does-not-exist")).Return(nil, cloud.ErrNotFound)
				// mockCloud.EXPECT().IsExistInstance(gomock.Eq(ctx), gomock.Eq(req.NodeId)).Return(false)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerPublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.NotFound {
						t.Fatalf("Expected error code %d, got %d message %s", codes.NotFound, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.NotFound)
				}
			},
		},

		{
			name: "fail volume not found",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{
					VolumeId:         "does-not-exist",
					NodeId:           expInstanceID,
					VolumeCapability: stdVolCap,
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetPVMInstanceByID(gomock.Eq(expInstanceID)).Return(nil, nil)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq("does-not-exist")).Return(&cloud.Disk{}, cloud.ErrNotFound)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerPublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.NotFound {
						t.Fatalf("Expected error code %d, got %d message %s", codes.NotFound, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.NotFound)
				}
			},
		},
		{
			name: "fail if volume already locked",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerPublishVolumeRequest{
					NodeId:           expInstanceID,
					VolumeCapability: stdVolCap,
					VolumeId:         volumeName,
				}

				ctx := context.Background()
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				powervsDriver.volumeLocks.TryAcquire(req.VolumeId)
				defer powervsDriver.volumeLocks.Release(req.VolumeId)

				_, err := powervsDriver.ControllerPublishVolume(ctx, req)
				checkExpectedErrorCode(t, err, codes.Aborted)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestControllerUnpublishVolume(t *testing.T) {
	expDevicePath := "/dev/xvda"
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerUnpublishVolumeRequest{
					NodeId:   expInstanceID,
					VolumeId: "vol-test",
				}
				expResp := &csi.ControllerUnpublishVolumeResponse{}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq("vol-test")).Return(&cloud.Disk{WWN: expDevicePath}, nil)
				mockCloud.EXPECT().IsAttached(gomock.Eq("vol-test"), gomock.Eq(expInstanceID)).Return(nil)
				mockCloud.EXPECT().DetachDisk(req.VolumeId, req.NodeId).Return(nil)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				resp, err := powervsDriver.ControllerUnpublishVolume(ctx, req)
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}

				if !reflect.DeepEqual(resp, expResp) {
					t.Fatalf("Expected resp to be %+v, got: %+v", expResp, resp)
				}
			},
		},
		{
			name: "success when resource is not found",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerUnpublishVolumeRequest{
					NodeId:   expInstanceID,
					VolumeId: "vol-test",
				}
				expResp := &csi.ControllerUnpublishVolumeResponse{}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				mockCloud.EXPECT().GetDiskByID(gomock.Eq("vol-test")).Return(&cloud.Disk{WWN: expDevicePath}, cloud.ErrNotFound)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}
				resp, err := powervsDriver.ControllerUnpublishVolume(ctx, req)
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}

				if !reflect.DeepEqual(resp, expResp) {
					t.Fatalf("Expected resp to be %+v, got: %+v", expResp, resp)
				}
			},
		},

		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerUnpublishVolumeRequest{}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerUnpublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.InvalidArgument {
						t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.InvalidArgument)
				}
			},
		},
		{
			name: "fail no NodeId",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerUnpublishVolumeRequest{
					VolumeId: "vol-test",
				}

				ctx := context.Background()

				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)

				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				if _, err := powervsDriver.ControllerUnpublishVolume(ctx, req); err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if srvErr.Code() != codes.InvalidArgument {
						t.Fatalf("Expected error code %d, got %d message %s", codes.InvalidArgument, srvErr.Code(), srvErr.Message())
					}
				} else {
					t.Fatalf("Expected error %v, got no error", codes.InvalidArgument)
				}
			},
		},
		{
			name: "fail if volume already locked",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerUnpublishVolumeRequest{
					NodeId:   expInstanceID,
					VolumeId: "vol-test",
				}

				ctx := context.Background()
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()

				mockCloud := mocks.NewMockCloud(mockCtl)
				powervsDriver := controllerService{
					cloud:         mockCloud,
					driverOptions: &Options{},
					volumeLocks:   util.NewVolumeLocks(),
				}

				powervsDriver.volumeLocks.TryAcquire(req.VolumeId)
				defer powervsDriver.volumeLocks.Release(req.VolumeId)

				_, err := powervsDriver.ControllerUnpublishVolume(ctx, req)
				checkExpectedErrorCode(t, err, codes.Aborted)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestControllerExpandVolume(t *testing.T) {
	testCases := []struct {
		name       string
		req        *csi.ControllerExpandVolumeRequest
		newSize    int64
		expResp    *csi.ControllerExpandVolumeResponse
		expError   bool
		volumeLock bool
	}{
		{
			name: "success normal",
			req: &csi.ControllerExpandVolumeRequest{
				VolumeId: "vol-test",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 5 * util.GiB,
				},
			},
			expResp: &csi.ControllerExpandVolumeResponse{
				CapacityBytes: 5 * util.GiB,
			},
		},
		{
			name:     "fail empty request",
			req:      &csi.ControllerExpandVolumeRequest{},
			expError: true,
		},
		{
			name: "fail exceeds limit after round up",
			req: &csi.ControllerExpandVolumeRequest{
				VolumeId: "vol-test",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 5*util.GiB + 1, // should round up to 6 GiB
					LimitBytes:    5 * util.GiB,
				},
			},
			expError: true,
		},
		{
			name: "fail if volume already locked",
			req: &csi.ControllerExpandVolumeRequest{
				VolumeId: "vol-test",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 5 * util.GiB,
				},
			},
			volumeLock: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mockCtl := gomock.NewController(t)
			defer mockCtl.Finish()

			var retSizeGiB int64
			if tc.newSize != 0 {
				retSizeGiB = tc.newSize
			} else {
				retSizeGiB = util.BytesToGiB(tc.req.CapacityRange.GetRequiredBytes())
			}

			mockCloud := mocks.NewMockCloud(mockCtl)
			powervsDriver := controllerService{
				cloud:         mockCloud,
				driverOptions: &Options{},
				volumeLocks:   util.NewVolumeLocks(),
			}

			if tc.volumeLock {
				powervsDriver.volumeLocks.TryAcquire(tc.req.VolumeId)
				defer powervsDriver.volumeLocks.Release(tc.req.VolumeId)

				_, err := powervsDriver.ControllerExpandVolume(ctx, tc.req)
				checkExpectedErrorCode(t, err, codes.Aborted)
			} else {
				mockCloud.EXPECT().ResizeDisk(gomock.Eq(tc.req.VolumeId), gomock.Any()).Return(retSizeGiB, nil).AnyTimes()
				resp, err := powervsDriver.ControllerExpandVolume(ctx, tc.req)
				if err != nil {
					srvErr, ok := status.FromError(err)
					if !ok {
						t.Fatalf("Could not get error status code from error: %v", srvErr)
					}
					if !tc.expError {
						t.Fatalf("Unexpected error: %v", err)
					}
				} else {
					if tc.expError {
						t.Fatalf("Expected error from ControllerExpandVolume, got nothing")
					}
				}

				sizeGiB := util.BytesToGiB(resp.GetCapacityBytes())
				expSizeGiB := util.BytesToGiB(tc.expResp.GetCapacityBytes())
				if sizeGiB != expSizeGiB {
					t.Fatalf("Expected size %d GiB, got %d GiB", expSizeGiB, sizeGiB)
				}
			}
		})
	}
}

func TestIsShareableVolume(t *testing.T) {
	testCases := []struct {
		name              string
		expectedShareable bool
		testVolCap        []*csi.VolumeCapability
	}{
		{
			name:              "Test csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER",
			expectedShareable: false,
			testVolCap: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
		},
		{
			name:              "Test csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER",
			expectedShareable: true,
			testVolCap: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
		},
		{
			name:              "Test csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY",
			expectedShareable: true,
			testVolCap: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if res := isShareableVolume(tc.testVolCap); res != tc.expectedShareable {
				t.Fatalf("Volume capability: %s expected value for shareable : %t got: %t", tc.testVolCap[0].AccessMode.GetMode().String(), tc.expectedShareable, res)
			}
		})
	}
}

func checkExpectedErrorCode(t *testing.T, err error, expectedCode codes.Code) {
	if err == nil {
		t.Fatalf("Expected operation to fail but got no error")
	}

	srvErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Could not get error status code from error: %v", srvErr)
	}
	if srvErr.Code() != expectedCode {
		t.Fatalf("Expected Aborted but got: %s", srvErr.Code())
	}
}
