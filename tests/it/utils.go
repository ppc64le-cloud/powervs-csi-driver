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

package it

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/driver"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/util"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/tests/remote"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	port = 10000
)

var (
	drv       *driver.Driver
	csiClient *CSIClient
	nodeID    string
	r         *remote.Remote
	endpoint  = fmt.Sprintf("tcp://localhost:%d", port)
)

// Before runs the driver and creates a CSI client.
var _ = BeforeSuite(func() {
	var err error

	runRemotely := os.Getenv("TEST_REMOTE_NODE") == "1"

	verifyRequiredEnvVars(runRemotely)

	if runRemotely {
		r = remote.NewRemote()
		err = r.SetupNewDriver(endpoint)
		Expect(err).To(BeNil(), "error while driver setup")
		nodeID = os.Getenv("POWERVS_INSTANCE_ID")
		Expect(nodeID).NotTo(BeEmpty(), "Missing env var POWERVS_INSTANCE_ID")
	} else {
		drv, err = driver.NewDriver(driver.WithEndpoint(endpoint))
		Expect(err).To(BeNil())
		// Run the driver in goroutine
		go func() {
			err = drv.Run()
			Expect(err).To(BeNil())
		}()
	}

	csiClient, err = newCSIClient()
	Expect(err).To(BeNil(), "failed to create CSI Client")
	Expect(csiClient).NotTo(BeNil())
})

// After stops the driver.
var _ = AfterSuite(func() {
	if os.Getenv("TEST_REMOTE_NODE") == "1" {
		r.TeardownDriver()
	} else if drv != nil {
		drv.Stop()
	}
})

// CSIClient controller and node clients.
type CSIClient struct {
	ctrl csi.ControllerClient
	node csi.NodeClient
}

// verifyRequiredEnvVars Verify that PowerVS details are set in env.
func verifyRequiredEnvVars(runRemotely bool) {
	Expect(os.Getenv("IBMCLOUD_API_KEY")).NotTo(BeEmpty(), "Missing env var IBMCLOUD_API_KEY")
	Expect(os.Getenv("POWERVS_CLOUD_INSTANCE_ID")).NotTo(BeEmpty(), "Missing env var POWERVS_CLOUD_INSTANCE_ID")
	Expect(os.Getenv("POWERVS_ZONE")).NotTo(BeEmpty(), "Missing env var POWERVS_ZONE")

	// POWERVS_INSTANCE_ID is required when not running remotely
	if !runRemotely {
		nodeID = os.Getenv("POWERVS_INSTANCE_ID")
		Expect(nodeID).NotTo(BeEmpty(), "Missing env var POWERVS_INSTANCE_ID")
	}
}

// newCSIClient creates as CSI client.
func newCSIClient() (*CSIClient, error) {
	resolver.SetDefaultScheme("passthrough")
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// grpc.WithBlock(),
		grpc.WithContextDialer(
			func(context.Context, string) (net.Conn, error) {
				scheme, addr, err := util.ParseEndpoint(endpoint)
				if err != nil {
					return nil, err
				}
				var conn net.Conn
				err = wait.PollUntilContextTimeout(context.Background(), 10*time.Second, 3*time.Minute, true, func(context.Context) (bool, error) {
					conn, err = net.Dial(scheme, addr)
					if err != nil {
						klog.Warningf("Client failed to dial endpoint %v", endpoint)
						return false, err
					}
					klog.Infof("Client succeeded to dial endpoint %v", endpoint)
					return true, nil
				})
				if err != nil || conn == nil {
					return nil, fmt.Errorf("failed to get client connection: %v", err)
				}
				return conn, err
			},
		),
	}
	grpcClient, err := grpc.NewClient(endpoint, opts...)
	if err != nil {
		return nil, err
	}
	return &CSIClient{
		ctrl: csi.NewControllerClient(grpcClient),
		node: csi.NewNodeClient(grpcClient),
	}, nil
}
