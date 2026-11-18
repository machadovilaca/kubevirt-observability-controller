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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("ResolveMetricsSelector", func() {
	const (
		namespace = "test-namespace"
		podName   = "controller-abc123-xyz"
	)

	readerWithPod := func(labels map[string]string) client.Reader {
		return fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: namespace,
					Labels:    labels,
				},
			}).
			Build()
	}

	It("should keep the default selector when the pod carries the expected label", func() {
		reader := readerWithPod(map[string]string{
			labelKeyName:         controllerName,
			podTemplateHashLabel: "abc123",
			"control-plane":      "controller-manager",
		})

		selector, err := ResolveMetricsSelector(context.Background(), reader, namespace, podName)

		Expect(err).ToNot(HaveOccurred())
		Expect(selector).To(Equal(map[string]string{labelKeyName: controllerName}))
	})

	It("should derive the selector from the pod labels when the default does not match", func() {
		reader := readerWithPod(map[string]string{
			"app":                "my-own-name",
			podTemplateHashLabel: "abc123",
		})

		selector, err := ResolveMetricsSelector(context.Background(), reader, namespace, podName)

		Expect(err).To(HaveOccurred())
		Expect(selector).To(Equal(map[string]string{"app": "my-own-name"}))
	})

	It("should never put the pod template hash in the selector", func() {
		reader := readerWithPod(map[string]string{
			"app":                "my-own-name",
			podTemplateHashLabel: "abc123",
		})

		selector, _ := ResolveMetricsSelector(context.Background(), reader, namespace, podName)

		Expect(selector).ToNot(HaveKey(podTemplateHashLabel))
	})

	It("should fall back to the default selector when the pod has no labels", func() {
		reader := readerWithPod(nil)

		selector, err := ResolveMetricsSelector(context.Background(), reader, namespace, podName)

		Expect(err).To(HaveOccurred())
		Expect(selector).To(Equal(map[string]string{labelKeyName: controllerName}))
	})

	It("should fall back to the default selector when POD_NAME is unset", func() {
		reader := readerWithPod(map[string]string{"app": "my-own-name"})

		selector, err := ResolveMetricsSelector(context.Background(), reader, namespace, "")

		Expect(err).To(HaveOccurred())
		Expect(selector).To(Equal(map[string]string{labelKeyName: controllerName}))
	})

	It("should fall back to the default selector when the pod cannot be read", func() {
		reader := fake.NewClientBuilder().WithScheme(testScheme).Build()

		selector, err := ResolveMetricsSelector(context.Background(), reader, namespace, podName)

		Expect(err).To(HaveOccurred())
		Expect(selector).To(Equal(map[string]string{labelKeyName: controllerName}))
	})
})
