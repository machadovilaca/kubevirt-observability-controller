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

package vmstats

import (
	"context"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	k6tv1 "kubevirt.io/api/core/v1"
)

type fakeEnablerClient struct {
	mu       sync.Mutex
	calls    []enableCall
	failKeys map[string]bool
}

type enableCall struct {
	podIP     string
	namespace string
	name      string
}

func (f *fakeEnablerClient) EnableVMStats(_ context.Context, podIP, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := namespace + "/" + name
	if f.failKeys[key] {
		return fmt.Errorf("simulated failure for %s", key)
	}
	f.calls = append(f.calls, enableCall{podIP: podIP, namespace: namespace, name: name})
	return nil
}

func (f *fakeEnablerClient) getCalls() []enableCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]enableCall, len(f.calls))
	copy(cp, f.calls)
	return cp
}

var _ = Describe("VMStatsEnabler", func() {
	It("should call EnableVMStats when a VMI with NodeName and GuestOSInfo is enqueued", func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status: k6tv1.VirtualMachineInstanceStatus{
				NodeName:    "node1",
				GuestOSInfo: k6tv1.VirtualMachineInstanceGuestOSInfo{ID: "fedora"},
			},
		}
		fake := &fakeEnablerClient{}
		enabler := newTestEnabler(fake, vmi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = enabler.Start(ctx) }()

		enabler.handleVMIEvent(vmi)

		Eventually(func() int { return len(fake.getCalls()) }, 5*time.Second, 100*time.Millisecond).Should(Equal(1))
		calls := fake.getCalls()
		Expect(calls[0].namespace).To(Equal("ns1"))
		Expect(calls[0].name).To(Equal("vm1"))
		Expect(calls[0].podIP).To(Equal("10.0.0.5"))
	})

	It("should skip VMIs without NodeName", func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status: k6tv1.VirtualMachineInstanceStatus{
				GuestOSInfo: k6tv1.VirtualMachineInstanceGuestOSInfo{ID: "fedora"},
			},
		}
		fake := &fakeEnablerClient{}
		enabler := newTestEnabler(fake, vmi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = enabler.Start(ctx) }()

		enabler.handleVMIEvent(vmi)

		Consistently(func() int { return len(fake.getCalls()) }, 500*time.Millisecond, 50*time.Millisecond).Should(Equal(0))
	})

	It("should skip VMIs without GuestOSInfo", func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status:     k6tv1.VirtualMachineInstanceStatus{NodeName: "node1"},
		}
		fake := &fakeEnablerClient{}
		enabler := newTestEnabler(fake, vmi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = enabler.Start(ctx) }()

		enabler.handleVMIEvent(vmi)

		Consistently(func() int { return len(fake.getCalls()) }, 500*time.Millisecond, 50*time.Millisecond).Should(Equal(0))
	})

	It("should not re-enable an already enabled VMI", func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status: k6tv1.VirtualMachineInstanceStatus{
				NodeName:    "node1",
				GuestOSInfo: k6tv1.VirtualMachineInstanceGuestOSInfo{ID: "fedora"},
			},
		}
		fake := &fakeEnablerClient{}
		enabler := newTestEnabler(fake, vmi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = enabler.Start(ctx) }()

		enabler.handleVMIEvent(vmi)
		Eventually(func() int { return len(fake.getCalls()) }, 5*time.Second, 100*time.Millisecond).Should(Equal(1))

		enabler.handleVMIEvent(vmi)
		Consistently(func() int { return len(fake.getCalls()) }, 500*time.Millisecond, 50*time.Millisecond).Should(Equal(1))
	})

	It("should re-enable when VMI migrates to a different node", func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status: k6tv1.VirtualMachineInstanceStatus{
				NodeName:    "node1",
				GuestOSInfo: k6tv1.VirtualMachineInstanceGuestOSInfo{ID: "fedora"},
			},
		}
		fake := &fakeEnablerClient{}
		enabler := newTestEnabler(fake, vmi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = enabler.Start(ctx) }()

		enabler.handleVMIEvent(vmi)
		Eventually(func() int { return len(fake.getCalls()) }, 5*time.Second, 100*time.Millisecond).Should(Equal(1))

		// Simulate migration by updating the VMI in the store
		vmi.Status.NodeName = "node2"
		_ = enabler.vmiInformer.GetStore().Update(vmi)
		enabler.handleVMIEvent(vmi)
		Eventually(func() int { return len(fake.getCalls()) }, 5*time.Second, 100*time.Millisecond).Should(Equal(2))

		calls := fake.getCalls()
		Expect(calls[1].podIP).To(Equal("10.0.0.6"))
	})

	It("should clean up on VMI delete", func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status: k6tv1.VirtualMachineInstanceStatus{
				NodeName:    "node1",
				GuestOSInfo: k6tv1.VirtualMachineInstanceGuestOSInfo{ID: "fedora"},
			},
		}
		fake := &fakeEnablerClient{}
		enabler := newTestEnabler(fake, vmi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = enabler.Start(ctx) }()
		enabler.handleVMIEvent(vmi)
		Eventually(func() int { return len(fake.getCalls()) }, 5*time.Second, 100*time.Millisecond).Should(Equal(1))

		enabler.handleVMIDelete(vmi)

		enabler.mu.Lock()
		_, inEnabled := enabler.enabled["ns1/vm1"]
		enabler.mu.Unlock()

		Expect(inEnabled).To(BeFalse())
	})

	It("should not retry on failure (best-effort)", func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status: k6tv1.VirtualMachineInstanceStatus{
				NodeName:    "node1",
				GuestOSInfo: k6tv1.VirtualMachineInstanceGuestOSInfo{ID: "fedora"},
			},
		}
		fake := &fakeEnablerClient{failKeys: map[string]bool{"ns1/vm1": true}}
		enabler := newTestEnabler(fake, vmi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = enabler.Start(ctx) }()

		enabler.handleVMIEvent(vmi)

		// Allow time for processing
		time.Sleep(200 * time.Millisecond)

		// Should not be marked as enabled
		enabler.mu.Lock()
		_, inEnabled := enabler.enabled["ns1/vm1"]
		enabler.mu.Unlock()
		Expect(inEnabled).To(BeFalse())

		// Re-sending the same event should try again but still fail
		enabler.handleVMIEvent(vmi)

		Consistently(func() int { return len(fake.getCalls()) }, 500*time.Millisecond, 50*time.Millisecond).Should(Equal(0))
	})

	It("should exit cleanly on context cancellation", func() {
		fake := &fakeEnablerClient{}
		enabler := newTestEnabler(fake)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- enabler.Start(ctx) }()

		cancel()
		Eventually(done, 2*time.Second).Should(Receive(BeNil()))
	})
})

