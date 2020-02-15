package helper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	yaml "sigs.k8s.io/yaml"

	"github.com/tidwall/gjson"

	"github.com/gobwas/glob"
	nmstatev1alpha1 "github.com/nmstate/kubernetes-nmstate/pkg/apis/nmstate/v1alpha1"
	"github.com/nmstate/kubernetes-nmstate/pkg/configurations"
)

var (
	log = logf.Log.WithName("client")
)

const nmstateCommand = "nmstatectl"
const vlanFilteringCommand = "vlan-filtering"
const defaultGwRetrieveTimeout = 120 * time.Second
const defaultGwProbeTimeout = 120 * time.Second
const apiServerProbeTimeout = 120 * time.Second

func show(arguments ...string) (string, error) {
	cmd := exec.Command(nmstateCommand, "show")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to execute nmstatectl show: '%v', '%s', '%s'", err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

func applyVlanFiltering(bridgeName string, ports []string) (string, error) {
	command := []string{bridgeName}
	command = append(command, ports...)

	cmd := exec.Command(vlanFilteringCommand, command...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to execute %s: '%v', '%s', '%s'", vlanFilteringCommand, err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

func nmstatectl(arguments []string, input string) (string, error) {
	cmd := exec.Command(nmstateCommand, arguments...)
	var stdout, stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	if input != "" {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return "", fmt.Errorf("failed to create pipe for writing into %s: %v", nmstateCommand, err)
		}
		go func() {
			defer stdin.Close()
			_, err = io.WriteString(stdin, input)
			if err != nil {
				fmt.Printf("failed to write input into stdin: %v\n", err)
			}
		}()

	}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to execute %s %s: '%v' '%s' '%s'", nmstateCommand, strings.Join(arguments, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil

}

func set(desiredState nmstatev1alpha1.State) (string, error) {
	// commit timeout doubles the default gw ping probe timeout, to
	// ensure the Checkpoint is alive before rolling it back
	// https://nmstate.github.io/cli_guide#manual-transaction-control
	output, err := nmstatectl([]string{"set", "--no-commit", "--timeout", strconv.Itoa(int(defaultGwProbeTimeout.Seconds()) * 2)}, string(desiredState.Raw))
	return output, err
}

func commit() (string, error) {
	return nmstatectl([]string{"commit"}, "")
}

func rollback(cause error) error {
	_, err := nmstatectl([]string{"rollback"}, "")
	return fmt.Errorf("rollback cause: %v, rollback error: %v", cause, err)
}

func GetNodeNetworkState(client client.Client, nodeName string) (nmstatev1alpha1.NodeNetworkState, error) {
	var nodeNetworkState nmstatev1alpha1.NodeNetworkState
	nodeNetworkStateKey := types.NamespacedName{
		Name: nodeName,
	}
	err := client.Get(context.TODO(), nodeNetworkStateKey, &nodeNetworkState)
	return nodeNetworkState, err
}

func InitializeNodeNeworkState(client client.Client, node *corev1.Node, scheme *runtime.Scheme) error {
	ownerRefList := []metav1.OwnerReference{{Name: node.ObjectMeta.Name, Kind: "Node", APIVersion: "v1", UID: node.UID}}

	nodeNetworkState := nmstatev1alpha1.NodeNetworkState{
		// Create NodeNetworkState for this node
		ObjectMeta: metav1.ObjectMeta{
			Name:            node.ObjectMeta.Name,
			OwnerReferences: ownerRefList,
		},
	}

	err := client.Create(context.TODO(), &nodeNetworkState)
	if err != nil {
		return fmt.Errorf("error creating NodeNetworkState: %v, %+v", err, nodeNetworkState)
	}

	return nil
}

func UpdateCurrentState(client client.Client, nodeNetworkState *nmstatev1alpha1.NodeNetworkState) error {
	observedStateRaw, err := show()
	if err != nil {
		return fmt.Errorf("error running nmstatectl show: %v", err)
	}
	observedState := nmstatev1alpha1.State{Raw: []byte(observedStateRaw)}
	fmt.Println("qaaaaaaaaaaaaaaa ", configurations.GetCurrentConfig().InterfaceFilter)
	interfacesFilterGlob := glob.MustCompile(configurations.GetCurrentConfig().InterfaceFilter)
	stateToReport, err := filterOut(observedState, interfacesFilterGlob)
	if err != nil {
		fmt.Printf("failed filtering out interfaces from NodeNetworkState, keeping orignal content, please fix the glob: %v", err)
		stateToReport = observedState
	}

	nodeNetworkState.Status.CurrentState = stateToReport
	nodeNetworkState.Status.LastSuccessfulUpdateTime = metav1.Time{Time: time.Now()}

	err = client.Status().Update(context.Background(), nodeNetworkState)
	if err != nil {
		return err
	}

	return nil
}

func ping(target string, timeout time.Duration) (string, error) {
	output := ""
	return output, wait.PollImmediate(1*time.Second, timeout, func() (bool, error) {
		cmd := exec.Command("ping", "-c", "1", target)
		var outputBuffer bytes.Buffer
		cmd.Stdout = &outputBuffer
		cmd.Stderr = &outputBuffer
		err := cmd.Run()
		output = fmt.Sprintf("cmd output: '%s'", outputBuffer.String())
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

func checkApiServerConnectivity(timeout time.Duration) error {
	return wait.PollImmediate(1*time.Second, timeout, func() (bool, error) {
		// Create new custom client to bypass cache [1]
		// [1] https://github.com/operator-framework/operator-sdk/blob/master/doc/user/client.md#non-default-client
		config, err := config.GetConfig()
		if err != nil {
			return false, errors.Wrap(err, "getting config")
		}
		// Since we are going to retrieve Nodes default schema is good
		// enough, also align timeout with poll
		config.Timeout = timeout
		client, err := client.New(config, client.Options{})
		if err != nil {
			log.Error(err, "failed to creating new custom client")
			return false, nil
		}
		err = client.Get(context.TODO(), types.NamespacedName{Name: metav1.NamespaceDefault}, &corev1.Namespace{})
		if err != nil {
			log.Error(err, "failed reaching the apiserver")
			return false, nil
		}
		return true, nil
	})
}

func defaultGw() (string, error) {
	defaultGw := ""
	return defaultGw, wait.PollImmediate(1*time.Second, defaultGwRetrieveTimeout, func() (bool, error) {
		observedStateRaw, err := show()
		if err != nil {
			log.Error(err, fmt.Sprintf("failed retrieving current state"))
			return false, nil
		}

		currentState, err := yaml.YAMLToJSON([]byte(observedStateRaw))
		if err != nil {
			return false, errors.Wrap(err, "failed to convert current state to JSON")
		}

		defaultGw = gjson.ParseBytes(currentState).
			Get("routes.running.#(destination==\"0.0.0.0/0\").next-hop-address").String()
		if defaultGw == "" {
			log.Info("default gw missing", "state", string(currentState))
			return false, nil
		}

		return true, nil
	})
}

func ApplyDesiredState(desiredState nmstatev1alpha1.State) (string, error) {
	if len(string(desiredState.Raw)) == 0 {
		return "Ignoring empty desired state", nil
	}

	setOutput, err := set(desiredState)
	if err != nil {
		return setOutput, err
	}

	// Future versions of nmstate/NM will support vlan-filtering meanwhile
	// we have to enforce it at the desiredState bridges and outbound ports
	// they will be configured with vlan_filtering 1 and all the vlan id range
	// set
	bridgesUpWithPorts, err := getBridgesUp(desiredState)
	if err != nil {
		return "", rollback(fmt.Errorf("error retrieving up bridges from desired state"))
	}

	commandOutput := ""
	for bridge, ports := range bridgesUpWithPorts {
		outputVlanFiltering, err := applyVlanFiltering(bridge, ports)
		commandOutput += fmt.Sprintf("bridge %s ports %v applyVlanFiltering command output: %s\n", bridge, ports, outputVlanFiltering)
		if err != nil {
			return commandOutput, rollback(err)
		}
	}

	defaultGw, err := defaultGw()
	if err != nil {
		return commandOutput, rollback(err)
	}

	currentState, err := show()
	if err != nil {
		return "", rollback(err)
	}

	// TODO: Make ping timeout configurable with a config map
	pingOutput, err := ping(defaultGw, defaultGwProbeTimeout)
	if err != nil {
		return pingOutput, rollback(fmt.Errorf("error pinging external address after network reconfiguration -> error: %v, currentState: %s", err, currentState))
	}

	err = checkApiServerConnectivity(apiServerProbeTimeout)
	if err != nil {
		return "", rollback(fmt.Errorf("error checking api server connectivity after network reconfiguration -> error: %v, currentState: %s", err, currentState))
	}

	commitOutput, err := commit()
	if err != nil {
		// We cannot rollback if commit fails, just return the error
		return commitOutput, err
	}

	commandOutput += fmt.Sprintf("setOutput: %s \n", setOutput)
	return commandOutput, nil
}

func filterOut(currentState nmstatev1alpha1.State, interfacesFilterGlob glob.Glob) (nmstatev1alpha1.State, error) {
	if interfacesFilterGlob.Match("") {
		return currentState, nil
	}

	var state map[string]interface{}
	err := yaml.Unmarshal(currentState.Raw, &state)
	if err != nil {
		return currentState, err
	}

	interfaces := state["interfaces"]
	var filteredInterfaces []interface{}

	for _, iface := range interfaces.([]interface{}) {
		name := iface.(map[string]interface{})["name"]
		if !interfacesFilterGlob.Match(name.(string)) {
			filteredInterfaces = append(filteredInterfaces, iface)
		}
	}

	state["interfaces"] = filteredInterfaces
	filteredState, err := yaml.Marshal(state)
	if err != nil {
		return currentState, err
	}

	return nmstatev1alpha1.State{Raw: filteredState}, nil
}
