/*
Copyright 2026.

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

package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	kubeflowv1 "github.com/kubeflow/training-operator/pkg/apis/kubeflow.org/v1"
	kubeflowutil "github.com/kubeflow/training-operator/pkg/util"
	cronv3 "github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/cron-operator/api/v1alpha1"
	"github.com/AliyunContainerService/cron-operator/pkg/common"
)

// CronReconciler reconciles a Cron object.
type CronReconciler struct {
	scheme   *runtime.Scheme
	client   client.Client
	reader   client.Reader
	recorder record.EventRecorder
}

// CronReconciler implements reconcile.Reconciler.
var _ reconcile.Reconciler = &CronReconciler{}

// NewCronReconciler creates a new CronReconciler instance.
func NewCronReconciler(s *runtime.Scheme, c client.Client, r client.Reader, recorder record.EventRecorder) *CronReconciler {
	return &CronReconciler{
		scheme:   s,
		client:   c,
		reader:   r,
		recorder: recorder,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *CronReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Cron{}).
		Owns(&kubeflowv1.PyTorchJob{}).
		Owns(&kubeflowv1.TFJob{}).
		WithLogConstructor(logConstructor(mgr.GetLogger(), "cron")).
		Complete(r)
}

// +kubebuilder:rbac:groups=kubedl.io,resources=crons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubedl.io,resources=crons/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubedl.io,resources=crons/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubeflow.org,resources=pytorchjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubeflow.org,resources=pytorchjobs/status,verbs=get
// +kubebuilder:rbac:groups=kubeflow.org,resources=tfjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubeflow.org,resources=tfjobs/status,verbs=get

// Reconcile is the main reconciliation loop for Cron objects.
// It ensures that the current state of the cluster (active workloads) matches
// the desired state defined in the Cron schedule.
func (r *CronReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, reconcileErr error) {
	log := logf.FromContext(ctx)
	log.Info("Start reconciling Cron")
	defer log.Info("Finish reconciling Cron")

	// Get the Cron object from cache.
	oldCron := &v1alpha1.Cron{}
	if err := r.client.Get(ctx, req.NamespacedName, oldCron); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Skip reconciling Cron for it may have been deleted")
			return ctrl.Result{}, nil
		}
		// Requeue the request when there is an error getting the Cron object.
		return ctrl.Result{}, err
	}
	cron := oldCron.DeepCopy()

	defer func() {
		if apiequality.Semantic.DeepEqual(oldCron.Status, cron.Status) {
			return
		}

		if err := r.client.Status().Patch(ctx, cron, client.MergeFrom(oldCron)); err != nil {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("failed to patch Cron status: %w", err))
		}

		// Suppress controller-runtime warnings when returning a non-empty ctrl.Result and a non-nil error.
		if reconcileErr != nil {
			result = ctrl.Result{}
		}
	}()

	gvk, err := getWorkloadGVK(cron)
	if err != nil {
		log.Error(err, "Failed to get workload GVK")
		return ctrl.Result{}, nil
	}

	// List all workloads owned by this Cron.
	workloads, err := r.listWorkloads(ctx, cron)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to list %s", gvk.Kind))
		return ctrl.Result{}, err
	}

	// Filter workloads into active and terminated lists.
	activeWorkloads := []client.Object{}
	terminatedWorkloads := []client.Object{}
	for _, workload := range workloads {
		status, err := getJobStatus(workload)
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to get %s status", gvk.Kind))
			continue
		}

		switch {
		case kubeflowutil.IsSucceeded(status) || kubeflowutil.IsFailed(status):
			terminatedWorkloads = append(terminatedWorkloads, workload)
		default:
			activeWorkloads = append(activeWorkloads, workload)
		}
	}
	log.Info(fmt.Sprintf("%s count", gvk.Kind), "active", len(activeWorkloads), "terminated", len(terminatedWorkloads))

	// Sync Cron status with active and terminated workloads.
	if err := r.syncStatus(ctx, cron, activeWorkloads, terminatedWorkloads); err != nil {
		log.Error(err, "Failed to sync Cron status")
		return ctrl.Result{}, err
	}

	now := time.Now()

	// Check if the Cron has been deleted.
	if cron.DeletionTimestamp != nil {
		log.Info("Cron has been deleted", "deletionTimestamp", cron.DeletionTimestamp)
		return ctrl.Result{}, nil
	}

	// Check if the Cron has been suspended.
	suspend := ptr.Deref(cron.Spec.Suspend, false)
	if suspend {
		log.Info("Cron has been suspended")
		return ctrl.Result{}, nil
	}

	// Check if the Cron has reached its deadline.
	if cron.Spec.Deadline != nil && now.After(cron.Spec.Deadline.Time) {
		log.Info("Cron has reached deadline and will not trigger scheduling anymore")
		r.recorder.Event(cron, corev1.EventTypeNormal, "Deadline", "cron has reach deadline and stop scheduling")
		return ctrl.Result{}, nil
	}

	// figure out the next times that we need to create
	// jobs at (or anything we missed).
	missedRun, nextRun, err := r.getNextSchedule(ctx, cron, now)
	if err != nil {
		log.Error(err, "Failed to figure out CronJob schedule")
		// we don't really care about requeuing until we get an update that
		// fixes the schedule, so don't return an error
		return ctrl.Result{}, nil
	}

	scheduledResult := ctrl.Result{RequeueAfter: nextRun.Sub(now)}
	log = log.WithValues("now", now, "next run", nextRun)

	// If we've missed a run, and we're still within the deadline to start it, we'll need to run a job.
	if missedRun.IsZero() {
		log.V(1).Info("No upcoming schedules, wait until next")
		return scheduledResult, nil
	}

	log = log.WithValues("current run", missedRun)

	// Handle concurrency policy forbid.
	if cron.Spec.ConcurrencyPolicy == v1alpha1.ConcurrentPolicyForbid && len(activeWorkloads) > 0 {
		log.V(1).Info(fmt.Sprintf("Skip creating new %s due to concurrency policy forbid", gvk.Kind), "active", len(activeWorkloads))
		return scheduledResult, nil
	}

	// Handle concurrency policy replace.
	if cron.Spec.ConcurrencyPolicy == v1alpha1.ConcurrentPolicyReplace {
		for _, workload := range activeWorkloads {
			// we don't care if the job was already deleted
			objectRef := klog.KRef(workload.GetNamespace(), workload.GetName())
			log.Info(fmt.Sprintf("Deleting active %s", gvk.Kind), gvk.Kind, objectRef)
			if err := r.client.Delete(ctx, workload, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
				log.Error(err, fmt.Sprintf("Failed to delete active %s", gvk.Kind), gvk.Kind, objectRef)
				return ctrl.Result{}, err
			}
		}
	}

	workload, err := r.newWorkloadFromTemplate(cron, nextRun)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to initialize %s from cron template: %v", gvk.Kind, err)
	}

	objectRef := klog.KRef(workload.GetNamespace(), workload.GetName())
	log.Info(fmt.Sprintf("Creating %s", gvk.Kind), gvk.Kind, objectRef)
	if err := r.client.Create(ctx, workload); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Info(fmt.Sprintf("%s already exists", gvk.Kind), gvk.Kind, objectRef)
		} else {
			r.recorder.Eventf(cron, corev1.EventTypeWarning, "FailedCreate", "Error creating %s: %v", gvk.Kind, err)
			return ctrl.Result{}, err
		}
	}
	cron.Status.LastScheduleTime = ptr.To(metav1.Time{Time: now})
	return scheduledResult, nil
}

// List all workloads owned by the given Cron object.
func (r *CronReconciler) listWorkloads(ctx context.Context, cron *v1alpha1.Cron) ([]client.Object, error) {
	log := logf.FromContext(ctx)

	workload, err := newEmptyWorkload(cron)
	if err != nil {
		return nil, err
	}
	gvk := workload.GetObjectKind().GroupVersionKind()

	log.V(1).Info(fmt.Sprintf("Listing %s", gvk.Kind))
	uList := unstructured.UnstructuredList{}
	uList.SetGroupVersionKind(gvk)
	matchingLabels := map[string]string{
		common.LabelCronName: cron.Name,
	}
	if err := r.client.List(ctx, &uList, client.InNamespace(cron.Namespace), client.MatchingLabels(matchingLabels)); err != nil {
		return nil, err
	}

	workloads := make([]client.Object, len(uList.Items))
	for i, u := range uList.Items {
		workloads[i] = &u
	}
	return workloads, nil
}

// Sync Cron status.
func (r *CronReconciler) syncStatus(ctx context.Context, cron *v1alpha1.Cron, activeWorkloads []client.Object, terminatedWorkloads []client.Object) error {
	log := logf.FromContext(ctx)
	log.V(1).Info("Syncing Cron status")

	if err := r.syncActiveList(ctx, cron, activeWorkloads); err != nil {
		return err
	}

	if err := r.syncCronHistory(ctx, cron, terminatedWorkloads); err != nil {
		return err
	}

	return nil
}

// Sync active list.
func (r *CronReconciler) syncActiveList(ctx context.Context, cron *v1alpha1.Cron, activeWorkloads []client.Object) error {
	log := logf.FromContext(ctx)
	log.V(1).Info("Syncing active list")

	sortByCreationTimestamp(activeWorkloads)
	refs := make([]corev1.ObjectReference, len(activeWorkloads))
	for i, workload := range activeWorkloads {
		gvk := workload.GetObjectKind().GroupVersionKind()
		refs[i] = corev1.ObjectReference{
			APIVersion:      gvk.GroupVersion().String(),
			Kind:            gvk.Kind,
			Name:            workload.GetName(),
			Namespace:       workload.GetNamespace(),
			UID:             workload.GetUID(),
			ResourceVersion: workload.GetResourceVersion(),
		}
	}
	cron.Status.Active = refs
	return nil
}

// Sync Cron history.
func (r *CronReconciler) syncCronHistory(ctx context.Context, cron *v1alpha1.Cron, terminatedWorkloads []client.Object) error {
	log := logf.FromContext(ctx)
	log.V(1).Info("Syncing Cron history")

	sortByCreationTimestamp(terminatedWorkloads)

	n := len(terminatedWorkloads)
	history := []v1alpha1.CronHistory{}
	historyLimit := ptr.Deref(cron.Spec.HistoryLimit, math.MaxInt)
	for i, workload := range terminatedWorkloads {
		gvk := workload.GetObjectKind().GroupVersionKind()
		objectRef := klog.KRef(workload.GetNamespace(), workload.GetName())
		if i < n-historyLimit {
			log.Info(fmt.Sprintf("Deleting terminated %s", gvk.Kind), gvk.Kind, objectRef)
			if err := r.client.Delete(ctx, workload, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
				log.Error(err, fmt.Sprintf("Failed to delete terminated %s", gvk.Kind), gvk.Kind, objectRef)
			}
		} else {
			status, finished := isWorkloadFinished(workload)
			entry := v1alpha1.CronHistory{
				UID: workload.GetUID(),
				Object: corev1.TypedLocalObjectReference{
					// For backward compatibility, we pass group/version instead of just group.
					APIGroup: ptr.To(gvk.GroupVersion().String()),
					Kind:     gvk.Kind,
					Name:     workload.GetName(),
				},
				Status:  status,
				Created: ptr.To(workload.GetCreationTimestamp()),
			}
			if finished {
				entry.Finished = ptr.To(metav1.Now())
			}
			history = append(history, entry)
		}
	}

	cron.Status.History = history
	return nil
}

// newWorkloadFromTemplate creates a new workload from a cron template.
func (r *CronReconciler) newWorkloadFromTemplate(cron *v1alpha1.Cron, scheduleTime time.Time) (client.Object, error) {
	w, err := newEmptyWorkload(cron)
	if err != nil {
		return nil, err
	}

	// Set generateName to empty if specified.
	if len(w.GetGenerateName()) != 0 {
		// Cron does not allow users to set customized generateName, because generated name
		// is suffixed with a randomized string which is not unique, so duplicated scheduling
		// is possible when cron-controller fail-over or fail to update status when workload
		// created, so we forcibly emptied it here.
		w.SetGenerateName("")
	}

	// Set name if not specified.
	if len(w.GetName()) == 0 {
		w.SetName(getDefaultJobName(cron, scheduleTime))
	} else {
		r.recorder.Event(cron, corev1.EventTypeNormal, "OverridePolicy", "metadata.name has been specified in workload template, override cron concurrency policy as Forbidden")
		cron.Spec.ConcurrencyPolicy = v1alpha1.ConcurrentPolicyForbid
	}
	w.SetNamespace(cron.Namespace)

	// Set labels.
	labels := w.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[common.LabelCronName] = cron.Name
	w.SetLabels(labels)

	// Set controller owner reference.
	if err := controllerutil.SetControllerReference(cron, w, r.scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller owner reference: %v", err)
	}

	return w, err
}

func (r *CronReconciler) getNextSchedule(ctx context.Context, cron *v1alpha1.Cron, now time.Time) (lastMissed time.Time, next time.Time, err error) {
	log := logf.FromContext(ctx)

	sched, err := cronv3.ParseStandard(cron.Spec.Schedule)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("unparsable cron %q: %w", cron.Spec.Schedule, err)
	}

	var earliestTime time.Time
	if cron.Status.LastScheduleTime != nil {
		earliestTime = cron.Status.LastScheduleTime.Time
	} else {
		earliestTime = cron.CreationTimestamp.Time
	}

	if earliestTime.After(now) {
		return time.Time{}, sched.Next(now), nil
	}

	missedTimes := 0
	for t := sched.Next(earliestTime); !t.After(now); t = sched.Next(t) {
		if t.IsZero() {
			return time.Time{}, time.Time{}, fmt.Errorf("unschedulable cron %q: %w", cron.Spec.Schedule, err)
		}

		lastMissed = t
		// An object might miss several starts. For example, if
		// controller gets wedged on Friday at 5:01pm when everyone has
		// gone home, and someone comes in on Tuesday AM and discovers
		// the problem and restarts the controller, then all the hourly
		// jobs, more than 80 of them for one hourly scheduledJob, should
		// all start running with no further intervention (if the scheduledJob
		// allows concurrency and late starts).
		//
		// However, if there is a bug somewhere, or incorrect clock
		// on controller's server or apiservers (for setting creationTimestamp)
		// then there could be so many missed start times (it could be off
		// by decades or more), that it would eat up all the CPU and memory
		// of this controller. In that case, we want to not try to list
		// all the missed start times.
		missedTimes++
	}
	if missedTimes > 100 {
		r.recorder.Eventf(cron, corev1.EventTypeWarning, "TooManyMissedTimes", "too many missed start times: %d. Check clock skew", missedTimes)
		log.Info("Too many missed times", "missed times", missedTimes)
	}

	return lastMissed, sched.Next(now), nil
}
