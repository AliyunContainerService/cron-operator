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
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	kubeflowv1 "github.com/kubeflow/training-operator/pkg/apis/kubeflow.org/v1"
	cronv3 "github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
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
		WithLogConstructor(LogConstructor(mgr.GetLogger(), "cron")).
		Complete(r)
}

// +kubebuilder:rbac:groups=kubedl.io,resources=crons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubedl.io,resources=crons/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubedl.io,resources=crons/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubeflow.org,resources=pytorchjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubeflow.org,resources=pytorchjobs/status,verbs=get
// +kubebuilder:rbac:groups=kubeflow.org,resources=tfjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubeflow.org,resources=tfjobs/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The controller will reconcile the Cron object and create/manage jobs based on the cron schedule.
func (r *CronReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Start reconciling")
	defer log.Info("Finish reconciling")

	cron := &v1alpha1.Cron{}
	if err := r.client.Get(ctx, req.NamespacedName, cron); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Cron has been deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	actives, err := r.listActiveWorkloads(ctx, cron)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err = r.updateCronHistory(ctx, cron, actives); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfeter, err := r.syncCron(ctx, cron, actives)
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeueAfeter != nil {
		return ctrl.Result{RequeueAfter: *requeueAfeter}, nil
	}

	return ctrl.Result{}, nil
}

func (r *CronReconciler) syncCron(ctx context.Context, cron *v1alpha1.Cron, workloads []client.Object) (*time.Duration, error) {
	log := logf.FromContext(ctx)
	now := time.Now()

	// 1) trim finished and missed workload from active list.
	if err := r.trimFinishedWorkloadsFromActiveList(ctx, cron, workloads); err != nil {
		return nil, err
	}

	// 2) apply latest cron status to cluster.
	if err := r.client.Status().Update(ctx, cron); err != nil {
		log.Error(err, "unable to update status for cron %s/%s ", cron.Namespace, cron.Name)
		return nil, err
	}

	// 3) check deletion/suspend/deadline state from retrieved cron object.
	if cron.DeletionTimestamp != nil {
		log.V(1).Info(fmt.Sprintf("Cron has been deleted at %v", cron.DeletionTimestamp))
		return nil, nil
	}

	if cron.Spec.Suspend != nil && *cron.Spec.Suspend {
		log.V(1).Info("Cron has been suspended")
		return nil, nil
	}

	if cron.Spec.Deadline != nil && now.After(cron.Spec.Deadline.Time) {
		log.V(1).Info("Cron has reached deadline and will not trigger scheduling anymore")
		r.recorder.Event(cron, corev1.EventTypeNormal, "Deadline", "cron has reach deadline and stop scheduling")
		return nil, nil
	}

	// 4) schedule next workload if schedule time has come.
	nextDuration, err := r.scheduleNextIfPossible(ctx, cron, now)
	if err != nil {
		log.Error(err, "unable to schedule next workload")
		return nil, err
	}
	return nextDuration, nil
}

