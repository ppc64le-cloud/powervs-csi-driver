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

package e2e

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"

	powervscloud "sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
	powervscsidriver "sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/driver"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/tests/e2e/driver"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/tests/e2e/testsuites"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	defaultDiskSize   = 4
	defaultVolumeType = powervscloud.VolumeTypeTier3
	debug             = false
	apiKeyEnv         = "IBMCLOUD_API_KEY"
)

var (
	defaultDiskSizeBytes int64 = defaultDiskSize * 1024 * 1024 * 1024
)

var _ = Describe("[powervs-csi-e2e]Pre-Provisioned", func() {
	f := framework.NewDefaultFramework("powervs")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs                         clientset.Interface
		ns                         *v1.Namespace
		cloud                      powervscloud.Cloud
		volumeID                   string
		diskSize                   string
		powervsDriver              driver.PreProvisionedVolumeTestDriver
		pvTestDriver               driver.PVTestDriver
		skipManuallyDeletingVolume bool
	)

	BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace
		powervsDriver = driver.InitPowervsCSIDriver()
		diskOptions := &powervscloud.DiskOptions{
			CapacityBytes: defaultDiskSizeBytes,
			VolumeType:    defaultVolumeType,
			Shareable:     false,
		}

		// setup PowerVS volume
		if os.Getenv(apiKeyEnv) == "" {
			Skip(fmt.Sprintf("env %q not set", apiKeyEnv))
		}
		metadata, err := testsuites.GetInstanceMetadataFromNodeSpec(cs)
		if err != nil {
			Skip(fmt.Sprintf("Could not get cloudInstanceId : %v", err))
		}

		cloud, err = powervscloud.NewPowerVSCloud(metadata.GetCloudInstanceId(), metadata.GetZone(), debug)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Client creation for instances and volumes has failed. err: %v", err))

		r1 := rand.New(rand.NewSource(time.Now().UnixNano()))
		disk, err := cloud.CreateDisk(fmt.Sprintf("pvc-%d", r1.Uint64()), diskOptions)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Disk creation has failed. err: %v", err))

		volumeID = disk.VolumeID
		diskSize = fmt.Sprintf("%dGi", defaultDiskSize)
		pvTestDriver = driver.InitPowervsCSIDriver()
	})

	AfterEach(func() {
		skipManuallyDeletingVolume = true
		if !skipManuallyDeletingVolume {
			err := cloud.WaitForVolumeState(volumeID, "detached")
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Detach volume has failed. err: %v", err))
			err = cloud.DeleteDisk(volumeID)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Disk deletion has failed. err: %v", err))
		}
	})

	It("[env][labels] should write and read to a pre-provisioned volume", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeID:  volumeID,
						ClaimSize: diskSize,
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.PreProvisionedVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It("[env] should use a pre-provisioned volume and mount it as readOnly in a pod", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeID:  volumeID,
						FSType:    powervscsidriver.FSTypeExt4,
						ClaimSize: diskSize,
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
							ReadOnly:          true,
						},
					},
				},
			},
		}
		test := testsuites.PreProvisionedReadOnlyVolumeTest{
			CSIDriver: powervsDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It(fmt.Sprintf("[env] should use a pre-provisioned volume and retain PV with reclaimPolicy %q", v1.PersistentVolumeReclaimRetain), func() {
		reclaimPolicy := v1.PersistentVolumeReclaimRetain
		volumes := []testsuites.VolumeDetails{
			{
				VolumeID:      volumeID,
				FSType:        powervscsidriver.FSTypeExt4,
				ClaimSize:     diskSize,
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		test := testsuites.PreProvisionedReclaimPolicyTest{
			CSIDriver: powervsDriver,
			Volumes:   volumes,
		}
		test.Run(cs, ns)
	})

	It(fmt.Sprintf("[env] should use a pre-provisioned volume and delete PV with reclaimPolicy %q", v1.PersistentVolumeReclaimDelete), func() {
		reclaimPolicy := v1.PersistentVolumeReclaimDelete
		skipManuallyDeletingVolume = true
		volumes := []testsuites.VolumeDetails{
			{
				VolumeID:      volumeID,
				FSType:        powervscsidriver.FSTypeExt4,
				ClaimSize:     diskSize,
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		test := testsuites.PreProvisionedReclaimPolicyTest{
			CSIDriver: powervsDriver,
			Volumes:   volumes,
		}
		test.Run(cs, ns)
	})
})
