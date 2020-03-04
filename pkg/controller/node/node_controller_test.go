package node

import (
	"context"

	nmstate "github.com/nmstate/kubernetes-nmstate/pkg/helper"

	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nmstatev1alpha1 "github.com/nmstate/kubernetes-nmstate/pkg/apis/nmstate/v1alpha1"
)

var _ = Describe("Node controller reconcile", func() {
	var (
		cl               client.Client
		reconciler       ReconcileNode
		existingNodeName = "node01"
		node             = corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: existingNodeName,
				UID:  "12345",
			},
		}
		nodenetworkstate = nmstatev1alpha1.NodeNetworkState{
			ObjectMeta: metav1.ObjectMeta{
				Name: existingNodeName,
			},
		}
	)
	BeforeEach(func() {
		s := scheme.Scheme
		s.AddKnownTypes(nmstatev1alpha1.SchemeGroupVersion,
			&nmstatev1alpha1.NodeNetworkState{},
		)

		objs := []runtime.Object{&node, &nodenetworkstate}

		// Create a fake client to mock API calls.
		cl = fake.NewFakeClientWithScheme(s, objs...)

		reconciler.client = cl
		reconciler.nmstateUpdater = nmstate.CreateOrUpdateNodeNetworkState
	})
	Context("when node is not found", func() {
		var (
			request reconcile.Request
		)
		BeforeEach(func() {
			request.Name = "not-present-node"
		})
		It("should returns empty result", func() {
			result, err := reconciler.Reconcile(request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})
	Context("when a node is found", func() {
		var (
			request reconcile.Request
		)
		BeforeEach(func() {
			request.Name = existingNodeName
		})
		Context("and nodenetworkstate is there too", func() {
			It("should return a Result with RequeueAfter set (trigger re-reconciliation)", func() {
				// Mocking nmstatectl.Show
				prevUpdater := reconciler.nmstateUpdater
				reconciler.nmstateUpdater = func(client client.Client, node *corev1.Node,
					namespace client.ObjectKey) error {
					fmt.Println("Using mock nmstateUpdater")
					return nil
				}

				result, err := reconciler.Reconcile(request)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{RequeueAfter: nodeRefresh}))
				reconciler.nmstateUpdater = prevUpdater
			})
		})
		Context("and nodenetworkstate is not there", func() {
			BeforeEach(func() {
				By("Delete the nodenetworkstate")
				err := cl.Delete(context.TODO(), &nodenetworkstate)
				Expect(err).ToNot(HaveOccurred())
			})
			It("should create a new nodenetworkstate with node as owner reference, making sure the nodenetworkstate will be removed when the node is deleted", func() {
				_, err := reconciler.Reconcile(request)
				Expect(err).ToNot(HaveOccurred())

				obtainedNNS := nmstatev1alpha1.NodeNetworkState{}
				nnsKey := types.NamespacedName{Name: existingNodeName}
				err = cl.Get(context.TODO(), types.NamespacedName{Name: existingNodeName}, &obtainedNNS)
				Expect(err).ToNot(HaveOccurred())
				Expect(obtainedNNS.Name).To(Equal(nnsKey.Name))
				Expect(obtainedNNS.ObjectMeta.OwnerReferences).To(HaveLen(1))
				Expect(obtainedNNS.ObjectMeta.OwnerReferences[0]).To(Equal(
					metav1.OwnerReference{Name: existingNodeName, Kind: "Node", APIVersion: "v1", UID: node.UID},
				))
			})
			It("should return a Result with RequeueAfter set (trigger re-reconciliation)", func() {
				result, err := reconciler.Reconcile(request)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{RequeueAfter: nodeRefresh}))
			})
		})
	})
})
