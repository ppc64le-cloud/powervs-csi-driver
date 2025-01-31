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
	"os"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"

	powervscloud "sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
	powervscsidriver "sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/driver"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/tests/e2e/driver"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/tests/e2e/testsuites"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("[powervs-csi-e2e]Dynamic Provisioning", func() {
	f := framework.NewDefaultFramework("powervs")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs           clientset.Interface
		ns           *v1.Namespace
		pvTestDriver driver.DynamicPVTestDriver
		volumeTypes  = powervscloud.ValidVolumeTypes
		fsTypes      = []string{powervscsidriver.FSTypeXfs}
	)

	BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace
		pvTestDriver = driver.InitPowervsCSIDriver()
	})

	for _, t := range volumeTypes {
		for _, fs := range fsTypes {
			volumeType := t
			fsType := fs
			It(fmt.Sprintf("should create a volume on demand with volume type %q and fs type %q", volumeType, fsType), func() {
				pods := []testsuites.PodDetails{
					{
						Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
						Volumes: []testsuites.VolumeDetails{
							{
								VolumeType: volumeType,
								FSType:     fsType,
								ClaimSize:  "10Gi",
								VolumeMount: testsuites.VolumeMountDetails{
									NameGenerate:      "test-volume-",
									MountPathGenerate: "/mnt/test-",
								},
							},
						},
					},
				}
				test := testsuites.DynamicallyProvisionedCmdVolumeTest{
					CSIDriver: pvTestDriver,
					Pods:      pods,
				}
				test.Run(cs, ns)
			})
		}
	}

	It("should create a volume on demand with provided mountOptions", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType:   powervscloud.VolumeTypeTier3,
						FSType:       powervscsidriver.FSTypeExt4,
						MountOptions: []string{"rw"},
						ClaimSize:    "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It("should create multiple PV objects, bind to PVCs and attach all to a single pod", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && echo 'hello world' > /mnt/test-2/data && grep 'hello world' /mnt/test-1/data  && grep 'hello world' /mnt/test-2/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier1,
						FSType:     powervscsidriver.FSTypeExt3,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
					{
						VolumeType: powervscloud.VolumeTypeTier3,
						FSType:     powervscsidriver.FSTypeExt4,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It("should create multiple PV objects, bind to PVCs and attach all to different pods", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier1,
						FSType:     powervscsidriver.FSTypeExt3,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier3,
						FSType:     powervscsidriver.FSTypeExt4,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It("should create a raw block volume on demand", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "dd if=/dev/zero of=/dev/xvda bs=1024k count=100",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier3,
						FSType:     powervscsidriver.FSTypeExt4,
						ClaimSize:  "10Gi",
						VolumeMode: testsuites.Block,
						VolumeDevice: testsuites.VolumeDeviceDetails{
							NameGenerate: "test-block-volume-",
							DevicePath:   "/dev/xvda",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It("should create a raw block volume and a filesystem volume on demand and bind to the same pod", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "dd if=/dev/zero of=/dev/xvda bs=1024k count=100 && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier1,
						FSType:     powervscsidriver.FSTypeExt4,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
					{
						VolumeType:   powervscloud.VolumeTypeTier3,
						FSType:       powervscsidriver.FSTypeExt3,
						MountOptions: []string{"rw"},
						ClaimSize:    "10Gi",
						VolumeMode:   testsuites.Block,
						VolumeDevice: testsuites.VolumeDeviceDetails{
							NameGenerate: "test-block-volume-",
							DevicePath:   "/dev/xvda",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It("should create multiple PV objects, bind to PVCs and attach all to different pods on the same node", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "while true; do echo $(date -u) >> /mnt/test-1/data; sleep 1; done",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier1,
						FSType:     powervscsidriver.FSTypeExt4,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
			{
				Cmd: "while true; do echo $(date -u) >> /mnt/test-1/data; sleep 1; done",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier3,
						FSType:     powervscsidriver.FSTypeExt3,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedCollocatedPodTest{
			CSIDriver:    pvTestDriver,
			Pods:         pods,
			ColocatePods: true,
		}
		test.Run(cs, ns)
	})

	It("should create a volume on demand and mount it as readOnly in a pod", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: "touch /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType: powervscloud.VolumeTypeTier3,
						FSType:     powervscsidriver.FSTypeExt4,
						ClaimSize:  "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
							ReadOnly:          true,
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedReadOnlyVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	It(fmt.Sprintf("should delete PV with reclaimPolicy %q", v1.PersistentVolumeReclaimDelete), func() {
		reclaimPolicy := v1.PersistentVolumeReclaimDelete
		volumes := []testsuites.VolumeDetails{
			{
				VolumeType:    powervscloud.VolumeTypeTier3,
				FSType:        powervscsidriver.FSTypeExt4,
				ClaimSize:     "10Gi",
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		test := testsuites.DynamicallyProvisionedReclaimPolicyTest{
			CSIDriver: pvTestDriver,
			Volumes:   volumes,
		}
		test.Run(cs, ns)
	})

	It(fmt.Sprintf("[env][labels] should retain PV with reclaimPolicy %q", v1.PersistentVolumeReclaimRetain), func() {
		if os.Getenv(apiKeyEnv) == "" {
			Skip(fmt.Sprintf("env %q not set", apiKeyEnv))
		}
		reclaimPolicy := v1.PersistentVolumeReclaimRetain
		volumes := []testsuites.VolumeDetails{
			{
				VolumeType:    powervscloud.VolumeTypeTier3,
				FSType:        powervscsidriver.FSTypeExt4,
				ClaimSize:     "10Gi",
				ReclaimPolicy: &reclaimPolicy,
			},
		}

		metadata, err := testsuites.GetInstanceMetadataFromNodeSpec(cs)
		if err != nil {
			Skip(fmt.Sprintf("Could not get cloudInstanceId : %v", err))
		}

		cloud, err := powervscloud.NewPowerVSCloud(metadata.GetCloudInstanceId(), metadata.GetZone(), debug)
		if err != nil {
			Fail(fmt.Sprintf("could not get NewCloud: %v", err))
		}

		test := testsuites.DynamicallyProvisionedReclaimPolicyTest{
			CSIDriver: pvTestDriver,
			Volumes:   volumes,
			Cloud:     cloud,
		}
		test.Run(cs, ns)
	})

	It("should create a deployment object, write and read to it, delete the pod and write and read to it again", func() {
		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' >> /mnt/test-1/data && while true; do sleep 1; done",
			Volumes: []testsuites.VolumeDetails{
				{
					VolumeType: powervscloud.VolumeTypeTier3,
					FSType:     powervscsidriver.FSTypeExt4,
					ClaimSize:  "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedDeletePodTest{
			CSIDriver: pvTestDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            []string{"cat", "/mnt/test-1/data"},
				ExpectedString: "hello world\nhello world\n", // pod will be restarted so expect to see 2 instances of string
			},
		}
		test.Run(cs, ns)
	})

	It("should create a volume on demand and resize it ", func() {
		allowVolumeExpansion := true
		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' >> /mnt/test-1/data && grep 'hello world' /mnt/test-1/data && sync",
			Volumes: []testsuites.VolumeDetails{
				{
					VolumeType: powervscloud.DefaultVolumeType,
					FSType:     powervscsidriver.FSTypeXfs,
					ClaimSize:  "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
					AllowVolumeExpansion: &allowVolumeExpansion,
				},
			},
		}

		test := testsuites.DynamicallyProvisionedResizeVolumeTest{
			CSIDriver: pvTestDriver,
			Pod:       pod,
		}
		test.Run(cs, ns)
	})

})

var _ = Describe("[powervs-csi-e2e] Volume binding mode test Dynamic Provisioning", func() {
	f := framework.NewDefaultFramework("powervs")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs           clientset.Interface
		ns           *v1.Namespace
		pvTestDriver driver.DynamicPVTestDriver
	)

	BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace
		pvTestDriver = driver.InitPowervsCSIDriver()
	})

	It("should allow for topology aware volume scheduling", func() {
		volumeBindingMode := storagev1.VolumeBindingWaitForFirstConsumer
		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeType:        powervscloud.VolumeTypeTier1,
						FSType:            powervscsidriver.FSTypeExt4,
						ClaimSize:         "10Gi",
						VolumeBindingMode: &volumeBindingMode,
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}

		test := testsuites.DynamicallyProvisionedTopologyAwareVolumeTest{
			CSIDriver: pvTestDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})
})
