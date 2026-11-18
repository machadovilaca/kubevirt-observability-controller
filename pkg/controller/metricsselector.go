/*
This file is part of the KubeVirt project

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

Copyright The KubeVirt Authors.
*/

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const podTemplateHashLabel = "pod-template-hash"

func DefaultMetricsSelector() map[string]string {
	return map[string]string{labelKeyName: controllerName}
}

func ResolveMetricsSelector(ctx context.Context, reader client.Reader,
	namespace, podName string) (map[string]string, error) {
	defaultSelector := DefaultMetricsSelector()

	if podName == "" {
		return defaultSelector, fmt.Errorf(
			"POD_NAME is not set, cannot confirm the metrics Service selects the controller pods")
	}

	pod := &corev1.Pod{}
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: podName}, pod); err != nil {
		return defaultSelector, fmt.Errorf("reading own pod %s/%s: %w", namespace, podName, err)
	}

	if selectorMatches(defaultSelector, pod.Labels) {
		return defaultSelector, nil
	}

	derived := make(map[string]string, len(pod.Labels))
	for key, value := range pod.Labels {
		if key != podTemplateHashLabel {
			derived[key] = value
		}
	}

	if len(derived) == 0 {
		return defaultSelector, fmt.Errorf(
			"pod %s/%s has no labels to select on, the metrics Service will have no endpoints", namespace, podName)
	}

	return derived, fmt.Errorf(
		"pod %s/%s is not labelled %s=%s, deriving the metrics Service selector from the pod labels instead",
		namespace, podName, labelKeyName, controllerName)
}

func selectorMatches(selector, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}
