#!/bin/bash

set -ex

kubectl=./cluster/kubectl.sh
manifests_dir=deploy

function getDesiredNumberScheduled {
    $kubectl get daemonset -n nmstate $1 -o=jsonpath='{.status.desiredNumberScheduled}'
}

function getNumberAvailable {
    numberAvailable=$($kubectl get daemonset -n nmstate $1 -o=jsonpath='{.status.numberAvailable}')
    echo ${numberAvailable:-0}
}

function eventually {
    timeout=30
    interval=5
    cmd=$@
    echo "Checking eventually $cmd"
    while ! $cmd; do
        sleep $interval
        timeout=$(( $timeout - $interval ))
        if [ $timeout -le 0 ]; then
            return 1
        fi
    done
}

function consistently {
    timeout=10
    interval=1
    cmd=$@
    echo "Checking consistently $cmd"
    while $cmd; do
        sleep $interval
        timeout=$(( $timeout - $interval ))
        if [ $timeout -le 0 ]; then
            return 0
        fi
    done
}

function isOk {
    desiredNumberScheduled=$(getDesiredNumberScheduled $1)
    numberAvailable=$(getNumberAvailable $1)
    [ "$desiredNumberScheduled" == "$numberAvailable" ]
}

function deploy() {
    # Cleanup previous deployment, if there is any
    make cluster-clean

    # Fetch registry port that can be used to upload images to the local kubevirtci cluster
    registry_port=$(./cluster/cli.sh ports registry | tr -d '\r')
    if [[ "${KUBEVIRT_PROVIDER}" =~ ^(okd|ocp)-.*$ ]]; then \
            registry=localhost:$(./cluster/cli.sh ports --container-name=cluster registry | tr -d '\r')
    else
        registry=localhost:$(./cluster/cli.sh ports registry | tr -d '\r')
    fi

    # Build new handler image from local sources and push it to the kubevirtci cluster
    IMAGE_REGISTRY=${registry} make push-handler

    # Deploy all needed manifests
    $kubectl apply -f $manifests_dir/namespace.yaml
    $kubectl apply -f $manifests_dir/service_account.yaml
    $kubectl apply -f $manifests_dir/role.yaml
    $kubectl apply -f $manifests_dir/role_binding.yaml
    $kubectl apply -f $manifests_dir/crds/nmstate.io_nodenetworkstates_crd.yaml
    $kubectl apply -f $manifests_dir/crds/nmstate.io_nodenetworkconfigurationpolicies_crd.yaml
    $kubectl apply -f $manifests_dir/crds/nmstate.io_nodenetworkconfigurationenactments_crd.yaml
    if [[ "$KUBEVIRT_PROVIDER" =~ ^(okd|ocp)-.*$ ]]; then
            $kubectl apply -f $manifests_dir/openshift/
    fi
    sed \
        -e "s#--v=production#--v=debug#" \
        -e "s#REPLACE_IMAGE#registry:5000/nmstate/kubernetes-nmstate-handler#" \
        $manifests_dir/operator.yaml | $kubectl create -f -

}

function wait_ready() {
    # Wait until the handler becomes consistently ready on all nodes
    for ds in nmstate-handler nmstate-handler-worker; do
        # We have to re-check desired number, sometimes takes some time to be filled in
        if ! eventually isOk $ds; then
            echo "Daemon set $ds haven't turned ready within the given timeout"
            exit 1
        fi

        # We have to re-check desired number, sometimes takes some time to be filled in
        if ! consistently isOk $ds; then
            echo "Daemon set $ds is not consistently ready within the given timeout"
            exit 1
        fi
    done
}

deploy
wait_ready
