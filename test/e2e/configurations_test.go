package e2e

import (
	"os"

	framework "github.com/operator-framework/operator-sdk/pkg/test"

	"github.com/nmstate/kubernetes-nmstate/build/_output/bin/go/src/fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	testValues    = "node_network_state_refresh_interval: \"1\"\ninterfaces_filter: \"eth1*\"\n"
	defaultValues = "node_network_state_refresh_interval: \"5\"\ninterfaces_filter: \"veth*\"\n"
)

var _ = Describe("Configurations test", func() {
	Context("when vlan configured", func() {
		BeforeEach(func() {
			writeTestYaml(testValues)
			By(fmt.Sprintf("Verifying %s is in current state", firstSecondaryNic))
			for _, node := range nodes {
				interfacesNameForNodeEventually(node).Should(ContainElement(firstSecondaryNic))
			}
		})
		AfterEach(func() {
			kubectlAndCheck("delete", "cm", "nmstate-config", "-n", framework.Global.Namespace)
		})

		It("should have NodeNetworkState with currentState for each node", func() {
			kubectlAndCheck("create", "cm", "nmstate-config",
				"--from-file=/tmp/nmstate.yaml", "-n", framework.Global.Namespace)

			By(fmt.Sprintf("Verifying %s is not in current state", firstSecondaryNic))
			for _, node := range nodes {
				interfacesNameForNodeEventually(node).ShouldNot(ContainElement(firstSecondaryNic))
			}

			By("Returning to default values")
			kubectlAndCheck("delete", "cm", "nmstate-config", "-n", framework.Global.Namespace)
			writeTestYaml(defaultValues)
			kubectlAndCheck("create", "cm", "nmstate-config",
				"--from-file=/tmp/nmstate.yaml", "-n", framework.Global.Namespace)
			By(fmt.Sprintf("Verifying %s is in current state", firstSecondaryNic))
			for _, node := range nodes {
				interfacesNameForNodeEventually(node).Should(ContainElement(firstSecondaryNic))
			}
		})
	})
})

func writeTestYaml(text string) {
	By("Writing configuration file")
	f, err := os.OpenFile("/tmp/nmstate.yaml", os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()
	if err != nil {
		panic("Failed to create test file")
	}
	f.WriteString(text)
}
