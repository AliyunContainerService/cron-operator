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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Util", func() {
	Context("LogConstructor", func() {
		var baseLogger logr.Logger

		BeforeEach(func() {
			baseLogger = zap.New(zap.UseDevMode(true))
		})

		It("should return a constructor that works with request", func() {
			kind := "Cron"
			constructor := logConstructor(baseLogger, kind)
			Expect(constructor).NotTo(BeNil())

			req := &reconcile.Request{
				NamespacedName: struct {
					Namespace string
					Name      string
				}{
					Namespace: "default",
					Name:      "test-cron",
				},
			}
			log := constructor(req)
			Expect(log).NotTo(BeNil())
		})

		It("should return a constructor that works without request", func() {
			kind := "Cron"
			constructor := logConstructor(baseLogger, kind)
			Expect(constructor).NotTo(BeNil())

			log := constructor(nil)
			Expect(log).NotTo(BeNil())
		})
	})
})
