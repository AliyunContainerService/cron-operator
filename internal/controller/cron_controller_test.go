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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/cron-operator/api/v1alpha1"
)

var _ = Describe("Cron Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			name      = "cron-test"
			namespace = "default"
		)

		ctx := context.Background()
		key := types.NamespacedName{Namespace: namespace, Name: name}
		cron := &v1alpha1.Cron{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Cron")
			if err := k8sClient.Get(ctx, key, cron); err != nil && apierrors.IsNotFound(err) {
				cron := &v1alpha1.Cron{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: v1alpha1.CronSpec{
						Schedule:          "*/5 * * * *",
						ConcurrencyPolicy: v1alpha1.ConcurrentPolicyForbid,
						Template: v1alpha1.CronTemplateSpec{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "kubeflow.org/v1",
								Kind:       "PyTorchJob",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, cron)).To(Succeed())
			}
		})

		AfterEach(func() {
			cron := &v1alpha1.Cron{}
			Expect(k8sClient.Get(ctx, key, cron)).To(Succeed())

			By("Cleanup the specific resource instance Cron")
			Expect(k8sClient.Delete(ctx, cron)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			r := NewCronReconciler(k8sClient.Scheme(), k8sClient, k8sClient, nil)

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: key,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
