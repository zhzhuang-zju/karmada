/*
Copyright 2025 The Karmada Authors.

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

package federatedresourcequota

import (
	"context"
	"reflect"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	policyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
)

const (
	// SchedulingControllerName is the controller name that will be used when reporting events.
	SchedulingControllerName = "federated-resource-quota-scheduling-controller"
)

// FederatedResourceQuotaSchedulingController watches FederatedResourceQuota update and delete events.
// When the overallUsed or overall fields change in a way that indicates more quota capacity
// is available, the controller will automatically reset the schedulingDueToQuota field from
// true to false on ResourceBinding objects in the same namespace, allowing previously
// quota-blocked resources to be reconsidered for scheduling.
type FederatedResourceQuotaSchedulingController struct {
	client.Client
	RateLimiterOptions ratelimiterflag.Options
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
// The FederatedResourceQuotaSchedulingController will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (c *FederatedResourceQuotaSchedulingController) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("FederatedResourceQuotaSchedulingController reconciling %s", req.NamespacedName.String())

	resourcebindings := &workv1alpha2.ResourceBindingList{}
	if err := c.Client.List(ctx, resourcebindings, &client.ListOptions{Namespace: req.Namespace}); err != nil { // index
		return controllerruntime.Result{}, err
	}

	var errs []error
	for _, rb := range resourcebindings.Items {
		if rb.Spec.Suspension != nil && rb.Spec.Suspension.SchedulingDueToQuota != nil && *rb.Spec.Suspension.SchedulingDueToQuota {
			rb.Spec.Suspension.SchedulingDueToQuota = nil
			if err := c.Client.Update(ctx, &rb); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return controllerruntime.Result{}, utilerrors.NewAggregate(errs)
}

var predicateFunc = predicate.Funcs{
	CreateFunc: func(event.CreateEvent) bool { return false },
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*policyv1alpha1.FederatedResourceQuota)
		newObj := e.ObjectNew.(*policyv1alpha1.FederatedResourceQuota)

		if !reflect.DeepEqual(oldObj.Status.Overall, newObj.Status.Overall) || !reflect.DeepEqual(oldObj.Status.OverallUsed, newObj.Status.OverallUsed) {
			return true
		}
		return false
	},
	DeleteFunc:  func(event.DeleteEvent) bool { return true },
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// SetupWithManager creates a controller and register to controller manager.
func (r *FederatedResourceQuotaSchedulingController) SetupWithManager(mgr controllerruntime.Manager) error {
	return controllerruntime.NewControllerManagedBy(mgr).
		Named(SchedulingControllerName).
		For(&policyv1alpha1.FederatedResourceQuota{}, builder.WithPredicates(predicateFunc)).
		WithOptions(controller.Options{RateLimiter: ratelimiterflag.DefaultControllerRateLimiter[controllerruntime.Request](r.RateLimiterOptions)}).
		Complete(r)
}
