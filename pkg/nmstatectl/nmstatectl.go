package nmstatectl

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	nmstatev1alpha1 "github.com/nmstate/kubernetes-nmstate/pkg/apis/nmstate/v1alpha1"
)

var (
	log = logf.Log.WithName("nmstatectl")
)

//go:generate mockgen -package nmstatectl -destination nmstatectl_mock.go github.com/nmstate/kubernetes-nmstate/pkg/nmstatectl Nmstatectl
type Nmstatectl interface {
	Show() (string, error)
	Commit() (string, error)
	Rollback(cause error) error
	Set(desiredState nmstatev1alpha1.State, timeout time.Duration) (string, error)
}

const nmstateCommand = "nmstatectl"

type Helper struct {}

func (h *Helper) Show() (string, error) {
	return Show()
}

func (h *Helper) Commit() (string, error) {
	return Commit()
}

func (h *Helper) Rollback(cause error) error {
	return Rollback(cause)
}

func (h *Helper) Set(desiredState nmstatev1alpha1.State, timeout time.Duration) (string, error) {
	return Set(desiredState, timeout)
}

func nmstatectlWithInput(arguments []string, input string) (string, error) {
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

func nmstatectl(arguments []string) (string, error) {
	return nmstatectlWithInput(arguments, "")
}

func Show(arguments ...string) (string, error) {
	return nmstatectl([]string{"show"})
}

func Set(desiredState nmstatev1alpha1.State, timeout time.Duration) (string, error) {
	var setDoneCh = make(chan struct{})
	go setUnavailableUp(setDoneCh)
	defer close(setDoneCh)

	setOutput, err := nmstatectlWithInput([]string{"set", "--no-commit", "--timeout", strconv.Itoa(int(timeout.Seconds()))}, string(desiredState.Raw))
	return setOutput, err
}

func Commit() (string, error) {
	return nmstatectl([]string{"commit"})
}

func Rollback(cause error) error {
	message := "rollback cause: %v"
	_, err := nmstatectl([]string{"rollback"})
	if err != nil {
		return errors.Wrapf(err, message, cause)
	}
	return fmt.Errorf(message, cause)
}
