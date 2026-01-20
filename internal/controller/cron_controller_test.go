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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kubeflowv1 "github.com/kubeflow/training-operator/pkg/apis/kubeflow.org/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/cron-operator/api/v1alpha1"
	"github.com/AliyunContainerService/cron-operator/pkg/common"
)

var _ = Describe("Cron Controller", func() {
	const (
		name      = "cron-test"
		namespace = "default"
	)

	ctx := context.Background()
	key := types.NamespacedName{Namespace: namespace, Name: name}
	req := reconcile.Request{NamespacedName: key}

	Context("When reconciling a resource", func() {
		BeforeEach(func() {
			cron := &v1alpha1.Cron{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: v1alpha1.CronSpec{
					Schedule:          "*/1 * * * *",
					ConcurrencyPolicy: v1alpha1.ConcurrentPolicyForbid,
					Template: v1alpha1.CronTemplateSpec{
						Workload: &runtime.RawExtension{
							Raw: []byte(`{"apiVersion":"kubeflow.org/v1","kind":"PyTorchJob","metadata":{"labels":{"test-label":"true"}}}`),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cron)).To(Succeed())
		})

		AfterEach(func() {
			cron := &v1alpha1.Cron{}
			if err := k8sClient.Get(ctx, key, cron); err == nil {
				Expect(k8sClient.Delete(ctx, cron)).To(Succeed())
			}

			// Clean up any created workloads
			uList := &unstructured.UnstructuredList{}
			uList.SetGroupVersionKind(kubeflowv1.SchemeGroupVersion.WithKind("PyTorchJob"))
			Expect(k8sClient.List(ctx, uList, client.InNamespace(namespace))).To(Succeed())
			for _, item := range uList.Items {
				Expect(k8sClient.Delete(ctx, &item)).To(Succeed())
			}
		})

		It("should successfully reconcile the resource", func() {
			r := NewCronReconciler(scheme, k8sClient, k8sClient, nil)
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should create a workload when schedule matches", func() {
			r := NewCronReconciler(scheme, k8sClient, k8sClient, nil)

			// Mock LastScheduleTime to be 2 minutes ago so it triggers now
			cron := &v1alpha1.Cron{}
			Expect(k8sClient.Get(ctx, key, cron)).To(Succeed())
			cron.Status.LastScheduleTime = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}
			Expect(k8sClient.Status().Update(ctx, cron)).To(Succeed())

			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check if PyTorchJob was created
			uList := &unstructured.UnstructuredList{}
			uList.SetGroupVersionKind(kubeflowv1.SchemeGroupVersion.WithKind("PyTorchJob"))
			Eventually(func() int {
				_ = k8sClient.List(ctx, uList, client.InNamespace(namespace), client.MatchingLabels{common.LabelCronName: name})
				return len(uList.Items)
			}, time.Second*5, time.Millisecond*500).Should(BeNumerically(">=", 1))
		})

		It("should not create a workload if suspended", func() {
			r := NewCronReconciler(scheme, k8sClient, k8sClient, nil)

			cron := &v1alpha1.Cron{}
			Expect(k8sClient.Get(ctx, key, cron)).To(Succeed())
			cron.Spec.Suspend = ptr.To(true)
			Expect(k8sClient.Update(ctx, cron)).To(Succeed())

			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check that no PyTorchJob was created
			uList := &unstructured.UnstructuredList{}
			uList.SetGroupVersionKind(kubeflowv1.SchemeGroupVersion.WithKind("PyTorchJob"))
			Consistently(func() int {
				_ = k8sClient.List(ctx, uList, client.InNamespace(namespace))
				return len(uList.Items)
			}, time.Second*2, time.Millisecond*500).Should(Equal(0))
		})
	})

	Context("Helper methods", func() {
		var r *CronReconciler

		BeforeEach(func() {
			r = NewCronReconciler(scheme, k8sClient, k8sClient, nil)
		})

		It("newWorkloadFromTemplate should populate metadata", func() {
			cron := &v1alpha1.Cron{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: v1alpha1.CronSpec{
					Template: v1alpha1.CronTemplateSpec{
						Workload: &runtime.RawExtension{
							Raw: []byte(`{"apiVersion":"kubeflow.org/v1","kind":"PyTorchJob"}`),
						},
					},
				},
			}
			t := time.Now()
			w, err := r.newWorkloadFromTemplate(cron, t)
			Expect(err).NotTo(HaveOccurred())
			Expect(w.GetName()).To(Equal(getDefaultJobName(cron, t)))
			Expect(w.GetNamespace()).To(Equal(namespace))
			Expect(w.GetLabels()).To(HaveKeyWithValue(common.LabelCronName, name))
		})
	})

	Context("getNextSchedule", func() {
		var r *CronReconciler
		now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

		BeforeEach(func() {
			r = NewCronReconciler(scheme, k8sClient, k8sClient, nil)
		})

		It("should return error when cron is unparsable", func() {
			cron := &v1alpha1.Cron{
				ObjectMeta: metav1.ObjectMeta{
					Name:              name,
					Namespace:         namespace,
					CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute)),
				},
				Spec: v1alpha1.CronSpec{
					Schedule: "60 31 30 2 *",
				},
			}

			lastMissed, next, err := r.getNextSchedule(ctx, cron, now)
			Expect(err.Error()).To(ContainSubstring("unparsable cron"))
			Expect(lastMissed.IsZero()).To(BeTrue())
			Expect(next.IsZero()).To(BeTrue())
		})

		It("should return error when cron is unschedulable", func() {
			cron := &v1alpha1.Cron{
				ObjectMeta: metav1.ObjectMeta{
					Name:              name,
					Namespace:         namespace,
					CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute)),
				},
				Spec: v1alpha1.CronSpec{
					Schedule: "0 0 30 2 *",
				},
			}

			lastMissed, next, err := r.getNextSchedule(ctx, cron, now)
			Expect(err.Error()).To(ContainSubstring("unschedulable cron"))
			Expect(lastMissed.IsZero()).To(BeTrue())
			Expect(next.IsZero()).To(BeTrue())
		})

		It("should return lastMissed and next schedule", func() {
			cron := &v1alpha1.Cron{
				ObjectMeta: metav1.ObjectMeta{
					Name:              name,
					Namespace:         namespace,
					CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute)),
				},
				Spec: v1alpha1.CronSpec{
					Schedule: "*/1 * * * *",
				},
			}

			lastMissed, next, err := r.getNextSchedule(ctx, cron, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(lastMissed).To(Equal(now))
			Expect(next.Unix()).To(Equal(now.Add(1 * time.Minute).Unix()))
		})
	})
})
