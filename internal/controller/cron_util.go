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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/cron-operator/api/v1alpha1"
)

// newEmptyWorkload creates an empty Unstructured object based on the workload template
// defined in the Cron specification. This allows the controller to handle any job type
// generically without compile-time dependencies on all possible job CRDs.
func newEmptyWorkload(cron *v1alpha1.Cron) (client.Object, error) {
	if cron.Spec.Template.Workload == nil {
		return nil, fmt.Errorf("workload template is missing in Cron spec")
	}

	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(cron.Spec.Template.Workload.Raw, obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workload template: %v", err)
	}

	gvk := obj.GroupVersionKind()
	if gvk.Group == "" || gvk.Version == "" || gvk.Kind == "" {
		return nil, fmt.Errorf("workload template is missing apiVersion or kind")
	}

	return obj, nil
}

// getWorkloadGVK returns the GroupVersionKind of the workload defined in the Cron workload template.
func getWorkloadGVK(cron *v1alpha1.Cron) (schema.GroupVersionKind, error) {
	workload, err := newEmptyWorkload(cron)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	return workload.GetObjectKind().GroupVersionKind(), nil
}

// getDefaultJobName generates a unique name for a scheduled job by appending
// the Unix timestamp of the schedule to the Cron object's name.
func getDefaultJobName(cron *v1alpha1.Cron, scheduleTime time.Time) string {
	return fmt.Sprintf("%s-%d", cron.Name, scheduleTime.Unix())
}

// isWorkloadFinished determines if a job has reached a terminal state (Succeeded or Failed)
// by examining its status conditions.
func isWorkloadFinished(workload metav1.Object) (kubeflowv1.JobConditionType, bool) {
	status, err := getJobStatus(workload)
	if err != nil {
		klog.Errorf("failed to extract job status from unstructured object: %v", err)
		return "", false
	}

	finished := kubeflowutil.IsFailed(status) || kubeflowutil.IsSucceeded(status)
	if len(status.Conditions) > 0 {
		// We return the latest condition type as the job's final status.
		return status.Conditions[len(status.Conditions)-1].Type, finished
	}
	return "", finished
}

// getJobStatus converts the generic 'status' field from an Unstructured object
// into a typed kubeflowv1.JobStatus struct for easier manipulation.
func getJobStatus(workload metav1.Object) (kubeflowv1.JobStatus, error) {
	status := kubeflowv1.JobStatus{}
	u, ok := workload.(*unstructured.Unstructured)
	if !ok {
		return status, fmt.Errorf("failed to convert workload to unstructured object")
	}

	statusField, ok := u.Object["status"]
	if !ok {
		return status, nil
	}

	us, ok := statusField.(map[string]interface{})
	if !ok {
		return status, nil
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(us, &status); err != nil {
		return status, fmt.Errorf("failed to convert status from unstructured: %v", err)
	}

	return status, nil
}

// sortByCreationTimestamp sorts a list of workloads by their creation timestamp.
func sortByCreationTimestamp(workloads []client.Object) {
	slices.SortStableFunc(workloads, func(a, b client.Object) int {
		aCreateTime := a.GetCreationTimestamp().Time
		bCreateTime := b.GetCreationTimestamp().Time
		if aCreateTime.Before(bCreateTime) {
			return -1
		}
		if aCreateTime.After(bCreateTime) {
			return 1
		}
		return 0
	})
}
