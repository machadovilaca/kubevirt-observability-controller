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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k6tv1 "kubevirt.io/api/core/v1"
)

var _ = Describe("Guest Metrics", func() {
	var report *VMIReport

	BeforeEach(func() {
		vmi := &k6tv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm1", Namespace: "ns1"},
			Status:     k6tv1.VirtualMachineInstanceStatus{NodeName: "node1"},
		}
		report = NewVMIReport(vmi, &VMStats{})
	})

	It("should return empty when no guest agent data", func() {
		Expect(collectGuestMetrics(report)).To(BeEmpty())
	})

	It("should parse GuestGetOsInfo", func() {
		report.Stats.GuestGetOsInfo = `{"id":"fedora","name":"Fedora Linux",` +
			`"version":"38","kernel-release":"6.2.0","machine":"x86_64"}`

		results := collectGuestMetrics(report)

		var found bool
		for _, r := range results {
			if r.Metric.GetOpts().Name == "kubevirt_vmi_guest_os_info" {
				found = true
				Expect(r.ConstLabels).To(HaveKeyWithValue("os_name", "Fedora Linux"))
				Expect(r.ConstLabels).To(HaveKeyWithValue("os_id", "fedora"))
				Expect(r.ConstLabels).To(HaveKeyWithValue("kernel_release", "6.2.0"))
				Expect(r.Value).To(Equal(1.0))
			}
		}
		Expect(found).To(BeTrue())
	})

	It("should parse GuestGetHostName", func() {
		report.Stats.GuestGetHostName = `{"host-name":"myhost"}`
		results := collectGuestMetrics(report)

		var found bool
		for _, r := range results {
			if r.Metric.GetOpts().Name == "kubevirt_vmi_guest_hostname" {
				found = true
				Expect(r.ConstLabels).To(HaveKeyWithValue("hostname", "myhost"))
			}
		}
		Expect(found).To(BeTrue())
	})

	It("should parse GuestGetUsers and count them", func() {
		report.Stats.GuestGetUsers = `[{"user":"root"},{"user":"testuser"}]`
		results := collectGuestMetrics(report)

		var found bool
		for _, r := range results {
			if r.Metric.GetOpts().Name == "kubevirt_vmi_guest_user_count" {
				found = true
				Expect(r.Value).To(Equal(2.0))
			}
		}
		Expect(found).To(BeTrue())
	})

	It("should parse GuestGetDevices", func() {
		report.Stats.GuestGetDevices = `[
		 {"driver-date":1651363200000000000,"driver-name":"Red Hat VirtIO SCSI controller","driver-version":"100.85.104.20800","id":{"device-id":4162,"vendor-id":6900,"type":"pci"}}
		]`

		results := collectGuestDevices(report)

		Expect(results).To(HaveLen(1))
		Expect(results[0].Value).To(Equal(1651363200.0))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("driver_name", "Red Hat VirtIO SCSI controller"))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("driver_version", "100.85.104.20800"))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("device_type", "pci"))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("vendor_id", "0x1af4"))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("device_id", "0x1042"))
	})

	It("should emit empty labels for optional GuestGetDevices fields", func() {
		report.Stats.GuestGetDevices = `[
		 {"driver-date":1736726400000000000,"driver-name":"VirtIO Balloon Driver"}
		]`

		results := collectGuestDevices(report)

		Expect(results).To(HaveLen(1))
		Expect(results[0].Value).To(Equal(1736726400.0))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("driver_name", "VirtIO Balloon Driver"))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("driver_version", ""))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("device_type", ""))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("vendor_id", ""))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("device_id", ""))
	})

	It("should not emit duplicate series for devices reported twice", func() {
		report.Stats.GuestGetDevices = `[
		 {"driver-date":1771372800000000000,"driver-name":"Red Hat VirtIO SCSI controller","driver-version":"100.103.104.29700","id":{"device-id":4162,"vendor-id":6900,"type":"pci"}},
		 {"driver-date":1768953600000000000,"driver-name":"Red Hat VirtIO SCSI pass-through controller","driver-version":"100.102.104.29500","id":{"device-id":4168,"vendor-id":6900,"type":"pci"}},
		 {"driver-date":1771372800000000000,"driver-name":"Red Hat VirtIO SCSI controller","driver-version":"100.103.104.29700","id":{"device-id":4162,"vendor-id":6900,"type":"pci"}}
		]`

		results := collectGuestDevices(report)

		Expect(results).To(HaveLen(2), "the repeated SCSI controller should be reported once")

		Expect(results[0].Value).To(Equal(1771372800.0))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("driver_name", "Red Hat VirtIO SCSI controller"))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("device_id", "0x1042"))

		Expect(results[1].Value).To(Equal(1768953600.0))
		Expect(results[1].ConstLabels).To(HaveKeyWithValue("driver_name", "Red Hat VirtIO SCSI pass-through controller"))
		Expect(results[1].ConstLabels).To(HaveKeyWithValue("device_id", "0x1048"))
	})

	It("should keep distinct devices that differ only by device id", func() {
		report.Stats.GuestGetDevices = `[
		 {"driver-date":1771372800000000000,"driver-name":"Red Hat VirtIO SCSI controller","driver-version":"100.103.104.29700","id":{"device-id":4100,"vendor-id":6900,"type":"pci"}},
		 {"driver-date":1771372800000000000,"driver-name":"Red Hat VirtIO SCSI controller","driver-version":"100.103.104.29700","id":{"device-id":4162,"vendor-id":6900,"type":"pci"}}
		]`

		Expect(collectGuestDevices(report)).To(HaveLen(2))
	})

	It("should not drop every device when a PCI ID exceeds uint16", func() {
		report.Stats.GuestGetDevices = `[
		 {"driver-date":1749427200000000000,"driver-name":"Red Hat VirtIO Ethernet Adapter","driver-version":"100.101.104.28200","id":{"device-id":4161,"vendor-id":70000,"type":"pci"}},
		 {"driver-date":1736726400000000000,"driver-name":"VirtIO Balloon Driver","driver-version":"100.100.104.27100","id":{"device-id":4165,"vendor-id":6900,"type":"pci"}}
		]`

		results := collectGuestDevices(report)

		Expect(results).To(HaveLen(2))
		Expect(results[0].ConstLabels).To(HaveKeyWithValue("driver_name", "Red Hat VirtIO Ethernet Adapter"))
		Expect(results[1].ConstLabels).To(HaveKeyWithValue("driver_name", "VirtIO Balloon Driver"))
	})

	It("should skip devices without a driver date", func() {
		report.Stats.GuestGetDevices = `[
		 {"driver-name":"VirtIO Input Driver","driver-version":"100.100.104.27100","id":{"device-id":4178,"vendor-id":6900,"type":"pci"}}
		]`

		Expect(collectGuestDevices(report)).To(BeEmpty())
	})

	It("should include GuestGetDevices in the collected guest metrics", func() {
		report.Stats.GuestGetDevices = `[
		 {"driver-date":1736726400000000000,"driver-name":"VirtIO Serial Driver","driver-version":"100.100.104.27100","id":{"device-id":4163,"vendor-id":6900,"type":"pci"}}
		]`

		var names []string
		for _, r := range collectGuestMetrics(report) {
			names = append(names, r.Metric.GetOpts().Name)
		}
		Expect(names).To(ContainElement("kubevirt_vmi_guest_device_driver_date_seconds"))
	})

	It("should skip malformed JSON gracefully", func() {
		report.Stats.GuestGetOsInfo = `{invalid json`
		results := collectGuestMetrics(report)

		for _, r := range results {
			Expect(r.Metric.GetOpts().Name).ToNot(Equal("kubevirt_vmi_guest_os_info"))
		}
	})
})