func (r *CronReconciler) scheduleNextIfPossible(ctx context.Context, cron *v1alpha1.Cron, now time.Time) (*time.Duration, error) {
	log := logf.FromContext(ctx)
	schedule, err := cronv3.ParseStandard(cron.Spec.Schedule)
	if err != nil {
		// this is likely a user error in defining the spec value
		// we should log the error and not reconcile this cronjob until an update to spec.
		msg := fmt.Sprintf("failed to parse schedule %s: %v", cron.Spec.Schedule, err)
		log.Info(msg)
		r.recorder.Eventf(cron, corev1.EventTypeWarning, "InvalidSchedule", msg)
		return nil, nil
	}

	scheduledTime, err := getNextScheduleTime(cron, now, schedule, r.recorder)
	if err != nil {
		// this is likely a user error in defining the spec value
		// we should log the error and not reconcile this cronjob until an update to spec
		msg := fmt.Sprintf("invalid schedule: %s", cron.Spec.Schedule)
		log.Error(err, msg)
		r.recorder.Event(cron, corev1.EventTypeWarning, "InvalidSchedule", msg)
		return nil, err
	}

	if scheduledTime == nil {
		log.V(1).Info("No unmet start time")
		return nextScheduledTimeDuration(schedule, now), nil
	}

	if cron.Status.LastScheduleTime.Equal(&metav1.Time{Time: *scheduledTime}) {
		log.V(1).Info("Not starting because the scheduled time is already precessed, cron: %s/%s", cron.Namespace, cron.Name)
		return nextScheduledTimeDuration(schedule, now), nil
	}

	if cron.Spec.ConcurrencyPolicy == v1alpha1.ConcurrentPolicyForbid && len(cron.Status.Active) > 0 {
		// Regardless which source of information we use for the set of active jobs,
		// there is some risk that we won't see an active job when there is one.
		// (because we haven't seen the status update to the Cron or the created pod).
		// So it is theoretically possible to have concurrency with Forbid.
		// As long the as the invocations are "far enough apart in time", this usually won't happen.
		//
		// TODO: for Forbid, we could use the same name for every execution, as a lock.
		log.V(1).Info("Not starting because prior execution is still running and concurrency policy is Forbid")
		r.recorder.Eventf(cron, corev1.EventTypeNormal, "AlreadyActive", "Not starting because prior execution is running and concurrency policy is Forbid")
		return nextScheduledTimeDuration(schedule, now), nil
	}
	if cron.Spec.ConcurrencyPolicy == v1alpha1.ConcurrentPolicyReplace {
		for _, active := range cron.Status.Active {
			log.V(1).Info("Deleting workload that was still running at next scheduled start time", active.Kind, klog.KRef(active.Namespace, active.Name))
			if err = r.deleteWorkload(ctx, cron, active); err != nil {
				return nil, err
			}
		}
	}

	workloadToCreate, err := r.newWorkloadFromTemplate(cron, *scheduledTime)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize workload from cron template: %v", err)
	}
	if err := r.client.Create(ctx, workloadToCreate); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// If workload is created by other actor and has already in active list, assume it has updated the cron
			// status accordingly, else not return and fallback to append active list then update status.
			log.Info("Workload already exists", workloadToCreate.GetObjectKind().GroupVersionKind().Kind, klog.KRef(workloadToCreate.GetNamespace(), workloadToCreate.GetName()))
			if inActiveList(cron.Status.Active, workloadToCreate) {
				return nil, err
			}
		}
		r.recorder.Eventf(cron, corev1.EventTypeWarning, "FailedCreate", "Error creating workload: %v", err)
		return nil, err
	}

	ref, err := reference.GetReference(r.scheme, workloadToCreate)
	if err != nil {
		return nil, err
	}
	r.recorder.Eventf(cron, corev1.EventTypeNormal, "SuccessfulCreate", "Created workload: %v", ref.Name)

	cron.Status.Active = append(cron.Status.Active, *ref)
	cron.Status.LastScheduleTime = &metav1.Time{Time: *scheduledTime}
	if err = r.client.Status().Update(ctx, cron); err != nil {
		log.Error(err, "Failed to update status")
		return nil, err
	}
	return nextScheduledTimeDuration(schedule, now), nil
}

