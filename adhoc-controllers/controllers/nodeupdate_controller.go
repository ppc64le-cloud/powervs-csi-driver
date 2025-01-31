/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/ibm-powervs-block-csi-driver/pkg/cloud"
)

// NodeUpdateReconciler reconciles a NodeUpdate object.
type NodeUpdateReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *NodeUpdateReconciler) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the Node instance
	node := corev1.Node{}
	err := r.Client.Get(context.Background(), req.NamespacedName, &node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// req object not found, could have been deleted after reconcile req.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			klog.Infof("%s: Node not found - do nothing", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the req.
		return ctrl.Result{}, fmt.Errorf("error getting node: %v", err)
	}

	// ProviderID format: ibmpowervs://<region>/<zone>/<service_instance_id>/<powervs_machine_id>
	if node.Spec.ProviderID != "" {
		klog.Infof("PROVIDER-ID: %s", node.Spec.ProviderID)
		metadata, err := cloud.TokenizeProviderID(node.Spec.ProviderID)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to tokenize the providerID. err: %v", err)
		}

		nodeUpdateScope, err := cloud.NewNodeUpdateScope(cloud.NodeUpdateScopeParams{
			ServiceInstanceId: metadata.GetCloudInstanceId(),
			InstanceId:        metadata.GetPvmInstanceId(),
			Zone:              metadata.GetZone(),
		})

		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create nodeUpdateScope: %w", err)
		}

		instance, err := nodeUpdateScope.Cloud.GetPVMInstanceDetails(nodeUpdateScope.InstanceId)
		if err != nil {
			klog.Errorf("unable to fetch instance details. err: %v", err)
			return ctrl.Result{}, nil
		}

		if instance != nil {
			klog.Infof("StoragePoolAffinity: %v", *instance.StoragePoolAffinity)
			if *instance.StoragePoolAffinity {
				switch *instance.Status {
				case cloud.PowerVSInstanceStateSHUTOFF, cloud.PowerVSInstanceStateACTIVE:
					if *instance.StoragePoolAffinity == cloud.StoragePoolAffinity {
						klog.Infof("PowerVS instance - %v Storage pool affinity already %t", instance.PvmInstanceID, cloud.StoragePoolAffinity)
					} else {
						err := r.getOrUpdate(nodeUpdateScope)
						if err != nil {
							klog.Errorf("unable to update instance StoragePoolAffinity. err: %v", err)
							return ctrl.Result{}, fmt.Errorf("failed to reconcile VSI for IBMPowerVSMachine %s/%s. err: %w", node.Namespace, node.Name, err)
						}
					}
				default:
					klog.Infof("PowerVS instance - %v state not ACTIVE/SHUTOFF yet", instance.PvmInstanceID)
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *NodeUpdateReconciler) getOrUpdate(scope *cloud.NodeUpdateScope) error {
	return scope.Cloud.UpdateStoragePoolAffinity(scope.InstanceId)
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(r)
}
