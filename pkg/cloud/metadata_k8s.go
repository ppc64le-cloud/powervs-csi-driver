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

package cloud

import (
	"context"
	"errors"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

type KubernetesAPIClient func(kubeconfig string) (kubernetes.Interface, error)

// Get default kubernetes API client.
var DefaultKubernetesAPIClient = func(kubeconfig string) (kubernetes.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	// creates the clientset
	return kubernetes.NewForConfig(config)
}

// Get instance info from kubernetes API.
func KubernetesAPIInstanceInfo(clientset kubernetes.Interface) (*Metadata, error) {
	nodeName := os.Getenv("CSI_NODE_NAME")
	if nodeName == "" {
		return nil, errors.New("CSI_NODE_NAME env var not set")
	}
	return GetInstanceInfoFromProviderID(clientset, nodeName)
}

func GetInstanceInfoFromProviderID(clientset kubernetes.Interface, nodeName string) (*Metadata, error) {
	// get node with k8s API
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting Node %s: %v", nodeName, err)
	}

	if node.Spec.ProviderID != "" {
		providerId := node.Spec.ProviderID
		klog.Infof("Node Name: %s, Provider ID: %s", nodeName, providerId)
		return TokenizeProviderID(providerId)
	} else {
		return nil, fmt.Errorf("ProviderID is empty for the node: %s", nodeName)
	}
}
