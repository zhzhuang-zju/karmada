/*
Copyright 2021 The Karmada Authors.

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

package detector

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	configv1alpha1 "github.com/karmada-io/karmada/pkg/apis/config/v1alpha1"
	policyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/events"
	"github.com/karmada-io/karmada/pkg/features"
	"github.com/karmada-io/karmada/pkg/metrics"
	"github.com/karmada-io/karmada/pkg/resourceinterpreter"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/eventfilter"
	"github.com/karmada-io/karmada/pkg/util/fedinformer"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/genericmanager"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/keys"
	"github.com/karmada-io/karmada/pkg/util/helper"
	"github.com/karmada-io/karmada/pkg/util/lifted"
	"github.com/karmada-io/karmada/pkg/util/names"
	"github.com/karmada-io/karmada/pkg/util/restmapper"
)

// ResourceDetector is a resource watcher which watches all resources and reconcile the events.
type ResourceDetector struct {
	// DiscoveryClientSet is used to resource discovery.
	DiscoveryClientSet *discovery.DiscoveryClient
	// Client is used to retrieve objects, it is often more convenient than lister.
	Client client.Client
	// DynamicClient used to fetch arbitrary resources.
	DynamicClient                dynamic.Interface
	InformerManager              genericmanager.SingleClusterInformerManager
	ControllerRuntimeCache       ctrlcache.Cache
	EventHandler                 cache.ResourceEventHandler
	Processor                    util.AsyncWorker
	SkippedResourceConfig        *util.SkippedResourceConfig
	SkippedPropagatingNamespaces []*regexp.Regexp
	// ResourceInterpreter knows the details of resource structure.
	ResourceInterpreter resourceinterpreter.ResourceInterpreter
	EventRecorder       record.EventRecorder
	// policyReconcileWorker maintains a rate limited queue which used to store PropagationPolicy's key and
	// a reconcile function to consume the items in queue.
	policyReconcileWorker util.AsyncWorker

	// clusterPolicyReconcileWorker maintains a rate limited queue which used to store ClusterPropagationPolicy's key and
	// a reconcile function to consume the items in queue.
	clusterPolicyReconcileWorker util.AsyncWorker

	RESTMapper meta.RESTMapper

	// waitingObjects tracks of objects which haven't been propagated yet as lack of appropriate policies.
	waitingObjects map[keys.ClusterWideKey]struct{}
	// waitingLock is the lock for waitingObjects operation.
	waitingLock sync.RWMutex
	// ConcurrentPropagationPolicySyncs is the number of PropagationPolicy that are allowed to sync concurrently.
	ConcurrentPropagationPolicySyncs int
	// ConcurrentClusterPropagationPolicySyncs is the number of ClusterPropagationPolicy that are allowed to sync concurrently.
	ConcurrentClusterPropagationPolicySyncs int
	// ConcurrentResourceTemplateSyncs is the number of resource templates that are allowed to sync concurrently.
	// Larger number means responsive resource template syncing but more CPU(and network) load.
	ConcurrentResourceTemplateSyncs int

	// RateLimiterOptions is the configuration for rate limiter which may significantly influence the performance of
	// the controller.
	RateLimiterOptions ratelimiterflag.Options
}

// Start runs the detector, never stop until context canceled.
func (d *ResourceDetector) Start(ctx context.Context) error {
	klog.Infof("Starting resource detector.")
	d.waitingObjects = make(map[keys.ClusterWideKey]struct{})

	// setup policy reconcile worker
	policyWorkerOptions := util.Options{
		Name:               "propagationPolicy reconciler",
		KeyFunc:            NamespacedKeyFunc,
		ReconcileFunc:      d.ReconcilePropagationPolicy,
		RateLimiterOptions: d.RateLimiterOptions,
	}
	d.policyReconcileWorker = util.NewAsyncWorker(policyWorkerOptions)
	d.policyReconcileWorker.Run(ctx, d.ConcurrentPropagationPolicySyncs)
	clusterPolicyWorkerOptions := util.Options{
		Name:               "clusterPropagationPolicy reconciler",
		KeyFunc:            NamespacedKeyFunc,
		ReconcileFunc:      d.ReconcileClusterPropagationPolicy,
		RateLimiterOptions: d.RateLimiterOptions,
	}
	d.clusterPolicyReconcileWorker = util.NewAsyncWorker(clusterPolicyWorkerOptions)
	d.clusterPolicyReconcileWorker.Run(ctx, d.ConcurrentClusterPropagationPolicySyncs)

	detectorWorkerOptions := util.Options{
		Name:               "resource detector",
		KeyFunc:            ResourceItemKeyFunc,
		ReconcileFunc:      d.Reconcile,
		RateLimiterOptions: d.RateLimiterOptions,
	}
	d.Processor = util.NewAsyncWorker(detectorWorkerOptions)
	d.Processor.Run(ctx, d.ConcurrentResourceTemplateSyncs)

	// watch and enqueue PropagationPolicy changes.
	policyHandler := fedinformer.NewHandlerOnEvents(d.OnPropagationPolicyAdd, d.OnPropagationPolicyUpdate, nil)
	ppInformer, err := d.ControllerRuntimeCache.GetInformer(ctx, &policyv1alpha1.PropagationPolicy{})
	if err != nil {
		klog.Errorf("Failed to get informer for PropagationPolicy: %v", err)
		return err
	}
	_, err = ppInformer.AddEventHandler(policyHandler)
	if err != nil {
		klog.Errorf("Failed to add event handler for PropagationPolicy: %v", err)
		return err
	}

	// watch and enqueue ClusterPropagationPolicy changes.
	clusterPolicyHandler := fedinformer.NewHandlerOnEvents(d.OnClusterPropagationPolicyAdd, d.OnClusterPropagationPolicyUpdate, nil)
	cppInformer, err := d.ControllerRuntimeCache.GetInformer(ctx, &policyv1alpha1.ClusterPropagationPolicy{})
	if err != nil {
		klog.Errorf("Failed to get informer for ClusterPropagationPolicy: %v", err)
		return err
	}
	_, err = cppInformer.AddEventHandler(clusterPolicyHandler)
	if err != nil {
		klog.Errorf("Failed to add event handler for ClusterPropagationPolicy: %v", err)
		return err
	}

	d.EventHandler = fedinformer.NewFilteringHandlerOnAllEvents(d.EventFilter, d.OnAdd, d.OnUpdate, d.OnDelete)
	go d.discoverResources(ctx, 30*time.Second)

	<-ctx.Done()
	klog.Infof("Stopped as context canceled")
	return nil
}

// Check if our ResourceDetector implements necessary interfaces
var (
	_ manager.Runnable               = &ResourceDetector{}
	_ manager.LeaderElectionRunnable = &ResourceDetector{}
)

func (d *ResourceDetector) discoverResources(ctx context.Context, period time.Duration) {
	wait.Until(func() {
		newResources := lifted.GetDeletableResources(d.DiscoveryClientSet)
		for r := range newResources {
			if d.InformerManager.IsHandlerExist(r, d.EventHandler) || d.gvrDisabled(r) {
				continue
			}
			klog.Infof("Setup informer for %s", r.String())
			d.InformerManager.ForResource(r, d.EventHandler)

			informer := d.InformerManager.GetInformer(r)
			informer.Informer().AddIndexers(cache.Indexers{
				"bylabel": func(obj interface{}) ([]string, error) {
					object, err := meta.Accessor(obj)
					if err != nil {
						return nil, nil
					}
					if val, ok := object.GetLabels()[policyv1alpha1.PropagationPolicyPermanentIDLabel]; ok && val != "" {
						return []string{val}, nil
					}
					return nil, nil
				},
			})
		}
		d.InformerManager.Start()
	}, period, ctx.Done())
}

// gvrDisabled returns whether GroupVersionResource is disabled.
func (d *ResourceDetector) gvrDisabled(gvr schema.GroupVersionResource) bool {
	if d.SkippedResourceConfig == nil {
		return false
	}

	if d.SkippedResourceConfig.GroupVersionDisabled(gvr.GroupVersion()) {
		return true
	}
	if d.SkippedResourceConfig.GroupDisabled(gvr.Group) {
		return true
	}

	gvks, err := d.RESTMapper.KindsFor(gvr)
	if err != nil {
		klog.Errorf("gvr(%s) transform failed: %v", gvr.String(), err)
		return false
	}

	for _, gvk := range gvks {
		if d.SkippedResourceConfig.GroupVersionKindDisabled(gvk) {
			return true
		}
	}

	return false
}

// NeedLeaderElection implements LeaderElectionRunnable interface.
// So that the detector could run in the leader election mode.
func (d *ResourceDetector) NeedLeaderElection() bool {
	return true
}

// Reconcile performs a full reconciliation for the object referred to by the key.
// The key will be re-queued if an error is non-nil.
func (d *ResourceDetector) Reconcile(key util.QueueKey) error {
	clusterWideKeyWithConfig, ok := key.(keys.ClusterWideKeyWithConfig)
	if !ok {
		klog.Error("Invalid key")
		return fmt.Errorf("invalid key")
	}

	clusterWideKey := clusterWideKeyWithConfig.ClusterWideKey
	resourceChangeByKarmada := clusterWideKeyWithConfig.ResourceChangeByKarmada
	klog.Infof("Reconciling object: %s", clusterWideKey)

	object, err := d.GetUnstructuredObject(clusterWideKey)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// The resource may no longer exist, in which case we try (may not exist in waiting list) remove it from waiting list and stop processing.
			d.RemoveWaiting(clusterWideKey)

			// Once resource be deleted, the derived ResourceBinding or ClusterResourceBinding also need to be cleaned up,
			// currently we do that by setting owner reference to derived objects.
			return nil
		}
		klog.Errorf("Failed to get unstructured object(%s), error: %v", clusterWideKeyWithConfig, err)
		return err
	}

	resourceTemplateClaimedBy := util.GetLabelValue(object.GetLabels(), util.ResourceTemplateClaimedByLabel)
	// If the resource lacks this label, it implies that the resource template can be propagated by Policy.
	// For instance, once MultiClusterService takes over the Service, Policy cannot reclaim it.
	if resourceTemplateClaimedBy != "" {
		d.RemoveWaiting(clusterWideKey)
		return nil
	}

	return d.propagateResource(object, clusterWideKey, resourceChangeByKarmada)
}

// EventFilter tells if an object should be taken care of.
//
// All objects under Karmada reserved namespace should be ignored:
// - karmada-system
// - karmada-cluster
// - karmada-es-*
//
// If '--skipped-propagating-namespaces'(defaults to kube-.*) is specified,
// all resources in the skipped-propagating-namespaces will be ignored.
func (d *ResourceDetector) EventFilter(obj interface{}) bool {
	key, err := ClusterWideKeyFunc(obj)
	if err != nil {
		return false
	}

	clusterWideKey, ok := key.(keys.ClusterWideKey)
	if !ok {
		klog.Errorf("Invalid key")
		return false
	}

	if names.IsReservedNamespace(clusterWideKey.Namespace) {
		return false
	}

	// if SkippedPropagatingNamespaces is set, skip object events in these namespaces.
	for _, nsRegexp := range d.SkippedPropagatingNamespaces {
		if match := nsRegexp.MatchString(clusterWideKey.Namespace); match {
			return false
		}
	}

	// Prevent configmap/extension-apiserver-authentication from propagating as it is generated
	// and managed by kube-apiserver.
	// Refer to https://github.com/karmada-io/karmada/issues/4228 for more details.
	if clusterWideKey.Namespace == "kube-system" && clusterWideKey.Kind == "ConfigMap" &&
		clusterWideKey.Name == "extension-apiserver-authentication" {
		return false
	}

	return true
}

// OnAdd handles object add event and push the object to queue.
func (d *ResourceDetector) OnAdd(obj interface{}) {
	runtimeObj, ok := obj.(runtime.Object)
	if !ok {
		return
	}
	d.Processor.Enqueue(ResourceItem{Obj: runtimeObj})
}

// OnUpdate handles object update event and push the object to queue.
func (d *ResourceDetector) OnUpdate(oldObj, newObj interface{}) {
	unstructuredOldObj, err := helper.ToUnstructured(oldObj)
	if err != nil {
		klog.Errorf("Failed to transform oldObj, error: %v", err)
		return
	}

	unstructuredNewObj, err := helper.ToUnstructured(newObj)
	if err != nil {
		klog.Errorf("Failed to transform newObj, error: %v", err)
		return
	}

	newRuntimeObj, ok := newObj.(runtime.Object)
	if !ok {
		klog.Errorf("Failed to assert newObj as runtime.Object")
		return
	}

	if !eventfilter.SpecificationChanged(unstructuredOldObj, unstructuredNewObj) {
		klog.V(4).Infof("Ignore update event of object (kind=%s, %s/%s) as specification no change", unstructuredOldObj.GetKind(), unstructuredOldObj.GetNamespace(), unstructuredOldObj.GetName())
		return
	}

	resourceChangeByKarmada := eventfilter.ResourceChangeByKarmada(unstructuredOldObj, unstructuredNewObj)

	resourceItem := ResourceItem{
		Obj:                     newRuntimeObj,
		ResourceChangeByKarmada: resourceChangeByKarmada,
	}

	d.Processor.Enqueue(resourceItem)
}

// OnDelete handles object delete event and push the object to queue.
func (d *ResourceDetector) OnDelete(obj interface{}) {
	d.OnAdd(obj)
}

// LookForMatchedPolicy tries to find a policy for object referenced by object key.
func (d *ResourceDetector) LookForMatchedPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey) (*policyv1alpha1.PropagationPolicy, error) {
	if len(objectKey.Namespace) == 0 {
		return nil, nil
	}

	klog.V(2).Infof("Attempts to match policy for resource(%s)", objectKey)
	policyList := &policyv1alpha1.PropagationPolicyList{}
	err := d.Client.List(context.TODO(), policyList, &client.ListOptions{
		Namespace:             objectKey.Namespace,
		UnsafeDisableDeepCopy: ptr.To(true),
	})
	if err != nil {
		klog.Errorf("Failed to list propagation policy: %v", err)
		return nil, err
	}
	if len(policyList.Items) == 0 {
		klog.V(2).Infof("No propagationpolicy find in namespace(%s).", objectKey.Namespace)
		return nil, nil
	}
	policies := make([]*policyv1alpha1.PropagationPolicy, 0, len(policyList.Items))
	for _, policy := range policyList.Items {
		if !policy.DeletionTimestamp.IsZero() {
			klog.V(4).Infof("Propagation policy(%s/%s) cannot match any resource template because it's being deleted.", policy.Namespace, policy.Name)
			continue
		}
		policies = append(policies, &policy)
	}

	return getHighestPriorityPropagationPolicy(policies, object, objectKey), nil
}

// LookForMatchedClusterPolicy tries to find a ClusterPropagationPolicy for object referenced by object key.
func (d *ResourceDetector) LookForMatchedClusterPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey) (*policyv1alpha1.ClusterPropagationPolicy, error) {
	klog.V(2).Infof("Attempts to match cluster policy for resource(%s)", objectKey)
	policyList := &policyv1alpha1.ClusterPropagationPolicyList{}
	err := d.Client.List(context.TODO(), policyList, &client.ListOptions{
		UnsafeDisableDeepCopy: ptr.To(true),
	})
	if err != nil {
		klog.Errorf("Failed to list cluster propagation policy: %v", err)
		return nil, err
	}
	if len(policyList.Items) == 0 {
		klog.V(2).Infof("No clusterpropagationpolicy find.")
		return nil, nil
	}

	policies := make([]*policyv1alpha1.ClusterPropagationPolicy, 0, len(policyList.Items))
	for _, policy := range policyList.Items {
		if !policy.DeletionTimestamp.IsZero() {
			klog.V(4).Infof("Cluster propagation policy(%s) cannot match any resource template because it's being deleted.", policy.Name)
			continue
		}
		policies = append(policies, &policy)
	}

	return getHighestPriorityClusterPropagationPolicy(policies, object, objectKey), nil
}

// ApplyPolicy starts propagate the object referenced by object key according to PropagationPolicy.
func (d *ResourceDetector) ApplyPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey,
	resourceChangeByKarmada bool, policy *policyv1alpha1.PropagationPolicy) (err error) {
	start := time.Now()
	klog.Infof("Applying policy(%s/%s) for object: %s", policy.Namespace, policy.Name, objectKey)
	var operationResult controllerutil.OperationResult
	defer func() {
		metrics.ObserveApplyPolicyAttemptAndLatency(err, start)
		if err != nil {
			d.EventRecorder.Eventf(object, corev1.EventTypeWarning, events.EventReasonApplyPolicyFailed, "Apply policy(%s/%s) failed: %v", policy.Namespace, policy.Name, err)
		} else if operationResult != controllerutil.OperationResultNone {
			d.EventRecorder.Eventf(object, corev1.EventTypeNormal, events.EventReasonApplyPolicySucceed, "Apply policy(%s/%s) succeed", policy.Namespace, policy.Name)
		}
	}()

	policyID, err := d.ClaimPolicyForObject(object, policy)
	if err != nil {
		klog.Errorf("Failed to claim policy(%s/%s) for object: %s", policy.Namespace, policy.Name, object)
		return err
	}

	// If this Reconcile action is triggered by Karmada itself and the current bound Policy is lazy activation preference,
	// resource will delay to sync the placement from Policy to Binding util resource is updated by User.
	if resourceChangeByKarmada && util.IsLazyActivationEnabled(policy.Spec.ActivationPreference) {
		operationResult = controllerutil.OperationResultNone
		klog.Infof("Skip refresh Binding for the change of resource (%s/%s) is from Karmada and activation "+
			"preference of current bound policy (%s) is enabled.", object.GetNamespace(), object.GetName(), policy.Name)
		return nil
	}

	binding, err := d.BuildResourceBinding(object, &policy.Spec, policyID, policy.ObjectMeta, AddPPClaimMetadata)
	if err != nil {
		klog.Errorf("Failed to build resourceBinding for object: %s. error: %v", objectKey, err)
		return err
	}
	bindingCopy := binding.DeepCopy()
	err = retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		operationResult, err = controllerutil.CreateOrUpdate(context.TODO(), d.Client, bindingCopy, func() error {
			// If this binding exists and its owner is not the input object, return error and let garbage collector
			// delete this binding and try again later. See https://github.com/karmada-io/karmada/issues/2090.
			if ownerRef := metav1.GetControllerOfNoCopy(bindingCopy); ownerRef != nil && ownerRef.UID != object.GetUID() {
				return fmt.Errorf("failed to update binding due to different owner reference UID, will " +
					"try again later after binding is garbage collected, see https://github.com/karmada-io/karmada/issues/2090")
			}

			// Just update necessary fields, especially avoid modifying Spec.Clusters which is scheduling result, if already exists.
			bindingCopy.Annotations = util.DedupeAndMergeAnnotations(bindingCopy.Annotations, binding.Annotations)
			bindingCopy.Labels = util.DedupeAndMergeLabels(bindingCopy.Labels, binding.Labels)
			bindingCopy.Finalizers = util.DedupeAndMergeFinalizers(bindingCopy.Finalizers, binding.Finalizers)
			bindingCopy.OwnerReferences = binding.OwnerReferences
			bindingCopy.Spec.Resource = binding.Spec.Resource
			bindingCopy.Spec.ReplicaRequirements = binding.Spec.ReplicaRequirements
			bindingCopy.Spec.Replicas = binding.Spec.Replicas
			bindingCopy.Spec.PropagateDeps = binding.Spec.PropagateDeps
			bindingCopy.Spec.SchedulerName = binding.Spec.SchedulerName
			bindingCopy.Spec.Placement = binding.Spec.Placement
			bindingCopy.Spec.Failover = binding.Spec.Failover
			bindingCopy.Spec.ConflictResolution = binding.Spec.ConflictResolution
			bindingCopy.Spec.PreserveResourcesOnDeletion = binding.Spec.PreserveResourcesOnDeletion
			bindingCopy.Spec.SchedulePriority = binding.Spec.SchedulePriority
			if binding.Spec.Suspension != nil {
				if bindingCopy.Spec.Suspension == nil {
					bindingCopy.Spec.Suspension = &workv1alpha2.Suspension{}
				}
				bindingCopy.Spec.Suspension.Suspension = binding.Spec.Suspension.Suspension
			}
			excludeClusterPolicy(bindingCopy)
			return nil
		})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		klog.Errorf("Failed to apply policy(%s) for object: %s. error: %v", policy.Name, objectKey, err)
		return err
	}

	switch operationResult {
	case controllerutil.OperationResultCreated:
		klog.Infof("Create ResourceBinding(%s/%s) successfully.", binding.GetNamespace(), binding.GetName())
	case controllerutil.OperationResultUpdated:
		klog.Infof("Update ResourceBinding(%s/%s) successfully.", binding.GetNamespace(), binding.GetName())
	default:
		klog.V(2).Infof("ResourceBinding(%s/%s) is up to date.", binding.GetNamespace(), binding.GetName())
	}

	return nil
}

// ApplyClusterPolicy starts propagate the object referenced by object key according to ClusterPropagationPolicy.
// nolint:gocyclo
func (d *ResourceDetector) ApplyClusterPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey,
	resourceChangeByKarmada bool, policy *policyv1alpha1.ClusterPropagationPolicy) (err error) {
	start := time.Now()
	klog.Infof("Applying cluster policy(%s) for object: %s", policy.Name, objectKey)
	var operationResult controllerutil.OperationResult
	defer func() {
		metrics.ObserveApplyPolicyAttemptAndLatency(err, start)
		if err != nil {
			d.EventRecorder.Eventf(object, corev1.EventTypeWarning, events.EventReasonApplyPolicyFailed, "Apply cluster policy(%s) failed: %v", policy.Name, err)
		} else if operationResult != controllerutil.OperationResultNone {
			d.EventRecorder.Eventf(object, corev1.EventTypeNormal, events.EventReasonApplyPolicySucceed, "Apply cluster policy(%s) succeed", policy.Name)
		}
	}()

	policyID, err := d.ClaimClusterPolicyForObject(object, policy)
	if err != nil {
		klog.Errorf("Failed to claim cluster policy(%s) for object: %s", policy.Name, object)
		return err
	}

	// If this Reconcile action is triggered by Karmada itself and the current bound Policy is lazy activation preference,
	// resource will delay to sync the placement from Policy to Binding util resource is updated by User.
	if resourceChangeByKarmada && util.IsLazyActivationEnabled(policy.Spec.ActivationPreference) {
		operationResult = controllerutil.OperationResultNone
		klog.Infof("Skip refresh Binding for the change of resource (%s/%s) is from Karmada and activation "+
			"preference of current bound cluster policy (%s) is enabled.", object.GetNamespace(), object.GetName(), policy.Name)
		return nil
	}

	// Build `ResourceBinding` or `ClusterResourceBinding` according to the resource template's scope.
	// For namespace-scoped resources, which namespace is not empty, building `ResourceBinding`.
	// For cluster-scoped resources, which namespace is empty, building `ClusterResourceBinding`.
	if object.GetNamespace() != "" {
		binding, err := d.BuildResourceBinding(object, &policy.Spec, policyID, policy.ObjectMeta, AddCPPClaimMetadata)
		if err != nil {
			klog.Errorf("Failed to build resourceBinding for object: %s. error: %v", objectKey, err)
			return err
		}
		bindingCopy := binding.DeepCopy()
		err = retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
			operationResult, err = controllerutil.CreateOrUpdate(context.TODO(), d.Client, bindingCopy, func() error {
				// If this binding exists and its owner is not the input object, return error and let garbage collector
				// delete this binding and try again later. See https://github.com/karmada-io/karmada/issues/2090.
				if ownerRef := metav1.GetControllerOfNoCopy(bindingCopy); ownerRef != nil && ownerRef.UID != object.GetUID() {
					return fmt.Errorf("failed to update binding due to different owner reference UID, will " +
						"try again later after binding is garbage collected, see https://github.com/karmada-io/karmada/issues/2090")
				}

				// Just update necessary fields, especially avoid modifying Spec.Clusters which is scheduling result, if already exists.
				bindingCopy.Annotations = util.DedupeAndMergeAnnotations(bindingCopy.Annotations, binding.Annotations)
				bindingCopy.Labels = util.DedupeAndMergeLabels(bindingCopy.Labels, binding.Labels)
				bindingCopy.Finalizers = util.DedupeAndMergeFinalizers(bindingCopy.Finalizers, binding.Finalizers)
				bindingCopy.OwnerReferences = binding.OwnerReferences
				bindingCopy.Spec.Resource = binding.Spec.Resource
				bindingCopy.Spec.ReplicaRequirements = binding.Spec.ReplicaRequirements
				bindingCopy.Spec.Replicas = binding.Spec.Replicas
				bindingCopy.Spec.PropagateDeps = binding.Spec.PropagateDeps
				bindingCopy.Spec.SchedulerName = binding.Spec.SchedulerName
				bindingCopy.Spec.Placement = binding.Spec.Placement
				bindingCopy.Spec.Failover = binding.Spec.Failover
				bindingCopy.Spec.ConflictResolution = binding.Spec.ConflictResolution
				bindingCopy.Spec.PreserveResourcesOnDeletion = binding.Spec.PreserveResourcesOnDeletion
				bindingCopy.Spec.SchedulePriority = binding.Spec.SchedulePriority
				if binding.Spec.Suspension != nil {
					if bindingCopy.Spec.Suspension == nil {
						bindingCopy.Spec.Suspension = &workv1alpha2.Suspension{}
					}
					bindingCopy.Spec.Suspension.Suspension = binding.Spec.Suspension.Suspension
				}
				return nil
			})
			return err
		})

		if err != nil {
			klog.Errorf("Failed to apply cluster policy(%s) for object: %s. error: %v", policy.Name, objectKey, err)
			return err
		}

		switch operationResult {
		case controllerutil.OperationResultCreated:
			klog.Infof("Create ResourceBinding(%s) successfully.", binding.GetName())
		case controllerutil.OperationResultUpdated:
			klog.Infof("Update ResourceBinding(%s) successfully.", binding.GetName())
		default:
			klog.V(2).Infof("ResourceBinding(%s) is up to date.", binding.GetName())
		}
	} else {
		binding, err := d.BuildClusterResourceBinding(object, &policy.Spec, policyID, policy.ObjectMeta)
		if err != nil {
			klog.Errorf("Failed to build clusterResourceBinding for object: %s. error: %v", objectKey, err)
			return err
		}
		bindingCopy := binding.DeepCopy()
		err = retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
			operationResult, err = controllerutil.CreateOrUpdate(context.TODO(), d.Client, bindingCopy, func() error {
				// If this binding exists and its owner is not the input object, return error and let garbage collector
				// delete this binding and try again later. See https://github.com/karmada-io/karmada/issues/2090.
				if ownerRef := metav1.GetControllerOfNoCopy(bindingCopy); ownerRef != nil && ownerRef.UID != object.GetUID() {
					return fmt.Errorf("failed to update binding due to different owner reference UID, will " +
						"try again later after binding is garbage collected, see https://github.com/karmada-io/karmada/issues/2090")
				}

				// Just update necessary fields, especially avoid modifying Spec.Clusters which is scheduling result, if already exists.
				bindingCopy.Annotations = util.DedupeAndMergeAnnotations(bindingCopy.Annotations, binding.Annotations)
				bindingCopy.Labels = util.DedupeAndMergeLabels(bindingCopy.Labels, binding.Labels)
				bindingCopy.Finalizers = util.DedupeAndMergeFinalizers(bindingCopy.Finalizers, binding.Finalizers)
				bindingCopy.OwnerReferences = binding.OwnerReferences
				bindingCopy.Spec.Resource = binding.Spec.Resource
				bindingCopy.Spec.ReplicaRequirements = binding.Spec.ReplicaRequirements
				bindingCopy.Spec.Replicas = binding.Spec.Replicas
				bindingCopy.Spec.SchedulerName = binding.Spec.SchedulerName
				bindingCopy.Spec.Placement = binding.Spec.Placement
				bindingCopy.Spec.Failover = binding.Spec.Failover
				bindingCopy.Spec.ConflictResolution = binding.Spec.ConflictResolution
				bindingCopy.Spec.PreserveResourcesOnDeletion = binding.Spec.PreserveResourcesOnDeletion
				if binding.Spec.Suspension != nil {
					if bindingCopy.Spec.Suspension == nil {
						bindingCopy.Spec.Suspension = &workv1alpha2.Suspension{}
					}
					bindingCopy.Spec.Suspension.Suspension = binding.Spec.Suspension.Suspension
				}
				return nil
			})
			return err
		})

		if err != nil {
			klog.Errorf("Failed to apply cluster policy(%s) for object: %s. error: %v", policy.Name, objectKey, err)
			return err
		}

		switch operationResult {
		case controllerutil.OperationResultCreated:
			klog.Infof("Create ClusterResourceBinding(%s) successfully.", binding.GetName())
		case controllerutil.OperationResultUpdated:
			klog.Infof("Update ClusterResourceBinding(%s) successfully.", binding.GetName())
		default:
			klog.V(2).Infof("ClusterResourceBinding(%s) is up to date.", binding.GetName())
		}
	}

	return nil
}

// GetUnstructuredObject retrieves object by key and returned its unstructured.
// Any updates to this resource template are not recommended as it may come from the informer cache.
// We should abide by the principle of making a deep copy first and then modifying it.
// See issue: https://github.com/karmada-io/karmada/issues/3878.
func (d *ResourceDetector) GetUnstructuredObject(objectKey keys.ClusterWideKey) (*unstructured.Unstructured, error) {
	objectGVR, err := restmapper.GetGroupVersionResource(d.RESTMapper, objectKey.GroupVersionKind())
	if err != nil {
		klog.Errorf("Failed to get GVR of object: %s, error: %v", objectKey, err)
		return nil, err
	}

	object, err := d.InformerManager.Lister(objectGVR).Get(objectKey.NamespaceKey())
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the target object is not found in the informer cache,
			// use the DynamicClient to get the target object again.
			var object *unstructured.Unstructured
			object, err = d.DynamicClient.Resource(objectGVR).Namespace(objectKey.Namespace).
				Get(context.TODO(), objectKey.Name, metav1.GetOptions{})
			if err == nil {
				return object, nil
			}
		}
		klog.Errorf("Failed to get object(%s), error: %v", objectKey, err)
		return nil, err
	}

	unstructuredObj, err := helper.ToUnstructured(object)
	if err != nil {
		klog.Errorf("Failed to transform object(%s), error: %v", objectKey, err)
		return nil, err
	}

	return unstructuredObj, nil
}

// ClaimPolicyForObject set policy identifier which the object associated with.
// It will add policy labels and annotations to the object, and update the obj with the content returned by the Server, ensuring that the obj used when generating the binding is the latest.
func (d *ResourceDetector) ClaimPolicyForObject(object *unstructured.Unstructured, policy *policyv1alpha1.PropagationPolicy) (string, error) {
	policyID := policy.Labels[policyv1alpha1.PropagationPolicyPermanentIDLabel]

	objLabels := object.GetLabels()
	if len(objLabels) > 0 {
		// object has been claimed, don't need to claim again
		if !excludeClusterPolicy(object) &&
			objLabels[policyv1alpha1.PropagationPolicyPermanentIDLabel] == policyID {
			return policyID, nil
		}
	}
	object = object.DeepCopy()
	AddPPClaimMetadata(object, policyID, policy.ObjectMeta)
	return policyID, d.Client.Update(context.TODO(), object)
}

// ClaimClusterPolicyForObject set cluster identifier which the object associated with.
// It will add policy labels and annotations to the object, and update the obj with the content returned by the Server, ensuring that the obj used when generating the binding is the latest.
func (d *ResourceDetector) ClaimClusterPolicyForObject(object *unstructured.Unstructured, policy *policyv1alpha1.ClusterPropagationPolicy) (string, error) {
	policyID := policy.Labels[policyv1alpha1.ClusterPropagationPolicyPermanentIDLabel]

	claimedID := util.GetLabelValue(object.GetLabels(), policyv1alpha1.ClusterPropagationPolicyPermanentIDLabel)
	// object has been claimed, don't need to claim again
	if claimedID == policyID {
		return policyID, nil
	}

	AddCPPClaimMetadata(object, policyID, policy.ObjectMeta)
	return policyID, d.Client.Update(context.TODO(), object)
}

// BuildResourceBinding builds a desired ResourceBinding for object.
func (d *ResourceDetector) BuildResourceBinding(object *unstructured.Unstructured, policySpec *policyv1alpha1.PropagationSpec, policyID string, policyMeta metav1.ObjectMeta, claimFunc func(object metav1.Object, policyId string, objectMeta metav1.ObjectMeta)) (*workv1alpha2.ResourceBinding, error) {
	bindingName := names.GenerateBindingName(object.GetKind(), object.GetName())
	propagationBinding := &workv1alpha2.ResourceBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: object.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(object, object.GroupVersionKind()),
			},
			Finalizers: []string{util.BindingControllerFinalizer},
		},
		Spec: workv1alpha2.ResourceBindingSpec{
			PropagateDeps:               policySpec.PropagateDeps,
			SchedulerName:               policySpec.SchedulerName,
			Placement:                   &policySpec.Placement,
			Failover:                    policySpec.Failover,
			ConflictResolution:          policySpec.ConflictResolution,
			PreserveResourcesOnDeletion: policySpec.PreserveResourcesOnDeletion,
			Resource: workv1alpha2.ObjectReference{
				APIVersion:      object.GetAPIVersion(),
				Kind:            object.GetKind(),
				Namespace:       object.GetNamespace(),
				Name:            object.GetName(),
				UID:             object.GetUID(),
				ResourceVersion: object.GetResourceVersion(),
			},
		},
	}

	if policySpec.Suspension != nil {
		propagationBinding.Spec.Suspension = &workv1alpha2.Suspension{Suspension: *policySpec.Suspension}
	}

	claimFunc(propagationBinding, policyID, policyMeta)

	if d.ResourceInterpreter.HookEnabled(object.GroupVersionKind(), configv1alpha1.InterpreterOperationInterpretReplica) {
		replicas, replicaRequirements, err := d.ResourceInterpreter.GetReplicas(object)
		if err != nil {
			klog.Errorf("Failed to customize replicas for %s(%s), %v", object.GroupVersionKind(), object.GetName(), err)
			return nil, err
		}
		propagationBinding.Spec.Replicas = replicas
		propagationBinding.Spec.ReplicaRequirements = replicaRequirements
	}

	if features.FeatureGate.Enabled(features.PriorityBasedScheduling) && policySpec.SchedulePriority != nil {
		var bindingSchedulePriority *workv1alpha2.SchedulePriority
		priorityClassName := policySpec.SchedulePriority.PriorityClassName

		switch policySpec.SchedulePriority.PriorityClassSource {
		case policyv1alpha1.KubePriorityClass:
			kubePriorityClass, err := helper.GetPriorityClassByName(context.TODO(), d.Client, policySpec.SchedulePriority.PriorityClassName)
			if err != nil {
				klog.Errorf("Failed to get Kube PriorityClass(%s): %v", priorityClassName, err)
				return nil, err
			}
			bindingSchedulePriority = &workv1alpha2.SchedulePriority{
				Priority: kubePriorityClass.Value,
				// TODO add preemptionpolicy
				// PreemptionPolicy: kubePriorityClass.PreemptionPolicy,
			}
		case policyv1alpha1.PodPriorityClass:
			return nil, fmt.Errorf("priority class source is PodPriorityClass, but PodPriorityClass is not supported yet")
		case policyv1alpha1.FederatedPriorityClass:
			return nil, fmt.Errorf("priority class source is FederatedPriorityClass, but FederatedPriorityClass is not supported yet")
		default:
			return nil, fmt.Errorf("unsupported priority class source")
		}
		propagationBinding.Spec.SchedulePriority = bindingSchedulePriority
	}

	return propagationBinding, nil
}

// BuildClusterResourceBinding builds a desired ClusterResourceBinding for object.
func (d *ResourceDetector) BuildClusterResourceBinding(object *unstructured.Unstructured,
	policySpec *policyv1alpha1.PropagationSpec, policyID string, policyMeta metav1.ObjectMeta) (*workv1alpha2.ClusterResourceBinding, error) {
	bindingName := names.GenerateBindingName(object.GetKind(), object.GetName())
	binding := &workv1alpha2.ClusterResourceBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: bindingName,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(object, object.GroupVersionKind()),
			},
			Finalizers: []string{util.ClusterResourceBindingControllerFinalizer},
		},
		Spec: workv1alpha2.ResourceBindingSpec{
			PropagateDeps:               policySpec.PropagateDeps,
			SchedulerName:               policySpec.SchedulerName,
			Placement:                   &policySpec.Placement,
			Failover:                    policySpec.Failover,
			ConflictResolution:          policySpec.ConflictResolution,
			PreserveResourcesOnDeletion: policySpec.PreserveResourcesOnDeletion,
			Resource: workv1alpha2.ObjectReference{
				APIVersion:      object.GetAPIVersion(),
				Kind:            object.GetKind(),
				Name:            object.GetName(),
				UID:             object.GetUID(),
				ResourceVersion: object.GetResourceVersion(),
			},
		},
	}

	if policySpec.Suspension != nil {
		binding.Spec.Suspension = &workv1alpha2.Suspension{Suspension: *policySpec.Suspension}
	}

	AddCPPClaimMetadata(binding, policyID, policyMeta)

	if d.ResourceInterpreter.HookEnabled(object.GroupVersionKind(), configv1alpha1.InterpreterOperationInterpretReplica) {
		replicas, replicaRequirements, err := d.ResourceInterpreter.GetReplicas(object)
		if err != nil {
			klog.Errorf("Failed to customize replicas for %s(%s), %v", object.GroupVersionKind(), object.GetName(), err)
			return nil, err
		}
		binding.Spec.Replicas = replicas
		binding.Spec.ReplicaRequirements = replicaRequirements
	}

	return binding, nil
}

// isWaiting indicates if the object is in waiting list.
func (d *ResourceDetector) isWaiting(objectKey keys.ClusterWideKey) bool {
	d.waitingLock.RLock()
	_, ok := d.waitingObjects[objectKey]
	d.waitingLock.RUnlock()
	return ok
}

// AddWaiting adds object's key to waiting list.
func (d *ResourceDetector) AddWaiting(objectKey keys.ClusterWideKey) {
	d.waitingLock.Lock()
	defer d.waitingLock.Unlock()

	d.waitingObjects[objectKey] = struct{}{}
	klog.V(1).Infof("Add object(%s) to waiting list, length of list is: %d", objectKey.String(), len(d.waitingObjects))
}

// RemoveWaiting removes object's key from waiting list.
func (d *ResourceDetector) RemoveWaiting(objectKey keys.ClusterWideKey) {
	d.waitingLock.Lock()
	defer d.waitingLock.Unlock()

	delete(d.waitingObjects, objectKey)
}

// GetMatching gets objects keys in waiting list that matches one of resource selectors.
func (d *ResourceDetector) GetMatching(resourceSelectors []policyv1alpha1.ResourceSelector) []keys.ClusterWideKey {
	d.waitingLock.RLock()
	defer d.waitingLock.RUnlock()

	var matchedResult []keys.ClusterWideKey

	for waitKey := range d.waitingObjects {
		waitObj, err := d.GetUnstructuredObject(waitKey)
		if err != nil {
			// all object in waiting list should exist. Just print a log to trace.
			klog.Errorf("Failed to get object(%s), error: %v", waitKey.String(), err)
			continue
		}

		for _, rs := range resourceSelectors {
			if util.ResourceMatches(waitObj, rs) {
				matchedResult = append(matchedResult, waitKey)
				break
			}
		}
	}

	return matchedResult
}

// OnPropagationPolicyAdd handles object add event and push the object to queue.
func (d *ResourceDetector) OnPropagationPolicyAdd(obj interface{}) {
	d.policyReconcileWorker.Enqueue(obj)
}

// OnPropagationPolicyUpdate handles object update event and push the object to queue.
func (d *ResourceDetector) OnPropagationPolicyUpdate(oldObj, newObj interface{}) {
	d.policyReconcileWorker.Enqueue(newObj)

	// Temporary solution of corner case: After the priority(.spec.priority) of
	// PropagationPolicy changed from high priority (e.g. 5) to low priority(e.g. 3),
	// we should try to check if there is a PropagationPolicy(e.g. with priority 4)
	// could preempt the targeted resources.
	//
	// Recognized limitations of the temporary solution are:
	// - Too much logical processed in an event handler function will slow down
	//   the overall reconcile speed.
	// - If there is an error raised during the process, the event will be lost
	//   and no second chance to retry.
	//
	// The idea of the long-term solution, perhaps PropagationPolicy could have
	// a status, in that case we can record the observed priority(.status.observedPriority)
	// which can be used to detect priority changes during reconcile logic.
	if features.FeatureGate.Enabled(features.PolicyPreemption) {
		oldPolicyObj := oldObj.(*policyv1alpha1.PropagationPolicy)
		policyObj := newObj.(*policyv1alpha1.PropagationPolicy)
		if policyObj.ExplicitPriority() < oldPolicyObj.ExplicitPriority() {
			d.HandleDeprioritizedPropagationPolicy(*oldPolicyObj, *policyObj)
		}
	}
}

// ReconcilePropagationPolicy handles PropagationPolicy resource changes.
// When adding a PropagationPolicy, the detector will pick the objects in waitingObjects list that matches the policy and
// put the object to queue.
// When removing a PropagationPolicy, the relevant ResourceBinding will be removed and
// the relevant objects will be put into queue again to try another policy.
func (d *ResourceDetector) ReconcilePropagationPolicy(key util.QueueKey) error {
	nkey, ok := key.(keys.NamespacedKey)
	if !ok { // should not happen
		klog.Error("Found invalid key when reconciling propagation policy.")
		return fmt.Errorf("invalid key")
	}
	propagationObject := &policyv1alpha1.PropagationPolicy{}
	err := d.Client.Get(context.TODO(), client.ObjectKey{Namespace: nkey.Namespace, Name: nkey.Name}, propagationObject)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		klog.Errorf("Failed to get PropagationPolicy(%s): %v", nkey.NamespaceKey(), err)
		return err
	}

	if !propagationObject.DeletionTimestamp.IsZero() {
		klog.Infof("PropagationPolicy(%s) is being deleted.", nkey.NamespaceKey())
		if err = d.HandlePropagationPolicyDeletion(propagationObject.Labels[policyv1alpha1.PropagationPolicyPermanentIDLabel], propagationObject.Spec.ResourceSelectors); err != nil {
			return err
		}
		if controllerutil.RemoveFinalizer(propagationObject, util.PropagationPolicyControllerFinalizer) {
			if err = d.Client.Update(context.TODO(), propagationObject); err != nil {
				klog.Errorf("Failed to remove finalizer for PropagationPolicy(%s), err: %v", nkey.NamespaceKey(), err)
				return err
			}
		}
		return nil
	}

	klog.Infof("PropagationPolicy(%s) has been added or updated.", nkey.NamespaceKey())
	return d.HandlePropagationPolicyCreationOrUpdate(propagationObject)
}

// OnClusterPropagationPolicyAdd handles object add event and push the object to queue.
func (d *ResourceDetector) OnClusterPropagationPolicyAdd(obj interface{}) {
	d.clusterPolicyReconcileWorker.Enqueue(obj)
}

// OnClusterPropagationPolicyUpdate handles object update event and push the object to queue.
func (d *ResourceDetector) OnClusterPropagationPolicyUpdate(oldObj, newObj interface{}) {
	d.clusterPolicyReconcileWorker.Enqueue(newObj)

	// Temporary solution of corner case: After the priority(.spec.priority) of
	// ClusterPropagationPolicy changed from high priority (e.g. 5) to low priority(e.g. 3),
	// we should try to check if there is a ClusterPropagationPolicy(e.g. with priority 4)
	// could preempt the targeted resources.
	//
	// Recognized limitations of the temporary solution are:
	// - Too much logical processed in an event handler function will slow down
	//   the overall reconcile speed.
	// - If there is an error raised during the process, the event will be lost
	//   and no second chance to retry.
	//
	// The idea of the long-term solution, perhaps ClusterPropagationPolicy could have
	// a status, in that case we can record the observed priority(.status.observedPriority)
	// which can be used to detect priority changes during reconcile logic.
	if features.FeatureGate.Enabled(features.PolicyPreemption) {
		oldPolicy := oldObj.(*policyv1alpha1.ClusterPropagationPolicy)
		policyObj := newObj.(*policyv1alpha1.ClusterPropagationPolicy)
		if policyObj.ExplicitPriority() < oldPolicy.ExplicitPriority() {
			d.HandleDeprioritizedClusterPropagationPolicy(*oldPolicy, *policyObj)
		}
	}
}

// ReconcileClusterPropagationPolicy handles ClusterPropagationPolicy resource changes.
// When adding a ClusterPropagationPolicy, the detector will pick the objects in waitingObjects list that matches the policy and
// put the object to queue.
// When removing a ClusterPropagationPolicy, the relevant ClusterResourceBinding will be removed and
// the relevant objects will be put into queue again to try another policy.
func (d *ResourceDetector) ReconcileClusterPropagationPolicy(key util.QueueKey) error {
	nkey, ok := key.(keys.NamespacedKey)
	if !ok { // should not happen
		klog.Error("Found invalid key when reconciling cluster propagation policy.")
		return fmt.Errorf("invalid key")
	}
	propagationObject := &policyv1alpha1.ClusterPropagationPolicy{}
	err := d.Client.Get(context.TODO(), client.ObjectKey{Name: nkey.Name}, propagationObject)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		klog.Errorf("Failed to get ClusterPropagationPolicy(%s): %v", nkey.NamespaceKey(), err)
		return err
	}

	if !propagationObject.DeletionTimestamp.IsZero() {
		klog.Infof("ClusterPropagationPolicy(%s) is being deleted.", nkey.NamespaceKey())
		if err = d.HandleClusterPropagationPolicyDeletion(propagationObject.Labels[policyv1alpha1.ClusterPropagationPolicyPermanentIDLabel]); err != nil {
			return err
		}
		if controllerutil.RemoveFinalizer(propagationObject, util.ClusterPropagationPolicyControllerFinalizer) {
			if err = d.Client.Update(context.TODO(), propagationObject); err != nil {
				klog.Errorf("Failed to remove finalizer for ClusterPropagationPolicy(%s), err: %v", nkey.NamespaceKey(), err)
				return err
			}
		}
		return nil
	}

	klog.Infof("ClusterPropagationPolicy(%s) has been added or updated.", nkey.NamespaceKey())
	return d.HandleClusterPropagationPolicyCreationOrUpdate(propagationObject)
}

// HandlePropagationPolicyDeletion handles PropagationPolicy delete event.
// After a policy is removed, the label and annotations claimed on relevant resource template will be removed (which gives
// the resource template a change to match another policy).
//
// Note: The relevant ResourceBinding will continue to exist until the resource template is gone.
func (d *ResourceDetector) HandlePropagationPolicyDeletion(policyID string, resourceSelector []policyv1alpha1.ResourceSelector) error {
	var errs []error

	for _, resource := range resourceSelector {
		gvr, err := restmapper.GetGroupVersionResource(d.RESTMapper, schema.FromAPIVersionAndKind(resource.APIVersion, resource.Kind))
		if err != nil {
			klog.Errorf("Failed to convert GVR from GVK(%s/%s), err: %v", resource.APIVersion, resource.Kind, err)
			errs = append(errs, err)
			continue
		}
		err = d.cleanRT(policyID, gvr, CleanupPPClaimMetadata)
		errs = append(errs, err)
	}

	claimMetadata := labels.Set{policyv1alpha1.PropagationPolicyPermanentIDLabel: policyID}
	rbs, err := helper.GetResourceBindings(d.Client, claimMetadata)
	if err != nil {
		klog.Errorf("Failed to list propagation bindings with policy permanentID(%s): %v", policyID, err)
		return err
	}

	for index, binding := range rbs.Items {
		// Clean up the claim metadata from the reference binding so that the karmada scheduler won't reschedule the binding.
		if err := d.CleanupResourceBindingClaimMetadata(&rbs.Items[index], claimMetadata, CleanupPPClaimMetadata); err != nil {
			klog.Errorf("Failed to clean up claim metadata from resource binding(%s/%s) when propagationPolicy removed, error: %v",
				binding.Namespace, binding.Name, err)
			errs = append(errs, err)
		}
	}
	return errors.NewAggregate(errs)
}

// HandleClusterPropagationPolicyDeletion handles ClusterPropagationPolicy delete event.
// After a policy is removed, the label and annotation claimed on relevant resource template will be removed (which gives
// the resource template a change to match another policy).
//
// Note: The relevant ClusterResourceBinding or ResourceBinding will continue to exist until the resource template is gone.
func (d *ResourceDetector) HandleClusterPropagationPolicyDeletion(policyID string) error {
	var errs []error
	labelSet := labels.Set{
		policyv1alpha1.ClusterPropagationPolicyPermanentIDLabel: policyID,
	}

	// load the ClusterResourceBindings which labeled with current policy
	crbs, err := helper.GetClusterResourceBindings(d.Client, labelSet)
	if err != nil {
		klog.Errorf("Failed to list clusterResourceBindings with clusterPropagationPolicy permanentID(%s), error: %v", policyID, err)
		errs = append(errs, err)
	} else if len(crbs.Items) > 0 {
		for index, binding := range crbs.Items {
			// Must remove the claim metadata, such as labels and annotations, from the resource template ahead of
			// ClusterResourceBinding, otherwise might lose the chance to do that in a retry loop (in particular, the
			// claim metadata was successfully removed from ClusterResourceBinding, but resource template not), since the
			// ClusterResourceBinding will not be listed again.
			if err := d.CleanupResourceTemplateClaimMetadata(binding.Spec.Resource, labelSet, CleanupCPPClaimMetadata); err != nil {
				klog.Errorf("Failed to clean up claim metadata from resource(%s-%s) when clusterPropagationPolicy removed, error: %v",
					binding.Spec.Resource.Kind, binding.Spec.Resource.Name, err)
				// Skip cleaning up policy labels and annotations from ClusterResourceBinding, give a chance to do that in a retry loop.
				continue
			}

			// Clean up the claim metadata from the reference binding so that the Karmada scheduler won't reschedule the binding.
			if err := d.CleanupClusterResourceBindingClaimMetadata(&crbs.Items[index], labelSet); err != nil {
				klog.Errorf("Failed to clean up claim metadata from clusterResourceBinding(%s) when clusterPropagationPolicy removed, error: %v",
					binding.Name, err)
				errs = append(errs, err)
			}
		}
	}

	// load the ResourceBindings which labeled with current policy
	rbs, err := helper.GetResourceBindings(d.Client, labelSet)
	if err != nil {
		klog.Errorf("Failed to list resourceBindings with clusterPropagationPolicy permanentID(%s), error: %v", policyID, err)
		errs = append(errs, err)
	} else if len(rbs.Items) > 0 {
		for index, binding := range rbs.Items {
			// Must remove the claim metadata, such as labels and annotations, from the resource template ahead of ResourceBinding,
			// otherwise might lose the chance to do that in a retry loop (in particular, the label was successfully
			// removed from ResourceBinding, but resource template not), since the ResourceBinding will not be listed again.
			if err := d.CleanupResourceTemplateClaimMetadata(binding.Spec.Resource, labelSet, CleanupCPPClaimMetadata); err != nil {
				klog.Errorf("Failed to clean up claim metadata from resource(%s-%s/%s) when clusterPropagationPolicy removed, error: %v",
					binding.Spec.Resource.Kind, binding.Spec.Resource.Namespace, binding.Spec.Resource.Name, err)
				errs = append(errs, err)
				// Skip cleaning up policy labels and annotations from ResourceBinding, give a chance to do that in a retry loop.
				continue
			}

			// Clean up the claim metadata from the reference binding so that the Karmada scheduler won't reschedule the binding.
			if err := d.CleanupResourceBindingClaimMetadata(&rbs.Items[index], labelSet, CleanupCPPClaimMetadata); err != nil {
				klog.Errorf("Failed to clean up claim metadata from resourceBinding(%s/%s) when clusterPropagationPolicy removed, error: %v",
					binding.Namespace, binding.Name, err)
				errs = append(errs, err)
			}
		}
	}
	return errors.NewAggregate(errs)
}

// HandlePropagationPolicyCreationOrUpdate handles PropagationPolicy add and update event.
// When a new policy arrives, should check whether existing objects are no longer matched by the current policy,
// if yes, clean the labels on the object.
// And then check if object in waiting list matches the policy, if yes remove the object
// from waiting list and throw the object to it's reconcile queue. If not, do nothing.
// Finally, handle the propagation policy preemption process if preemption is enabled.
func (d *ResourceDetector) HandlePropagationPolicyCreationOrUpdate(policy *policyv1alpha1.PropagationPolicy) error {
	// If the Policy's ResourceSelectors change, causing certain resources to no longer match the Policy, the label claimed
	// on relevant resource template will be removed (which gives the resource template a change to match another policy).
	policyID := policy.Labels[policyv1alpha1.PropagationPolicyPermanentIDLabel]
	err := d.cleanPPUnmatchedRBs(policyID, policy.Namespace, policy.Name, policy.Spec.ResourceSelectors)
	if err != nil {
		return err
	}

	// When updating fields other than ResourceSelector, should first find the corresponding ResourceBinding
	// and add the bound object to the processor's queue for reconciliation to make sure that
	// PropagationPolicy's updates can be synchronized to ResourceBinding.
	resourceBindings, err := d.listPPDerivedRBs(policyID, policy.Namespace, policy.Name)
	if err != nil {
		return err
	}
	for _, rb := range resourceBindings.Items {
		resourceKey, err := helper.ConstructClusterWideKey(rb.Spec.Resource)
		if err != nil {
			return err
		}
		d.Processor.Add(keys.ClusterWideKeyWithConfig{ClusterWideKey: resourceKey, ResourceChangeByKarmada: true})
	}

	// check whether there are matched RT in waiting list, is so, add it to processor
	matchedKeys := d.GetMatching(policy.Spec.ResourceSelectors)
	klog.Infof("Matched %d resources by policy(%s/%s)", len(matchedKeys), policy.Namespace, policy.Name)

	// check dependents only when there at least a real match.
	if len(matchedKeys) > 0 {
		// return err when dependents not present, that we can retry at next reconcile.
		if present, err := helper.IsDependentOverridesPresent(d.Client, policy); err != nil || !present {
			klog.Infof("Waiting for dependent overrides present for policy(%s/%s)", policy.Namespace, policy.Name)
			return fmt.Errorf("waiting for dependent overrides")
		}
	}

	for _, key := range matchedKeys {
		d.RemoveWaiting(key)
		d.Processor.Add(keys.ClusterWideKeyWithConfig{ClusterWideKey: key, ResourceChangeByKarmada: true})
	}

	// If preemption is enabled, handle the preemption process.
	// If this policy succeeds in preempting resource managed by other policy, the label claimed on relevant resource
	// will be replaced, which gives the resource template a change to match to this policy.
	if preemptionEnabled(policy.Spec.Preemption) {
		return d.handlePropagationPolicyPreemption(policy)
	}

	return nil
}

// HandleClusterPropagationPolicyCreationOrUpdate handles ClusterPropagationPolicy add and update event.
// When a new policy arrives, should check whether existing objects are no longer matched by the current policy,
// if yes, clean the labels on the object.
// And then check if object in waiting list matches the policy, if yes remove the object
// from waiting list and throw the object to it's reconcile queue. If not, do nothing.
// Finally, handle the cluster propagation policy preemption process if preemption is enabled.
func (d *ResourceDetector) HandleClusterPropagationPolicyCreationOrUpdate(policy *policyv1alpha1.ClusterPropagationPolicy) error {
	// If the Policy's ResourceSelectors change, causing certain resources to no longer match the Policy, the label claimed
	// on relevant resource template will be removed (which gives the resource template a change to match another policy).
	policyID := policy.Labels[policyv1alpha1.ClusterPropagationPolicyPermanentIDLabel]
	err := d.cleanCPPUnmatchedRBs(policyID, policy.Name, policy.Spec.ResourceSelectors)
	if err != nil {
		return err
	}

	err = d.cleanUnmatchedCRBs(policyID, policy.Name, policy.Spec.ResourceSelectors)
	if err != nil {
		return err
	}

	// When updating fields other than ResourceSelector, should first find the corresponding ResourceBinding/ClusterResourceBinding
	// and add the bound object to the processor's queue for reconciliation to make sure that
	// ClusterPropagationPolicy's updates can be synchronized to ResourceBinding/ClusterResourceBinding.
	resourceBindings, err := d.listCPPDerivedRBs(policyID, policy.Name)
	if err != nil {
		return err
	}
	clusterResourceBindings, err := d.listCPPDerivedCRBs(policyID, policy.Name)
	if err != nil {
		return err
	}
	for _, rb := range resourceBindings.Items {
		resourceKey, err := helper.ConstructClusterWideKey(rb.Spec.Resource)
		if err != nil {
			return err
		}
		d.Processor.Add(keys.ClusterWideKeyWithConfig{ClusterWideKey: resourceKey, ResourceChangeByKarmada: true})
	}
	for _, crb := range clusterResourceBindings.Items {
		resourceKey, err := helper.ConstructClusterWideKey(crb.Spec.Resource)
		if err != nil {
			return err
		}
		d.Processor.Add(keys.ClusterWideKeyWithConfig{ClusterWideKey: resourceKey, ResourceChangeByKarmada: true})
	}

	matchedKeys := d.GetMatching(policy.Spec.ResourceSelectors)
	klog.Infof("Matched %d resources by policy(%s)", len(matchedKeys), policy.Name)

	// check dependents only when there at least a real match.
	if len(matchedKeys) > 0 {
		// return err when dependents not present, that we can retry at next reconcile.
		if present, err := helper.IsDependentClusterOverridesPresent(d.Client, policy); err != nil || !present {
			klog.Infof("Waiting for dependent overrides present for policy(%s)", policy.Name)
			return fmt.Errorf("waiting for dependent overrides")
		}
	}

	for _, key := range matchedKeys {
		d.RemoveWaiting(key)
		d.Processor.Add(keys.ClusterWideKeyWithConfig{ClusterWideKey: key, ResourceChangeByKarmada: true})
	}

	// If preemption is enabled, handle the preemption process.
	// If this policy succeeds in preempting resource managed by other policy, the label claimed on relevant resource
	// will be replaced, which gives the resource template a change to match to this policy.
	if preemptionEnabled(policy.Spec.Preemption) {
		return d.handleClusterPropagationPolicyPreemption(policy)
	}

	return nil
}

// CleanupResourceTemplateClaimMetadata removes claim metadata, such as labels and annotations, from object referencing by objRef.
func (d *ResourceDetector) CleanupResourceTemplateClaimMetadata(objRef workv1alpha2.ObjectReference, targetClaimMetadata map[string]string, cleanupFunc func(obj metav1.Object)) error {
	gvr, err := restmapper.GetGroupVersionResource(d.RESTMapper, schema.FromAPIVersionAndKind(objRef.APIVersion, objRef.Kind))
	if err != nil {
		klog.Errorf("Failed to convert GVR from GVK(%s/%s), err: %v", objRef.APIVersion, objRef.Kind, err)
		return err
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		workload, err := d.DynamicClient.Resource(gvr).Namespace(objRef.Namespace).Get(context.TODO(), objRef.Name, metav1.GetOptions{})
		if err != nil {
			// do nothing if resource template not exist, it might have been removed.
			if apierrors.IsNotFound(err) {
				return nil
			}
			klog.Errorf("Failed to fetch resource(kind=%s, %s/%s): err is %v", objRef.Kind, objRef.Namespace, objRef.Name, err)
			return err
		}

		if !NeedCleanupClaimMetadata(workload, targetClaimMetadata) {
			klog.Infof("No need to clean up the claim metadata on resource(kind=%s, %s/%s) since they have changed", workload.GetKind(), workload.GetNamespace(), workload.GetName())
			return nil
		}

		cleanupFunc(workload)

		_, err = d.DynamicClient.Resource(gvr).Namespace(workload.GetNamespace()).Update(context.TODO(), workload, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("Failed to update resource(kind=%s, %s/%s): err is %v", workload.GetKind(), workload.GetNamespace(), workload.GetName(), err)
			return err
		}
		klog.V(2).Infof("Updated resource template(kind=%s, %s/%s) successfully", workload.GetKind(), workload.GetNamespace(), workload.GetName())
		return nil
	})
}

// CleanupResourceBindingClaimMetadata removes claim metadata, such as labels and annotations, from resource binding.
func (d *ResourceDetector) CleanupResourceBindingClaimMetadata(rb *workv1alpha2.ResourceBinding, targetClaimMetadata map[string]string, cleanupFunc func(obj metav1.Object)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		if !NeedCleanupClaimMetadata(rb, targetClaimMetadata) {
			klog.Infof("No need to clean up the claim metadata on ResourceBinding(%s/%s) since they have changed", rb.GetNamespace(), rb.GetName())
			return nil
		}
		cleanupFunc(rb)
		updateErr := d.Client.Update(context.TODO(), rb)
		if updateErr == nil {
			return nil
		}

		updated := &workv1alpha2.ResourceBinding{}
		if err = d.Client.Get(context.TODO(), client.ObjectKey{Namespace: rb.GetNamespace(), Name: rb.GetName()}, updated); err == nil {
			rb = updated.DeepCopy()
		} else {
			klog.Errorf("Failed to get updated ResourceBinding(%s/%s): %v", rb.GetNamespace(), rb.GetName(), err)
		}
		return updateErr
	})
}

// CleanupClusterResourceBindingClaimMetadata removes claim metadata, such as labels and annotations, from cluster resource binding.
func (d *ResourceDetector) CleanupClusterResourceBindingClaimMetadata(crb *workv1alpha2.ClusterResourceBinding, targetClaimMetadata map[string]string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		if !NeedCleanupClaimMetadata(crb, targetClaimMetadata) {
			klog.Infof("No need to clean up the claim metadata on ClusterResourceBinding(%s) since they have changed", crb.GetName())
			return nil
		}
		CleanupCPPClaimMetadata(crb)
		updateErr := d.Client.Update(context.TODO(), crb)
		if updateErr == nil {
			return nil
		}

		updated := &workv1alpha2.ClusterResourceBinding{}
		if err = d.Client.Get(context.TODO(), client.ObjectKey{Name: crb.GetName()}, updated); err == nil {
			crb = updated.DeepCopy()
		} else {
			klog.Errorf("Failed to get updated ClusterResourceBinding(%s):: %v", crb.GetName(), err)
		}
		return updateErr
	})
}

func (d *ResourceDetector) cleanRT(policyID string, gvr schema.GroupVersionResource, cleanupFunc func(obj metav1.Object)) error {
	informer := d.InformerManager.GetInformer(gvr)
	if informer == nil {
		return fmt.Errorf("Informer not found for GVR: %s", gvr.String())
	}

	objs, err := informer.Informer().GetIndexer().ByIndex("bylabel", policyID)
	if err != nil {
		klog.Errorf("Failed to get objects by index 'bylabel' with value '%s' for GVR %s: %v", policyID, gvr.String(), err)
		return err
	}

	var errs []error
	claimMetadata := labels.Set{policyv1alpha1.PropagationPolicyPermanentIDLabel: policyID}
	for _, obj := range objs {
		if rt, ok := obj.(*unstructured.Unstructured); ok {
			err = d.cleanupResourceTemplateClaimMetadata(gvr, rt, claimMetadata, cleanupFunc)
			errs = append(errs, err)
		} else {
			errs = append(errs, fmt.Errorf("%s/%s", rt.GetNamespace(), rt.GetName()))
		}
	}

	return errors.NewAggregate(errs)
}

func (d *ResourceDetector) cleanupResourceTemplateClaimMetadata(gvr schema.GroupVersionResource, workload *unstructured.Unstructured, targetClaimMetadata map[string]string, cleanupFunc func(obj metav1.Object)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		if !NeedCleanupClaimMetadata(workload, targetClaimMetadata) {
			klog.Infof("No need to clean up the claim metadata on resource(kind=%s, %s/%s) since they have changed", workload.GetKind(), workload.GetNamespace(), workload.GetName())
			return nil
		}

		cleanupFunc(workload)

		_, err = d.DynamicClient.Resource(gvr).Namespace(workload.GetNamespace()).Update(context.TODO(), workload, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("Failed to update resource(kind=%s, %s/%s): err is %v", workload.GetKind(), workload.GetNamespace(), workload.GetName(), err)
			if apierrors.IsConflict(err) {
				workload, err = d.DynamicClient.Resource(gvr).Namespace(workload.GetNamespace()).Get(context.TODO(), workload.GetName(), metav1.GetOptions{})
				if err != nil {
					// do nothing if resource template not exist, it might have been removed.
					if apierrors.IsNotFound(err) {
						return nil
					}
					klog.Errorf("Failed to fetch resource(kind=%s, %s/%s): err is %v", workload.GetKind(), workload.GetNamespace(), workload.GetName(), err)
					return err
				}
			}
			return err
		}
		klog.V(2).Infof("Updated resource template(kind=%s, %s/%s) successfully", workload.GetKind(), workload.GetNamespace(), workload.GetName())
		return nil
	})
}

// findDeploymentsByAppIndex 通过 app 索引查找所有带指定 app label 的 Deployment 对象
func (d *ResourceDetector) findDeploymentsByAppIndex(appValue string) ([]*unstructured.Unstructured, error) {
	gvr := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}
	informer := d.InformerManager.GetInformer(gvr)
	objs, err := informer.Informer().GetIndexer().ByIndex("bylabel", appValue)
	if err != nil {
		return nil, err
	}
	var result []*unstructured.Unstructured
	for _, obj := range objs {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			result = append(result, u)
		}
	}
	return result, nil
}
