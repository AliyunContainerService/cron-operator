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
	"encoding/json"
	"fmt"
	"slices"
	"time"

	kubeflowv1 "github.com/kubeflow/training-operator/pkg/apis/kubeflow.org/v1"
	kubeflowutil "github.com/kubeflow/training-operator/pkg/util"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/cron-operator/api/v1alpha1"
)

var (
	nextScheduleDelta = 100 * time.Millisecond
)

// nextScheduledTimeDuration returns the time duration to requeue based on
// the schedule and current time. It adds a 100ms padding to the next requeue to account
// for Network Time Protocol(NTP) time skews. If the time drifts are adjusted which in most
// realistic cases would be around 100s, scheduled cron will still be executed without missing
// the schedule.
func nextScheduledTimeDuration(sched cron.Schedule, now time.Time) *time.Duration {
	t := sched.Next(now).Add(nextScheduleDelta).Sub(now)
	return &t
}

// getNextScheduleTime gets the time of next schedule after last scheduled and before now it
// returns nil if no unmet schedule times.
// If there are too many (>100) unstarted times, it will raise a warning and but still return
// the list of missed times.
func getNextScheduleTime(cron *v1alpha1.Cron, now time.Time, schedule cron.Schedule, recorder record.EventRecorder) (*time.Time, error) {
	var (
		earliestTime time.Time
	)

	if cron.Status.LastScheduleTime != nil {
		earliestTime = cron.Status.LastScheduleTime.Time
	} else {
		earliestTime = cron.CreationTimestamp.Time
	}
	if earliestTime.After(now) {
		return nil, nil
	}

	t, numberOfMissedSchedules, err := getMostRecentScheduleTime(earliestTime, now, schedule)
	if err != nil {
		return nil, err
	}

	if numberOfMissedSchedules > 100 {
		// An object might miss several starts. For example, if
		// controller gets wedged on friday at 5:01pm when everyone has
		// gone home, and someone comes in on tuesday AM and discovers
		// the problem and restarts the controller, then all the hourly
		// jobs, more than 80 of them for one hourly cronJob, should
		// all start running with no further intervention (if the cronJob
		// allows concurrency and late starts).
		//
		// However, if there is a bug somewhere, or incorrect clock
		// on controller's server or apiservers (for setting creationTimestamp)
		// then there could be so many missed start times (it could be off
		// by decades or more), that it would eat up all the CPU and memory
		// of this controller. In that case, we want to not try to list
		// all the missed start times.
		recorder.Eventf(cron, corev1.EventTypeWarning, "TooManyMissedTimes", "too many missed start times: %d. Check clock skew", numberOfMissedSchedules)
		// klog.Infof("too many missed times, cron: %s/%s, missed times: %v", cron.Namespace, cron.Name, numberOfMissedSchedules)
	}
	return t, nil
}

// getMostRecentScheduleTime returns the latest schedule time between earliestTime and the count of number of
// schedules in between them
func getMostRecentScheduleTime(earliestTime time.Time, now time.Time, schedule cron.Schedule) (*time.Time, int64, error) {
	t1 := schedule.Next(earliestTime)
	if t1.IsZero() {
		return nil, 0, fmt.Errorf("invalid cron expression")
	}

	t2 := schedule.Next(t1)

	if now.Before(t1) {
		return nil, 0, nil
	}
	if now.Before(t2) {
		return &t1, 1, nil
	}

	// At this point, now > t2 > t1, which means cron has missed more than one time.
	for !t2.After(now) {
		t2 = schedule.Next(t2)
	}

	// It is possible for cron.ParseStandard("59 23 31 2 *") to return an invalid schedule
	// seconds - 59, minute - 23, hour - 31 (?!)  dom - 2, and dow is optional, clearly 31 is invalid
	// In this case the timeBetweenTwoSchedules will be 0, and we error out the invalid schedule.
	timeBetweenTwoSchedules := int64(t2.Sub(t1).Round(time.Second).Seconds())
	if timeBetweenTwoSchedules < 1 {
		return nil, 0, fmt.Errorf("time difference between two schedules less than 1 second")
	}
	timeElapsed := int64(now.Sub(t1).Seconds())
	numberOfMissedSchedules := (timeElapsed / timeBetweenTwoSchedules) + 1
	t := time.Unix(t1.Unix()+((numberOfMissedSchedules-1)*timeBetweenTwoSchedules), 0).UTC()
	return &t, numberOfMissedSchedules, nil
}

// newEmptyWorkload creates a new empty workload object from cron spec.
func newEmptyWorkload(cron *v1alpha1.Cron) (client.Object, error) {
	if cron.Spec.Template.Workload == nil {
		return nil, fmt.Errorf("workload template is nil")
	}

	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(cron.Spec.Template.Workload.Raw, obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workload template: %v", err)
	}

	gvk := obj.GroupVersionKind()
	if gvk.Group == "" || gvk.Version == "" || gvk.Kind == "" {
		return nil, fmt.Errorf("workload template is missing APIVersion or Kind")
	}

	return obj, nil
}

// getDefaultJobName returns the default job name for a given cron and scheduled time.
func getDefaultJobName(c *v1alpha1.Cron, t time.Time) string {
	return fmt.Sprintf("%s-%d", c.Name, t.Unix())
}

// workloadToHistory converts a workload object to a CronHistory.
func workloadToHistory(obj metav1.Object, apiGroup, kind string) v1alpha1.CronHistory {
	status, finished := IsWorkloadFinished(obj)
	created := obj.GetCreationTimestamp()
	ch := v1alpha1.CronHistory{
		Created: &created,
		Status:  status,
		UID:     obj.GetUID(),
		Object: corev1.TypedLocalObjectReference{
			APIGroup: &apiGroup,
			Kind:     kind,
			Name:     obj.GetName(),
		},
	}
	if finished {
		// Finished is the finished status observed timestamp of controller view.
		now := metav1.Now()
		ch.Finished = &now
	}
	return ch
}

// inActiveList checks if a workload is in the active list.
func inActiveList(active []corev1.ObjectReference, workload client.Object) bool {
	return slices.ContainsFunc(active, func(r corev1.ObjectReference) bool { return r.UID == workload.GetUID() })
}

// deleteFromActiveList deletes a workload from active list by its uid.
func deleteFromActiveList(cron *v1alpha1.Cron, uid types.UID) {
	cron.Status.Active = slices.DeleteFunc(cron.Status.Active, func(r corev1.ObjectReference) bool { return r.UID == uid })
}

// IsWorkloadFinished checks if the workload is finished.
func IsWorkloadFinished(workload metav1.Object) (kubeflowv1.JobConditionType, bool) {
	u, ok := workload.(*unstructured.Unstructured)
	if !ok {
		return "", false
	}

	status, err := getJobStatus(u)
	if err != nil {
		klog.Errorf("failed to trait status from workload, err: %v", err)
		return "", false
	}

	finished := kubeflowutil.IsFailed(status) || kubeflowutil.IsSucceeded(status)
	if len(status.Conditions) > 0 {
		return status.Conditions[len(status.Conditions)-1].Type, finished
	}
	return "", finished
}

// getJobStatus checks if a job has finished (succeeded or failed).
func getJobStatus(job *unstructured.Unstructured) (kubeflowv1.JobStatus, error) {
	status := kubeflowv1.JobStatus{}

	statusField, ok := job.Object["status"]
	if !ok {
		return status, nil
	}
	u, ok := statusField.(map[string]interface{})
	if !ok {
		return status, nil
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u, &status); err != nil {
		return status, fmt.Errorf("failed to convert status from unstructured: %v", err)
	}

	return status, nil
}