// updateCronHistory updates the cron status history with the given workloads.
func (r *CronReconciler) updateCronHistory(ctx context.Context, cron *v1alpha1.Cron, workloads []client.Object) error {
	log := logf.FromContext(ctx)
	// history maps current cron history from a unique key to index in history slice.
	history := map[string]int{}

	// unique cron history keyed by {name}-{startTimestamp}
	key := func(h *v1alpha1.CronHistory) string { return fmt.Sprintf("%s-%d", h.Object.Name, h.Created.Unix()) }

	for i := range cron.Status.History {
		history[key(&cron.Status.History[i])] = i
	}

	for i := range workloads {
		workload := workloads[i]
		newHistory := workloadToHistory(workload, cron.Spec.Template.APIVersion, cron.Spec.Template.Kind)
		nk := key(&newHistory)
		if idx, ok := history[nk]; ok {
			// only value of status that can change.
			cron.Status.History[idx].UID = newHistory.UID
			cron.Status.History[idx].Status = newHistory.Status
			if newHistory.Finished != nil {
				cron.Status.History[idx].Finished = newHistory.Finished
			}
		} else {
			cron.Status.History = append(cron.Status.History, newHistory)
		}
	}

	// Sort history by creation time (oldest first).
	sort.Slice(cron.Status.History, func(i, j int) bool {
		history := cron.Status.History
		if history[i].Created == nil && history[j].Created != nil {
			return false
		}
		if history[i].Created != nil && history[j].Created == nil {
			return true
		}
		if history[i].Created.Equal(history[j].Created) {
			return history[i].Object.Name < history[j].Object.Name
		}
		return history[i].Created.Before(history[j].Created)
	})

	historySize := len(cron.Status.History)
	historyLimit := ptr.Deref(cron.Spec.HistoryLimit, math.MaxInt)
	if historySize > historyLimit {
		log.Info(fmt.Sprintf("Truncate history for its size has exceed history limit %d", historyLimit))
		toTruncate := cron.Status.History[:historySize-historyLimit]
		for _, truncate := range toTruncate {
			if err := r.deleteWorkload(ctx, cron, corev1.ObjectReference{
				UID:        truncate.UID,
				Kind:       truncate.Object.Kind,
				Namespace:  cron.Namespace,
				Name:       truncate.Object.Name,
				APIVersion: *truncate.Object.APIGroup,
			}); err != nil {
				return err
			}
		}

		cron.Status.History = cron.Status.History[historySize-historyLimit:]
	}

	return r.client.Status().Update(ctx, cron)
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
		r.recorder.Event(cron, corev1.EventTypeNormal, "OverridePolicy", "metadata.name has been specifeid in workload template, override cron concurrency policy as Forbidden")
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

// trimFinishedWorkloadsFromActiveList removes workloads that are finished from active list.
func (r *CronReconciler) trimFinishedWorkloadsFromActiveList(ctx context.Context, cron *v1alpha1.Cron, workloads []client.Object) error {
	log := logf.FromContext(ctx)
	knownActive := sets.New[types.UID]()

	for _, w := range workloads {
		knownActive.Insert(w.GetUID())
		found := slices.ContainsFunc(cron.Status.Active, func(r corev1.ObjectReference) bool { return r.UID == w.GetUID() })
		_, finished := IsWorkloadFinished(w)
		if !found && !finished {
			// Workload is not in active list and has not finish yet, treat it as an unexpected workload.
			// Retrieve latest cron status and double checks first.
			latestCron := &v1alpha1.Cron{}
			key := types.NamespacedName{Namespace: cron.Namespace, Name: cron.Name}
			if err := r.client.Get(ctx, key, latestCron); err != nil {
				return err
			}
			if inActiveList(latestCron.Status.Active, w) {
				cron = latestCron
				continue
			}
			r.recorder.Eventf(cron, corev1.EventTypeWarning, "ExpectedWorkload", "Saw a workload that the controller did not create or forgot: %s", w.GetName())
			// We found an unfinished workload that has us as the parent, but it is not in our Active list.
			// This could happen if we crashed right after creating the Workload and before updating the status,
			// or if our workloads list is newer than our cron status after a relist, or if someone intentionally created.
		} else if found && finished {
			deleteFromActiveList(cron, w.GetUID())
			r.recorder.Eventf(cron, corev1.EventTypeNormal, "SawCompletedWorkload", "Saw completed workload: %s", w.GetName())
		}
	}

	// Remove any reference from the active list if the corresponding workload does not exist any more.
	// Otherwise, the cron may be stuck in active mode forever even though there is no matching running.
	for _, objRef := range cron.Status.Active {
		if knownActive.Has(objRef.UID) {
			continue
		}

		wl, err := newEmptyWorkload(cron)
		if err != nil {
			log.Error(err, "failed to initialize a new workload, apiVersion: %s, kind: %s", objRef.APIVersion, objRef.Kind)
			continue
		}

		key := types.NamespacedName{Namespace: objRef.Namespace, Name: objRef.Name}
		if err := r.reader.Get(ctx, key, wl); err != nil {
			if apierrors.IsNotFound(err) {
				r.recorder.Eventf(cron, corev1.EventTypeNormal, "MissingWorkload", "Active workload went missing: %v", objRef.Name)
				deleteFromActiveList(cron, objRef.UID)
			}
			return err
		}
	}

	return nil
}

// listActiveWorkloads lists all active workloads of a given cron.
func (r *CronReconciler) listActiveWorkloads(ctx context.Context, cron *v1alpha1.Cron) ([]client.Object, error) {
	log := logf.FromContext(ctx)
	active := cron.Status.Active
	workloads := make([]client.Object, 0, len(active))

	for i, ref := range active {
		workload, err := newEmptyWorkload(cron)
		if err != nil {
			log.Error(err, "unsupported cron workload and failed to init by scheme, kind: %s", active[i].Kind, err)
			continue
		}

		key := types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}
		if err := r.client.Get(ctx, key, workload); err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("active workload %s has been deleted", key)
				continue
			}
			return nil, err
		}

		workloads = append(workloads, workload)
	}

	return workloads, nil
}

// deleteWorkload deletes a workload by its reference.
func (r *CronReconciler) deleteWorkload(ctx context.Context, cron *v1alpha1.Cron, ref corev1.ObjectReference) error {
	log := logf.FromContext(ctx)
	workload, err := newEmptyWorkload(cron)
	if err != nil {
		return err
	}

	key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
	if err = r.client.Get(ctx, key, workload); err != nil {
		if apierrors.IsNotFound(err) {
			deleteFromActiveList(cron, ref.UID)
			log.V(1).Info("workload %s has been deleted from active list", key)
			return nil
		}
		return err
	}

	deleteFromActiveList(cron, ref.UID)
	if err = r.client.Delete(ctx, workload); err != nil {
		log.Error(err, "failed to delete workload %s ", key)
	}

	r.recorder.Eventf(cron, corev1.EventTypeNormal, "SuccessfulDelete", "successfully delete workload %s", ref.Name)
	return nil
}
