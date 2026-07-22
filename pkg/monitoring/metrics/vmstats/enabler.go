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
	"sync"

	"k8s.io/client-go/tools/cache"
	k6tv1 "kubevirt.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var enablerLog = ctrl.Log.WithName("vmstats-enabler")

const workChSize = 100

type VMStatsEnablerClient interface {
	EnableVMStats(ctx context.Context, podIP, namespace, name string) error
}

type VMStatsEnabler struct {
	client      VMStatsEnablerClient
	podStore    cache.Store
	vmiInformer cache.SharedInformer

	mu      sync.Mutex
	enabled map[string]string
	workCh  chan string
}

func NewVMStatsEnabler(
	client VMStatsEnablerClient,
	podStore cache.Store,
	vmiInformer cache.SharedInformer,
) *VMStatsEnabler {
	return &VMStatsEnabler{
		client:      client,
		podStore:    podStore,
		vmiInformer: vmiInformer,
		enabled:     make(map[string]string),
		workCh:      make(chan string, workChSize),
	}
}

func (e *VMStatsEnabler) Start(ctx context.Context) error {
	if e.vmiInformer != nil {
		_, err := e.vmiInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				if vmi, ok := obj.(*k6tv1.VirtualMachineInstance); ok {
					e.handleVMIEvent(vmi)
				}
			},
			UpdateFunc: func(_, newObj any) {
				if vmi, ok := newObj.(*k6tv1.VirtualMachineInstance); ok {
					e.handleVMIEvent(vmi)
				}
			},
			DeleteFunc: func(obj any) {
				if vmi, ok := obj.(*k6tv1.VirtualMachineInstance); ok {
					e.handleVMIDelete(vmi)
				}
			},
		})
		if err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case key := <-e.workCh:
			e.processWorkItem(ctx, key)
		}
	}
}

func (e *VMStatsEnabler) handleVMIEvent(vmi *k6tv1.VirtualMachineInstance) {
	if vmi.Status.NodeName == "" || vmi.Status.GuestOSInfo.ID == "" {
		return
	}

	key := vmi.Namespace + "/" + vmi.Name
	nodeName := vmi.Status.NodeName

	e.mu.Lock()
	if e.enabled[key] == nodeName {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	select {
	case e.workCh <- key:
	default:
		enablerLog.V(4).Info("work channel full, dropping enable request", "vmi", key)
	}
}

func (e *VMStatsEnabler) handleVMIDelete(vmi *k6tv1.VirtualMachineInstance) {
	key := vmi.Namespace + "/" + vmi.Name
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.enabled, key)
}

func (e *VMStatsEnabler) processWorkItem(ctx context.Context, key string) {
	var vmi *k6tv1.VirtualMachineInstance
	if e.vmiInformer != nil {
		item, exists, err := e.vmiInformer.GetStore().GetByKey(key)
		if err != nil || !exists {
			return
		}
		var ok bool
		vmi, ok = item.(*k6tv1.VirtualMachineInstance)
		if !ok || vmi.Status.NodeName == "" {
			return
		}
	}
	if vmi == nil {
		return
	}

	nodeName := vmi.Status.NodeName

	e.mu.Lock()
	if e.enabled[key] == nodeName {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	podIP, err := findVirtHandlerPodIP(e.podStore, nodeName)
	if err != nil {
		enablerLog.V(4).Info("cannot find virt-handler pod", "vmi", key, "node", nodeName, "error", err)
		return
	}

	namespace, name := splitKey(key)
	if err := e.client.EnableVMStats(ctx, podIP, namespace, name); err != nil {
		enablerLog.V(4).Info("EnableVMStats failed (best-effort)", "vmi", key, "error", err)
		return
	}

	enablerLog.V(2).Info("EnableVMStats succeeded", "vmi", key, "node", nodeName)
	e.mu.Lock()
	e.enabled[key] = nodeName
	e.mu.Unlock()
}

func splitKey(key string) (namespace, name string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}
