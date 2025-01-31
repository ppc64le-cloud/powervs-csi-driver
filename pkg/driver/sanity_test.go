package driver

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/kubernetes-csi/csi-test/pkg/sanity"
	"k8s.io/mount-utils"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/util"
)

func TestSanity(t *testing.T) {
	// Setup the full driver and its environment
	dir, err := os.MkdirTemp("", "sanity-powervs-csi")
	if err != nil {
		t.Fatalf("error creating directory %v", err)
	}
	defer os.RemoveAll(dir)

	targetPath := filepath.Join(dir, "mount")
	stagingPath := filepath.Join(dir, "staging")
	endpoint := "unix://" + filepath.Join(dir, "csi.sock")

	config := &sanity.Config{
		TargetPath:       targetPath,
		StagingPath:      stagingPath,
		Address:          endpoint,
		CreateTargetDir:  createDir,
		CreateStagingDir: createDir,
	}

	driverOptions := &Options{
		endpoint: endpoint,
		mode:     AllMode,
	}

	statsUtil := MockStatSanity{
		targetPath: targetPath,
	}

	drv := &Driver{
		options: driverOptions,
		controllerService: controllerService{
			cloud:         newFakeCloudProvider(),
			driverOptions: driverOptions,
			volumeLocks:   util.NewVolumeLocks(),
		},
		nodeService: nodeService{
			mounter:       newFakeMounter(),
			cloud:         newFakeCloudProvider(),
			driverOptions: &Options{},
			pvmInstanceId: "test1234",
			volumeLocks:   util.NewVolumeLocks(),
			stats:         &statsUtil,
		},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recover: %v", r)
		}
	}()
	go func() {
		if err := drv.Run(); err != nil {
			panic(fmt.Sprintf("%v", err))
		}
	}()

	// Now call the test suite
	sanity.Test(t, config)
}

func createDir(targetPath string) (string, error) {
	if err := os.MkdirAll(targetPath, 0300); err != nil {
		if os.IsNotExist(err) {
			return "", err
		}
	}
	return targetPath, nil
}

// Fake State interface methods implementation for getting.
type MockStatSanity struct {
	targetPath string
}

// FSInfo.
func (su *MockStatSanity) FSInfo(path string) (int64, int64, int64, int64, int64, int64, error) {
	return 1, 1, 1, 1, 1, 1, nil
}

// DeviceInfo.
func (su *MockStatSanity) DeviceInfo(path string) (int64, error) {
	return 1, nil
}

// IsBlockDevice.
func (su *MockStatSanity) IsBlockDevice(devicePath string) (bool, error) {
	if !strings.Contains(devicePath, su.targetPath) {
		return false, errors.New("not a valid path")
	}
	return true, nil
}

func (su *MockStatSanity) IsPathNotExist(devicePath string) bool {
	// return true if not matched
	return !strings.Contains(devicePath, su.targetPath)
}

type fakeCloudProvider struct {
	disks  map[string]*fakeDisk
	pub    map[string]string
	tokens map[string]int64
}

type fakeDisk struct {
	*cloud.Disk
}

func newFakeCloudProvider() *fakeCloudProvider {
	return &fakeCloudProvider{
		disks:  make(map[string]*fakeDisk),
		pub:    make(map[string]string),
		tokens: make(map[string]int64),
	}
}

func (p *fakeCloudProvider) GetPVMInstanceByName(name string) (*cloud.PVMInstance, error) {
	return &cloud.PVMInstance{
		ID:       name + "-" + "id",
		DiskType: "tier3",
		Name:     name,
	}, nil
}

func (p *fakeCloudProvider) GetPVMInstanceByID(instanceID string) (*cloud.PVMInstance, error) {
	return &cloud.PVMInstance{
		ID:       instanceID,
		DiskType: "tier3",
		Name:     strings.Split(instanceID, "-")[0],
	}, nil
}

func (p *fakeCloudProvider) GetPVMInstanceDetails(instanceID string) (*models.PVMInstance, error) {
	return &models.PVMInstance{
		PvmInstanceID: &instanceID,
		ServerName:    &strings.Split(instanceID, "-")[0],
	}, nil
}

func (p *fakeCloudProvider) UpdateStoragePoolAffinity(instanceID string) error {
	return nil
}

