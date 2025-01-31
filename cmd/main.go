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

package main

// DONE.

import (
	"flag"

	"k8s.io/klog/v2"

	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/driver"
)

func main() {
	fs := flag.NewFlagSet("ibm-powervs-block-csi-driver", flag.ExitOnError)
	options := GetOptions(fs)

	drv, err := driver.NewDriver(
		driver.WithEndpoint(options.ServerOptions.Endpoint),
		driver.WithMode(options.DriverMode),
		driver.WithDebug(options.ServerOptions.Debug),
		driver.WithVolumeAttachLimit(options.NodeOptions.VolumeAttachLimit),
		driver.WithKubeConfig(options.ServerOptions.Kubeconfig),
		driver.WithCloudConfig(options.ServerOptions.Cloudconfig),
	)
	if err != nil {
		klog.Fatalf("unable to create CSI driver. err: %v", err)
	}
	if err := drv.Run(); err != nil {
		klog.Fatalf("failed to run the CSI driver. err: %v", err)
	}
}
