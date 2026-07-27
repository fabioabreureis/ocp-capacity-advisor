/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	advisorv1alpha1 "github.com/openshift/ocp-capacity-advisor/api/v1alpha1"
)

var _ = Describe("CapacityAdvisor Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "cluster"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{Name: resourceName}

		BeforeEach(func() {
			By("creating the CapacityAdvisor CR")
			capacityadvisor := &advisorv1alpha1.CapacityAdvisor{}
			err := k8sClient.Get(ctx, typeNamespacedName, capacityadvisor)
			if err != nil && errors.IsNotFound(err) {
				resource := &advisorv1alpha1.CapacityAdvisor{
					ObjectMeta: metav1.ObjectMeta{Name: resourceName},
					Spec: advisorv1alpha1.CapacityAdvisorSpec{
						CPUTargetPercent:    70,
						MemoryTargetPercent: 70,
						PodsTargetPercent:   80,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &advisorv1alpha1.CapacityAdvisor{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			By("Cleanup CapacityAdvisor")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &CapacityAdvisorReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &advisorv1alpha1.CapacityAdvisor{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.ObservedAt.IsZero()).To(BeFalse())
		})
	})
})
