/*
Copyright 2021 k0s authors

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
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cloudflare/cfssl/csr"
	"github.com/sirupsen/logrus"
	authzv1 "k8s.io/api/authorization/v1"
	certsv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/component/manager"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"
	certificates "k8s.io/kubernetes/pkg/apis/certificates"
)

type CSRApprover struct {
	log  *logrus.Entry
	stop context.CancelFunc

	ClusterConfig     *v1beta1.ClusterConfig
	KubeClientFactory kubeutil.ClientFactoryInterface
	leaderElector     leaderelector.Interface
	clientset         clientset.Interface
}

var _ manager.Component = (*CSRApprover)(nil)

// NewCSRApprover creates the CSRApprover component
func NewCSRApprover(c *v1beta1.ClusterConfig, leaderElector leaderelector.Interface, kubeClientFactory kubeutil.ClientFactoryInterface) *CSRApprover {
	d := atomic.Value{}
	d.Store(true)
	return &CSRApprover{
		ClusterConfig:     c,
		leaderElector:     leaderElector,
		KubeClientFactory: kubeClientFactory,
		log:               logrus.WithFields(logrus.Fields{"component": "csrapprover"}),
	}
}

// Stop stops the CSRApprover
func (a *CSRApprover) Stop() error {
	a.stop()
	return nil
}

// Init initializes the component needs
func (a *CSRApprover) Init(_ context.Context) error {
	var err error
	a.clientset, err = a.KubeClientFactory.GetClient()
	if err != nil {
		return fmt.Errorf("can't create kubernetes rest client for CSR check: %w", err)
	}

	return nil
}

// Run every 10 seconds checks for newly issued CSRs and approves them
func (a *CSRApprover) Start(ctx context.Context) error {
	ctx, a.stop = context.WithCancel(ctx)
	go func() {
		defer a.stop()
		ticker := time.NewTicker(10 * time.Second) // TODO: sometimes this should be refactored so it watches instead of polls for CSRs
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				err := a.approveCSR(ctx)
				if err != nil {
					a.log.WithError(err).Warn("CSR approval failed")
				}
			case <-ctx.Done():
				a.log.Info("CSR Approver context done")
				return
			}
		}
	}()

	return nil
}

func (a *CSRApprover) reconcileCSRs(ctx context.Context) error {
	return watch.CSRs(a.clientset.CertificatesV1().CertificateSigningRequests()).
		WithFieldSelector(fields.OneTermEqualSelector("spec.signerName", certsv1.KubeletServingSignerName)).
		Until(ctx, func(csr *certsv1.CertificateSigningRequest) (bool, error) {
			if !a.leaderElector.IsLeader() {
				a.log.Debug("Not the leader, won't approve CSRs")
				return true, nil
			}

		})
}

func (a *CSRApprover) reconcileKubeletServingCSR(ctx context.Context, csr *certsv1.CertificateSigningRequest) error {
	for _, cond := range csr.Status.Conditions {
		switch cond.Type {
		case certsv1.CertificateApproved, certsv1.CertificateDenied, certsv1.CertificateFailed:
			a.log.Debug("Not reconciling CSR %q with %v", csr.Name, cond)
			return nil
		}
	}

	csrClient := a.clientset.CertificatesV1().CertificateSigningRequests()

	updateFunc, cond := csrClient.UpdateApproval, certsv1.CertificateSigningRequestCondition{
		Type:    certsv1.CertificateApproved,
		Status:  corev1.ConditionTrue,
		Reason:  "AutoApproved",
		Message: "Auto approving kubelet serving CSR after SubjectAccessReview by k0s.",
	}

	certReq, err := a.validateCSR(ctx, csr)
	if err != nil {
		var condErr *conditionError
		if !errors.As(err, &condErr) {
			return err
		}
		updateFunc, cond = func(ctx context.Context, _ string, csr *certsv1.CertificateSigningRequest, opts metav1.UpdateOptions) (*certsv1.CertificateSigningRequest, error) {
			return csrClient.UpdateStatus(ctx, csr, opts)
		}, condErr.inner
	}

	cond.LastUpdateTime = metav1.Now()
	csr.Status.Conditions = append(csr.Status.Conditions, cond)
	csr, err = updateFunc(ctx, csr.Name, csr, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update CSR with %s: %w", &cond, err)
	}

	var msg := "Updated CSR %q"
	if certReq != nil 

	if certReq != nil {
		a.log.Infof("Updated CSR %q with SANs: %s, IP Addresses: %s", csr.Name, certReq.DNSNames, certReq.IPAddresses)
	}
	return nil
}

type conditionError struct {
	inner certsv1.CertificateSigningRequestCondition
}

func (c *conditionError) Error() string {
	return c.inner.String()
}

func (a *CSRApprover) validateCSR(ctx context.Context, csr *certsv1.CertificateSigningRequest) (*certsv1.CertificateSigningRequestCondition, error) {
	certReq, err := validateKubeletServingCSR(&csr.Spec)
	if err != nil {
		return nil, &conditionError{certsv1.CertificateSigningRequestCondition{
			Type:           certsv1.CertificateFailed,
			Status:         corev1.ConditionTrue,
			Reason:         "InvalidKubeletServingCSR",
			Message:        err.Error(),
			LastUpdateTime: metav1.Now(),
		}}
	}

	reviewStatus, err := a.reviewSubjectAccess(ctx, csr)
	if err != nil {
		return nil, fmt.Errorf("SubjectAccessReview failed: %w", csr.Name, err)
	}

	if !reviewStatus.Allowed {
		return &certsv1.CertificateSigningRequestCondition{
			Type:           certsv1.CertificateDenied,
			Status:         corev1.ConditionTrue,
			Reason:         "SubjectAccessReviewDenied",
			Message:        reviewStatus.Reason,
			LastUpdateTime: metav1.Now(),
		}, nil
	}

	if err := a.addCondition(ctx, csr, failedCondition); err != nil {
		return false, fmt.Errorf("failed to update CSR %q with %s: %w", csr.Name, failedCondition, err)
	}

	return false, nil

	return &certsv1.CertificateSigningRequestCondition{
		Type:    certsv1.CertificateApproved,
		Status:  corev1.ConditionTrue,
		Reason:  "AutoAproved",
		Message: "kubelet serving certificate Auto-approved by k0s after SubjectAccessReview.",
	}, nil

	if approvedCSR, err := a.clientset.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, csr.Name, csr, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("failed to approve CSR %q: %w", csr.Name, err)
	} else {
		csr = approvedCSR
	}

}

func findOffendingCondition(conditions []certsv1.CertificateSigningRequestCondition) *certsv1.CertificateSigningRequestCondition {
	for i := range conditions {
		c := &conditions[i]
		switch c.Type {
		case certsv1.CertificateApproved, certsv1.CertificateDenied, certsv1.CertificateFailed:
			return c
		}
	}

	return nil
}

func validateKubeletServingCSR(spec *certsv1.CertificateSigningRequestSpec) (*x509.CertificateRequest, error) {
	block, remainder := pem.Decode(spec.Request)
	if block == nil {
		return nil, errors.New("CSR doesn't contain a PEM formatted block")
	}
	if len(remainder) > 0 {
		return nil, errors.New("CSR contains additional data after the first PEM block")
	}
	if block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf(`CSR contains a %q PEM block when a "CERTIFICATE REQUEST" PEM block was expected`, block.Type)
	}

	certReq, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate request: %w", err)
	}

	usages := sets.NewString()
	for _, usage := range spec.Usages {
		usages.Insert(string(usage))
	}

	if err := certificates.ValidateKubeletServingCSR(certReq, usages); err != nil {
		return nil, err
	}

	return certReq, nil
}

// Majority of this code has been adapted from https://github.com/kontena/kubelet-rubber-stamp
func (a *CSRApprover) approveCSR(ctx context.Context, item *csr) error {

	if approved, denied := getCertApprovalCondition(&item.Status); approved || denied {
		a.log.Debugf("CSR %s is approved=%t || denied=%t. Carry on", item.Name, approved, denied)
		return nil
	}

	x509cr, err := parseCSR(item)
	if err != nil {
		return fmt.Errorf("unable to parse csr %q: %w", item.Name, err)
	}

	if err := a.ensureKubeletServingCert(item, x509cr); err != nil {
		a.log.WithError(err).Infof("Not approving CSR %q as it is not recognized as a kubelet-serving certificate", csr.Name)
		return nil
	}

	approved, err := a.authorize(ctx, item)
	if err != nil {
		return fmt.Errorf("SubjectAccessReview failed for CSR %q: %w", item.Name, err)
	}

	if !approved {
		return fmt.Errorf("failed to perform SubjectAccessReview for CSR %q", item.Name)
	}

	a.log.Infof("approving csr %s with SANs: %s, IP Addresses:%s", item.ObjectMeta.Name, x509cr.DNSNames, x509cr.IPAddresses)
	appendApprovalCondition(item, "Auto approving kubelet serving certificate after SubjectAccessReview.")
	_, err = a.clientset.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, item.Name, &csr, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating approval for CSR %q: %w", item.Name, err)
	}

	return nil
}

func (a *CSRApprover) reviewSubjectAccess(ctx context.Context, csr *certsv1.CertificateSigningRequest) (*authzv1.SubjectAccessReviewStatus, error) {
	extra := make(map[string]authzv1.ExtraValue, len(csr.Spec.Extra))
	for k, v := range csr.Spec.Extra {
		extra[k] = authzv1.ExtraValue(v)
	}

	sar := &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User:   csr.Spec.Username,
			UID:    csr.Spec.UID,
			Groups: csr.Spec.Groups,
			Extra:  extra,
			ResourceAttributes: &authzv1.ResourceAttributes{
				Verb:     "create",
				Group:    "certificates.k8s.io",
				Resource: "certificatesigningrequests",
			},
		},
	}

	sar, err := a.clientset.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return &sar.Status, nil
}

func (a *CSRApprover) ensureKubeletServingCert(csr *certsv1.CertificateSigningRequest, x509cr *x509.CertificateRequest) error {
	usages := sets.NewString()
	for _, usage := range csr.Spec.Usages {
		usages.Insert(string(usage))
	}

	return certificates.ValidateKubeletServingCSR(x509cr, usages)
}

func getCertApprovalCondition(status *certsv1.CertificateSigningRequestStatus) (approved, denied corev1.ConditionStatus) {
	approved, denied = corev1.ConditionUnknown, corev1.ConditionUnknown
	for _, c := range status.Conditions {
		if c.Type == certsv1.CertificateApproved {
			approved = c.Status
		}
		if c.Type == certsv1.CertificateDenied {
			denied = c.Status
		}
	}
	return
}

// parseCSR extracts the CSR from the API object and decodes it.
func parseCSR(obj *certsv1.CertificateSigningRequest) (*x509.CertificateRequest, error) {
	// extract PEM from request object
	pemBytes := obj.Spec.Request
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("PEM block type must be CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	return csr, nil
}

func appendApprovalCondition(csr *certsv1.CertificateSigningRequest, message string) {
	csr.Status.Conditions = append(csr.Status.Conditions, certsv1.CertificateSigningRequestCondition{
		Type:    certsv1.CertificateApproved,
		Reason:  "Autoapproved by K0s CSRApprover",
		Message: message,
		Status:  corev1.ConditionTrue,
	})
}
