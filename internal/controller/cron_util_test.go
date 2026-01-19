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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kubeflowv1 "github.com/kubeflow/training-operator/pkg/apis/kubeflow.org/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/cron-operator/api/v1alpha1"
)

var _ = Describe("CronUtil", func() {
	const (
		name      = "cron-test"
		namespace = "default"
	)

	Context("newEmptyWorkload", func() {
		It("should create an empty workload from a valid template", func() {
			cron := &v1alpha1.Cron{
				Spec: v1alpha1.CronSpec{
					Template: v1alpha1.CronTemplateSpec{
						Workload: &runtime.RawExtension{
							Raw: []byte(`{"apiVersion":"kubeflow.org/v1","kind":"PyTorchJob"}`),
						},
					},
				},
			}
			obj, err := newEmptyWorkload(cron)
			Expect(err).NotTo(HaveOccurred())
			Expect(obj).NotTo(BeNil())
			Expect(obj.GetObjectKind().GroupVersionKind()).To(Equal(schema.GroupVersionKind{Group: "kubeflow.org", Version: "v1", Kind: "PyTorchJob"}))
		})

		It("should return error if workload template is missing", func() {
			cron := &v1alpha1.Cron{
				Spec: v1alpha1.CronSpec{},
			}
			obj, err := newEmptyWorkload(cron)
			Expect(err).To(HaveOccurred())
			Expect(obj).To(BeNil())
			Expect(err.Error()).To(Equal("workload template is missing in Cron spec"))
		})

		It("should return error if workload template is invalid JSON", func() {
			cron := &v1alpha1.Cron{
				Spec: v1alpha1.CronSpec{
					Template: v1alpha1.CronTemplateSpec{
						Workload: &runtime.RawExtension{
							Raw: []byte(`{invalid json}`),
						},
					},
				},
			}
			obj, err := newEmptyWorkload(cron)
			Expect(err).To(HaveOccurred())
			Expect(obj).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("failed to unmarshal workload template"))
		})

		It("should return error if workload template is missing apiVersion", func() {
			cron := &v1alpha1.Cron{
				Spec: v1alpha1.CronSpec{
					Template: v1alpha1.CronTemplateSpec{
						Workload: &runtime.RawExtension{
							Raw: []byte(`{"kind":"PyTorchJob"}`),
						},
					},
				},
			}
			obj, err := newEmptyWorkload(cron)
			Expect(err).To(HaveOccurred())
			Expect(obj).To(BeNil())
			Expect(err.Error()).To(Equal("workload template is missing apiVersion or kind"))
		})

		It("should return error if workload template is missing kind", func() {
			cron := &v1alpha1.Cron{
				Spec: v1alpha1.CronSpec{
					Template: v1alpha1.CronTemplateSpec{
						Workload: &runtime.RawExtension{
							Raw: []byte(`{"apiVersion":"kubeflow.org/v1"}`),
						},
					},
				},
			}
			obj, err := newEmptyWorkload(cron)
			Expect(err).To(HaveOccurred())
			Expect(obj).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("failed to unmarshal workload template"))
		})
	})

	Context("getWorkloadGVK", func() {
		It("should return correct GVK from a valid template", func() {
			cron := &v1alpha1.Cron{
				Spec: v1alpha1.CronSpec{
					Template: v1alpha1.CronTemplateSpec{
						Workload: &runtime.RawExtension{
							Raw: []byte(`{"apiVersion":"kubeflow.org/v1","kind":"TFJob"}`),
						},
					},
				},
			}
			gvk, err := getWorkloadGVK(cron)
			Expect(err).NotTo(HaveOccurred())
			Expect(gvk).To(Equal(schema.GroupVersionKind{Group: "kubeflow.org", Version: "v1", Kind: "TFJob"}))
		})
	})

	Context("getDefaultJobName", func() {
		It("should generate a deterministic name", func() {
			cron := &v1alpha1.Cron{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
			}
			scheduleTime := time.Unix(1234567890, 0)
			jobName := getDefaultJobName(cron, scheduleTime)
			Expect(jobName).To(Equal(fmt.Sprintf("%s-%d", name, scheduleTime.Unix())))
		})
	})

	Context("getJobStatus", func() {
		It("should extract status from unstructured object", func() {
			status := kubeflowv1.JobStatus{
				Conditions: []kubeflowv1.JobCondition{
					{
						Type:   kubeflowv1.JobSucceeded,
						Status: corev1.ConditionTrue,
					},
				},
			}
			statusRaw, _ := json.Marshal(status)
			var statusMap map[string]interface{}
			_ = json.Unmarshal(statusRaw, &statusMap)

			u := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": statusMap,
				},
			}

			s, err := getJobStatus(u)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Conditions).To(HaveLen(1))
			Expect(s.Conditions[0].Type).To(Equal(kubeflowv1.JobSucceeded))
		})
	})

	Context("isWorkloadFinished", func() {
		It("should return true for succeeded job", func() {
			u := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   string(kubeflowv1.JobSucceeded),
								"status": string(corev1.ConditionTrue),
							},
						},
					},
				},
			}
			cond, finished := isWorkloadFinished(u)
			Expect(finished).To(BeTrue())
			Expect(cond).To(Equal(kubeflowv1.JobSucceeded))
		})

		It("should return true for failed job", func() {
			u := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   string(kubeflowv1.JobFailed),
								"status": string(corev1.ConditionTrue),
							},
						},
					},
				},
			}
			cond, finished := isWorkloadFinished(u)
			Expect(finished).To(BeTrue())
			Expect(cond).To(Equal(kubeflowv1.JobFailed))
		})

		It("should return false for running job", func() {
			u := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   string(kubeflowv1.JobRunning),
								"status": string(corev1.ConditionTrue),
							},
						},
					},
				},
			}
			_, finished := isWorkloadFinished(u)
			Expect(finished).To(BeFalse())
		})
	})

	Context("sortByCreationTimestamp", func() {
		It("should sort workloads by creation time", func() {
			now := time.Now()
			w1 := &unstructured.Unstructured{}
			w1.SetCreationTimestamp(metav1.NewTime(now.Add(-10 * time.Minute)))
			w2 := &unstructured.Unstructured{}
			w2.SetCreationTimestamp(metav1.NewTime(now.Add(-20 * time.Minute)))
			w3 := &unstructured.Unstructured{}
			w3.SetCreationTimestamp(metav1.NewTime(now.Add(-5 * time.Minute)))

			workloads := []client.Object{w1, w2, w3}
			sortByCreationTimestamp(workloads)

			Expect(workloads[0]).To(Equal(w2))
			Expect(workloads[1]).To(Equal(w1))
			Expect(workloads[2]).To(Equal(w3))
		})
	})
})
