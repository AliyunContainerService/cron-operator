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

package v1alpha1

import (
	kubeflowv1 "github.com/kubeflow/training-operator/pkg/apis/kubeflow.org/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

func init() {
	SchemeBuilder.Register(&Cron{}, &CronList{})
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="SCHEDULE",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="SUSPEND",type=boolean,JSONPath=`.spec.suspend`
// +kubebuilder:printcolumn:name="LAST_SCHEDULE",type=string,JSONPath=`.status.lastScheduleTime`
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=`.metadata.creationTimestamp`

// Cron is the Schema for the crons API.
// It represents a scheduled job that runs workloads at specified times using cron expressions.
type Cron struct {
	metav1.TypeMeta `json:",inline"`

	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Spec defines the desired state of Cron.
	// +required
	Spec CronSpec `json:"spec"`

	// Status defines the observed state of Cron.
	// +optional
	Status CronStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CronList contains a list of Cron resources.
type CronList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#lists-and-simple-kinds
	metav1.ListMeta `json:"metadata,omitzero"`

	// Items is the list of Cron objects.
	Items []Cron `json:"items"`
}

// CronSpec defines the desired state of Cron.
type CronSpec struct {
	// Schedule specifies the cron schedule in standard cron format.
	// For example: "0 0 * * *" for daily at midnight, "*/5 * * * *" for every 5 minutes.
	// See https://en.wikipedia.org/wiki/Cron for more details.
	// +required
	Schedule string `json:"schedule"`

	// Template specifies the workload template that will be created when executing a cron job.
	// +required
	Template CronTemplateSpec `json:"template"`

	// ConcurrencyPolicy specifies how to treat concurrent executions of a job.
	// Valid values are:
	// - "Allow" (default): allows cron jobs to run concurrently.
	// - "Forbid": forbids concurrent runs, skipping next run if previous run hasn't finished yet.
	// - "Replace": cancels currently running job and replaces it with a new one.
	// +optional
	// +kubebuilder:default=Allow
	ConcurrencyPolicy ConcurrencyPolicy `json:"concurrencyPolicy,omitempty"`

	// Suspend tells the controller to suspend subsequent executions.
	// It does not apply to already started executions.
	// Defaults to false.
	// +optional
	Suspend *bool `json:"suspend,omitempty"`

	// Deadline is the optional deadline timestamp after which the cron job will stop scheduling new executions.
	// If specified, no new jobs will be created after this time.
	// +optional
	Deadline *metav1.Time `json:"deadline,omitempty"`

	// HistoryLimit specifies the number of finished job history records to retain.
	// This is a pointer to distinguish between explicit zero and not specified.
	// If not set, a default value will be used by the controller.
	// +optional
	HistoryLimit *int `json:"historyLimit,omitempty"`
}

// CronTemplateSpec describes a template for launching a specific workload.
type CronTemplateSpec struct {
	metav1.TypeMeta `json:",inline"`

	// Workload contains the specification of the desired workload to be scheduled.
	// It can be any Kubernetes workload type (e.g., Job, Deployment, Pod).
	// The workload is stored as RawExtension to support different resource types.
	// +kubebuilder:pruning:PreserveUnknownFields
	Workload *runtime.RawExtension `json:"workload,omitempty"`
}

// ConcurrencyPolicy describes how concurrent executions of a job will be handled.
// Only one of the following concurrent policies may be specified.
// If none of the following policies is specified, the default one is Allow.
// +kubebuilder:validation:Enum=Allow;Forbid;Replace
type ConcurrencyPolicy string

const (
	// ConcurrentPolicyAllow allows cron jobs to run concurrently.
	// Multiple instances of the job can run at the same time.
	ConcurrentPolicyAllow ConcurrencyPolicy = "Allow"

	// ConcurrentPolicyForbid forbids concurrent runs.
	// If the previous run hasn't finished yet, the next scheduled run will be skipped.
	ConcurrentPolicyForbid ConcurrencyPolicy = "Forbid"

	// ConcurrentPolicyReplace cancels the currently running job and replaces it with a new one.
	// This ensures only one job instance runs at a time by terminating the old one.
	ConcurrentPolicyReplace ConcurrencyPolicy = "Replace"
)

// CronStatus defines the observed state of Cron.
type CronStatus struct {
	// Active contains a list of references to currently running jobs created by this cron.
	// +optional
	// +listType=atomic
	Active []corev1.ObjectReference `json:"active,omitempty"`

	// History is a list of previously scheduled cron jobs with their execution records.
	// This provides an audit trail of job executions.
	// +optional
	// +listType=atomic
	History []CronHistory `json:"history,omitempty"`

	// LastScheduleTime records the last time a job was successfully scheduled.
	// This is used to determine the next execution time.
	// +optional
	LastScheduleTime *metav1.Time `json:"lastScheduleTime,omitempty"`
}

// CronHistory represents a historical record of a scheduled cron job execution.
type CronHistory struct {
	// UID is the unique identifier of the scheduled job.
	// +optional
	UID types.UID `json:"uid,omitempty"`

	// Object is the reference to the historical scheduled cron job.
	// It contains the kind, name, and API group of the workload.
	// +required
	Object corev1.TypedLocalObjectReference `json:"object"`

	// Status is the final status of the job when it finished execution.
	// +required
	Status kubeflowv1.JobConditionType `json:"status"`

	// Created is the timestamp when the job was created.
	// +optional
	Created *metav1.Time `json:"created,omitempty"`

	// Finished is the timestamp when the job finished execution (either succeeded or failed).
	// +optional
	Finished *metav1.Time `json:"finished,omitempty"`
}