func (c *fakeCloudProvider) CreateDisk(volumeName string, diskOptions *cloud.DiskOptions) (*cloud.Disk, error) {
	r1 := rand.New(rand.NewSource(time.Now().UnixNano()))

	if existingDisk, ok := c.disks[volumeName]; ok {
		// Already Created volume
		if existingDisk.Disk.CapacityGiB != util.BytesToGiB(diskOptions.CapacityBytes) {
			return nil, errors.New("disk Already exists")
		} else {
			return existingDisk.Disk, nil
		}
	}
	d := &fakeDisk{
		Disk: &cloud.Disk{
			VolumeID:    fmt.Sprintf("vol-%d", r1.Uint64()),
			CapacityGiB: util.BytesToGiB(diskOptions.CapacityBytes),
			WWN:         "/fake-path",
		},
	}
	c.disks[volumeName] = d
	return d.Disk, nil
}

func (c *fakeCloudProvider) DeleteDisk(volumeID string) error {
	for volName, f := range c.disks {
		if f.Disk.VolumeID == volumeID {
			delete(c.disks, volName)
		}
	}
	return nil
}

func (c *fakeCloudProvider) AttachDisk(volumeID, nodeID string) error {
	if _, ok := c.pub[volumeID]; ok {
		return cloud.ErrAlreadyExists
	}
	c.pub[volumeID] = nodeID
	return nil
}

func (c *fakeCloudProvider) DetachDisk(volumeID, nodeID string) error {
	return nil
}

func (c *fakeCloudProvider) CloneDisk(sourceVolumeName string, cloneVolumeName string) (disk *cloud.Disk, err error) {
	return nil, nil
}

func (c *fakeCloudProvider) IsAttached(volumeID string, nodeID string) (err error) {
	return nil
}

func (c *fakeCloudProvider) WaitForVolumeState(volumeID, expectedState string) error {
	return nil
}

func (c *fakeCloudProvider) WaitForCloneStatus(cloneTaskId string) error {
	return nil
}

func (c *fakeCloudProvider) GetDiskByName(name string) (*cloud.Disk, error) {
	disks := make([]*fakeDisk, 0, len(c.disks))
	for _, d := range c.disks {
		disks = append(disks, d)
	}
	if len(disks) > 1 {
		return nil, cloud.ErrAlreadyExists
	} else if len(disks) == 1 {
		return disks[0].Disk, nil
	}
	return nil, nil
}

func (c *fakeCloudProvider) GetDiskByNamePrefix(namePrefix string) (*cloud.Disk, error) {
	return nil, nil
}

func (c *fakeCloudProvider) GetDiskByID(volumeID string) (*cloud.Disk, error) {
	for _, f := range c.disks {
		if f.Disk.VolumeID == volumeID {
			return f.Disk, nil
		}
	}
	return nil, cloud.ErrNotFound
}

func (c *fakeCloudProvider) IsExistInstance(nodeID string) bool {
	return nodeID == "instanceID"
}

func (c *fakeCloudProvider) ResizeDisk(volumeID string, newSize int64) (int64, error) {
	for volName, f := range c.disks {
		if f.Disk.VolumeID == volumeID {
			c.disks[volName].CapacityGiB = newSize
			return newSize, nil
		}
	}
	return 0, cloud.ErrNotFound
}

type fakeMounter struct {
	mount.Interface
}

func newFakeMounter() *fakeMounter {
	return &fakeMounter{}
}

func (f *fakeMounter) IsCorruptedMnt(err error) bool {
	return false
}

func (f *fakeMounter) Mount(source string, target string, fstype string, options []string) error {
	return nil
}

func (f *fakeMounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return nil
}

func (f *fakeMounter) MountSensitiveWithoutSystemd(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return nil
}
func (f *fakeMounter) RescanSCSIBus() error {
	return nil
}
func (f *fakeMounter) Unmount(target string) error {
	return nil
}

func (f *fakeMounter) List() ([]mount.MountPoint, error) {
	return []mount.MountPoint{}, nil
}

func (f *fakeMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return false, nil
}

func (f *fakeMounter) GetMountRefs(pathname string) ([]string, error) {
	return []string{}, nil
}

func (f *fakeMounter) FormatAndMount(source string, target string, fstype string, options []string) error {
	return nil
}

func (f *fakeMounter) GetDeviceNameFromMount(mountPath string) (string, int, error) {
	return "", 0, nil
}

func (f *fakeMounter) MakeFile(pathname string) error {
	file, err := os.OpenFile(pathname, os.O_CREATE, os.FileMode(0644))
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	if err = file.Close(); err != nil {
		return err
	}
	return nil
}

func (f *fakeMounter) MakeDir(pathname string) error {
	err := os.MkdirAll(pathname, os.FileMode(0755))
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func (f *fakeMounter) ExistsPath(filename string) (bool, error) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func (f *fakeMounter) NeedResize(source string, path string) (bool, error) {
	return false, nil
}

func (f *fakeMounter) GetDeviceName(mountPath string) (string, int, error) {
	return mount.GetDeviceNameFromMount(f, mountPath)
}