func newTestEnabler(client VMStatsEnablerClient, vmis ...*k6tv1.VirtualMachineInstance) *VMStatsEnabler {
	podStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	_ = podStore.Add(&k8sv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "virt-handler-node1", Namespace: "kubevirt",
			Labels: map[string]string{"kubevirt.io": "virt-handler"},
		},
		Spec:   k8sv1.PodSpec{NodeName: "node1"},
		Status: k8sv1.PodStatus{PodIP: "10.0.0.5", Phase: k8sv1.PodRunning},
	})
	_ = podStore.Add(&k8sv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "virt-handler-node2", Namespace: "kubevirt",
			Labels: map[string]string{"kubevirt.io": "virt-handler"},
		},
		Spec:   k8sv1.PodSpec{NodeName: "node2"},
		Status: k8sv1.PodStatus{PodIP: "10.0.0.6", Phase: k8sv1.PodRunning},
	})

	informer := &fakeInformer{store: cache.NewStore(cache.MetaNamespaceKeyFunc)}
	for _, vmi := range vmis {
		_ = informer.store.Add(vmi)
	}

	return NewVMStatsEnabler(client, podStore, informer)
}

type fakeInformer struct {
	cache.SharedInformer
	store cache.Store
}

func (f *fakeInformer) GetStore() cache.Store {
	return f.store
}

func (f *fakeInformer) AddEventHandler(
	handler cache.ResourceEventHandler,
) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
