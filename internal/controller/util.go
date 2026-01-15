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
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// LogConstructor returns a log constructor for the given kind.
func LogConstructor(logger logr.Logger, kind string) func(*reconcile.Request) logr.Logger {
	// Use the lowercase of kind as controller name, as it will show up in the metrics,
	// and thus should be a prometheus compatible name(underscores and alphanumeric characters only).
	name := strings.ToLower(kind)
	baseLogger := logger.WithValues("controller", name)

	// Use a custom log constructor.
	return func(req *reconcile.Request) logr.Logger {
		if req != nil {
			return baseLogger.WithValues(kind, klog.KRef(req.Namespace, req.Name))
		}
		return baseLogger
	}
}
