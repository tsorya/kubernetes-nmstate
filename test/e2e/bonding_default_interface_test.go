package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	nmstatev1alpha1 "github.com/nmstate/kubernetes-nmstate/pkg/apis/nmstate/v1alpha1"
)

func bondUpWithEth0AndEth1(bondName string) nmstatev1alpha1.State {
	return nmstatev1alpha1.NewState(fmt.Sprintf(`interfaces:
  - name: %s
    type: bond
    state: up
    ipv4:
      dhcp: true
      enabled: true
    link-aggregation:
      mode: balance-rr
      options:
        miimon: '140'
      slaves:
        - %s
        - %s
`, bondName, *primaryNic, *firstSecondaryNic))
}

func bondAbsentWithEth0Up(bondName string) nmstatev1alpha1.State {
	return nmstatev1alpha1.NewState(fmt.Sprintf(`interfaces:
  - name: %s
    type: bond
    state: absent
  - name: %s
    state: up
    type: ethernet
    ipv4:
      dhcp: true
      enabled: true
`, bondName, *primaryNic))
}

var _ = Describe("NodeNetworkConfigurationPolicy bonding default interface", func() {
	Context("when there is a default interface with dynamic address", func() {
		addressByNode := map[string]string{}
		BeforeEach(func() {
			By(fmt.Sprintf("Check %s is the default route interface and has dynamic address", *primaryNic))
			for _, node := range nodes {
				defaultRouteNextHopInterface(node).Should(Equal(*primaryNic))
				Expect(dhcpFlag(node, *primaryNic)).Should(BeTrue())
			}

			By("Fetching current IP address")
			for _, node := range nodes {
				address := ""
				Eventually(func() string {
					address = ipv4Address(node, *primaryNic)
					return address
				}, 15*time.Second, 1*time.Second).ShouldNot(BeEmpty(), fmt.Sprintf("Interface %s has no ipv4 address", *primaryNic))
				By(fmt.Sprintf("Fetching current IP address %s", address))
				addressByNode[node] = address
			}
			By(fmt.Sprintf("Reseting state of %s", *firstSecondaryNic))
			resetNicStateForNodes(*firstSecondaryNic)
			By(fmt.Sprintf("Creating %s on %s and %s", bond1, *primaryNic, *firstSecondaryNic))
			updateDesiredState(bondUpWithEth0AndEth1(bond1))
			waitForAvailableTestPolicy()
			By("Done BeforeEch stage")

		})
		AfterEach(func() {
			By(fmt.Sprintf("Removing bond %s and configuring %s with dhcp", bond1, *primaryNic))
			updateDesiredState(bondAbsentWithEth0Up(bond1))
			waitForAvailableTestPolicy()

			By("Waiting until the node becomes ready again")
			for _, node := range nodes {

				interfacesNameForNodeEventually(node).ShouldNot(ContainElement(bond1))
			}

			resetDesiredStateForNodes()

			By(fmt.Sprintf("Check %s has the default ip address", *primaryNic))
			for _, node := range nodes {
				Eventually(func() string {
					return ipv4Address(node, *primaryNic)
				}, 30*time.Second, 1*time.Second).Should(Equal(addressByNode[node]), fmt.Sprintf("Interface %s address is not the original one", *primaryNic))
			}


		})

		It("should successfully move default IP address on top of the bond", func() {
			var (
				expectedBond  = interfaceByName(interfaces(bondUpWithEth0AndEth1(bond1)), bond1)
				expectedSpecs = expectedBond["link-aggregation"].(map[string]interface{})
			)

			By("Checking that bond was configured and obtained the same IP address")
			for _, node := range nodes {
				interfacesForNode(node).Should(ContainElement(SatisfyAll(
					HaveKeyWithValue("name", expectedBond["name"]),
					HaveKeyWithValue("type", expectedBond["type"]),
					HaveKeyWithValue("state", expectedBond["state"]),
					HaveKeyWithValue("link-aggregation", HaveKeyWithValue("mode", expectedSpecs["mode"])),
					HaveKeyWithValue("link-aggregation", HaveKeyWithValue("options", expectedSpecs["options"])),
					HaveKeyWithValue("link-aggregation", HaveKeyWithValue("slaves", ConsistOf([]string{*primaryNic, *firstSecondaryNic}))),
				)))

				Eventually(func() string {
					return ipv4Address(node, bond1)
				}, 30*time.Second, 1*time.Second).Should(Equal(addressByNode[node]), fmt.Sprintf("Interface bond1 has not take over the %s address", *primaryNic))
			}
			// Restart only first node that it master if other node is restarted it will stuck in NotReady state
			nodeToReboot := nodes[0]
			By(fmt.Sprintf("Reboot node %s and verify that bond still has ip of primary nic", nodeToReboot))
			err := restartNode(nodeToReboot)
			Expect(err).ToNot(HaveOccurred())
			By(fmt.Sprintf("Node %s was rebooted, verifying %s exists and ip was not changed", nodeToReboot, bond1))
			interfacesForNode(nodeToReboot).Should(ContainElement(SatisfyAll(
				HaveKeyWithValue("name", expectedBond["name"]),
				HaveKeyWithValue("type", expectedBond["type"]),
				HaveKeyWithValue("state", expectedBond["state"]),
				HaveKeyWithValue("link-aggregation", HaveKeyWithValue("mode", expectedSpecs["mode"])),
				HaveKeyWithValue("link-aggregation", HaveKeyWithValue("options", expectedSpecs["options"])),
				HaveKeyWithValue("link-aggregation", HaveKeyWithValue("slaves", ConsistOf([]string{*primaryNic, *firstSecondaryNic}))),
			)))

			Eventually(func() string {
				return ipv4Address(nodeToReboot, bond1)
			}, 30*time.Second, 1*time.Second).Should(Equal(addressByNode[nodeToReboot]), fmt.Sprintf("Interface bond1 has not take over the %s address", *primaryNic))
		})
	})
})


func resetNicStateForNodes(nicName string) {
	updateDesiredState(ethernetNicUp(nicName))
	waitForAvailableTestPolicy()
	deletePolicy(TestPolicy)
}
