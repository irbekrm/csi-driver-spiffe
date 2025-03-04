/*
Copyright 2021 The cert-manager Authors.

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

package test

import (
	"context"
	"errors"

	"github.com/jetstack/cert-manager/pkg/api"
	apiutil "github.com/jetstack/cert-manager/pkg/api/util"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/jetstack/cert-manager/pkg/apis/meta/v1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cert-manager/csi-driver-spiffe/internal/approver/controller"
	evaluatorfake "github.com/cert-manager/csi-driver-spiffe/internal/approver/evaluator/fake"
)

var _ = Context("Approval", func() {
	var (
		ctx    context.Context
		cancel func()

		cl        client.Client
		namespace corev1.Namespace

		evaluator = evaluatorfake.New()
		issuerRef = cmmeta.ObjectReference{
			Name:  "spiffe-ca",
			Kind:  "ClusterIssuer",
			Group: "cert-manager.io",
		}
	)

	JustBeforeEach(func() {
		ctx, cancel = context.WithCancel(context.TODO())

		var err error
		cl, err = client.New(env.Config, client.Options{Scheme: api.Scheme})
		Expect(err).NotTo(HaveOccurred())

		namespace = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-csi-driver-spiffe-",
			},
		}
		Expect(cl.Create(ctx, &namespace)).NotTo(HaveOccurred())

		log := klogr.New().WithName("testing")
		mgr, err := ctrl.NewManager(env.Config, ctrl.Options{
			Scheme:                        api.Scheme,
			LeaderElection:                true,
			MetricsBindAddress:            "0",
			LeaderElectionNamespace:       namespace.Name,
			LeaderElectionID:              "cert-manager-csi-driver-spiffe-approver",
			LeaderElectionReleaseOnCancel: true,
			Logger:                        log,
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(controller.AddApprover(ctx, log, controller.Options{
			Manager:   mgr,
			Evaluator: evaluator,
			IssuerRef: issuerRef,
		})).NotTo(HaveOccurred())

		By("Running Approver controller")
		go mgr.Start(ctx)

		By("Waiting for Leader Election")
		<-mgr.Elected()

		By("Waiting for Informers to Sync")
		Expect(mgr.GetCache().WaitForCacheSync(ctx)).Should(BeTrue())
	})

	JustAfterEach(func() {
		Expect(cl.Delete(ctx, &namespace)).NotTo(HaveOccurred())
		cancel()
	})

	It("should ignore CertificateRequest that have the wrong IssuerRef", func() {
		cr := cmapi.CertificateRequest{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cert-manager-csi-driver-spiffe-",
				Namespace:    namespace.Name,
			},
			Spec: cmapi.CertificateRequestSpec{
				Request: []byte("request"),
				IssuerRef: cmmeta.ObjectReference{
					Name:  "not-spiffe-ca",
					Kind:  "ClusterIssuer",
					Group: "cert-manager.io",
				},
			},
		}
		Expect(cl.Create(ctx, &cr)).NotTo(HaveOccurred())

		Consistently(func() bool {
			Eventually(func() error {
				return cl.Get(ctx, client.ObjectKeyFromObject(&cr), &cr)
			}).Should(BeNil())
			return apiutil.CertificateRequestIsApproved(&cr) || apiutil.CertificateRequestIsDenied(&cr)
		}, "3s").Should(BeFalse(), "expected neither approved not denied")
	})

	It("should deny CertificateRequest when the evaluator returns error", func() {
		evaluator.WithEvaluate(func(_ *cmapi.CertificateRequest) error {
			return errors.New("this is an error")
		})

		cr := cmapi.CertificateRequest{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cert-manager-csi-driver-spiffe-",
				Namespace:    namespace.Name,
			},
			Spec: cmapi.CertificateRequestSpec{
				Request:   []byte("request"),
				IssuerRef: issuerRef,
			},
		}
		Expect(cl.Create(ctx, &cr)).NotTo(HaveOccurred())

		Eventually(func() bool {
			Eventually(func() error {
				return cl.Get(ctx, client.ObjectKeyFromObject(&cr), &cr)
			}).Should(BeNil())
			return apiutil.CertificateRequestIsDenied(&cr)
		}).Should(BeTrue(), "expected denial")
	})

	It("should approve CertificateRequest when the evaluator returns nil", func() {
		evaluator.WithEvaluate(func(_ *cmapi.CertificateRequest) error {
			return nil
		})

		cr := cmapi.CertificateRequest{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cert-manager-csi-driver-spiffe-",
				Namespace:    namespace.Name,
			},
			Spec: cmapi.CertificateRequestSpec{
				Request:   []byte("request"),
				IssuerRef: issuerRef,
			},
		}
		Expect(cl.Create(ctx, &cr)).NotTo(HaveOccurred())

		Eventually(func() bool {
			Eventually(func() error {
				return cl.Get(ctx, client.ObjectKeyFromObject(&cr), &cr)
			}).Should(BeNil())
			return apiutil.CertificateRequestIsApproved(&cr)
		}).Should(BeTrue(), "expected approval")
	})
})
