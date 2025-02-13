/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2021 Red Hat, Inc.
 *
 */

package admitters

import (
	"context"
	"encoding/json"
	"fmt"

	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"

	k8sfield "k8s.io/apimachinery/pkg/util/validation/field"

	"kubevirt.io/api/migrations"

	migrationsv1 "kubevirt.io/api/migrations/v1alpha1"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/pkg/psa"
	webhookutils "kubevirt.io/kubevirt/pkg/util/webhooks"
)

// MigrationPolicyAdmitter validates VirtualMachineSnapshots
type MigrationPolicyAdmitter struct {
	ClusterConfig *virtconfig.ClusterConfig
	Client        kubecli.KubevirtClient
}

// NewMigrationPolicyAdmitter creates a MigrationPolicyAdmitter
func NewMigrationPolicyAdmitter(clusterConfig *virtconfig.ClusterConfig, client kubecli.KubevirtClient) *MigrationPolicyAdmitter {
	return &MigrationPolicyAdmitter{
		ClusterConfig: clusterConfig,
		Client:        client,
	}
}

// Admit validates an AdmissionReview
func (admitter *MigrationPolicyAdmitter) Admit(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	if ar.Request.Resource.Group != migrationsv1.MigrationPolicyKind.Group ||
		ar.Request.Resource.Resource != migrations.ResourceMigrationPolicies {
		return webhookutils.ToAdmissionResponseError(fmt.Errorf("unexpected resource %+v", ar.Request.Resource))
	}

	policy := &migrationsv1.MigrationPolicy{}
	err := json.Unmarshal(ar.Request.Object.Raw, policy)
	if err != nil {
		return webhookutils.ToAdmissionResponseError(err)
	}

	var causes []metav1.StatusCause

	sourceField := k8sfield.NewPath("spec")

	spec := policy.Spec
	if spec.CompletionTimeoutPerGiB != nil && *spec.CompletionTimeoutPerGiB < 0 {
		causes = append(causes, metav1.StatusCause{
			Type:    metav1.CauseTypeFieldValueInvalid,
			Message: "must not be negative",
			Field:   sourceField.Child("completionTimeoutPerGiB").String(),
		})
	}

	if spec.BandwidthPerMigration != nil {
		quantity, ok := spec.BandwidthPerMigration.AsInt64()
		if !ok {
			dec := spec.BandwidthPerMigration.AsDec()
			quantity = int64(dec.Sign())
		}

		if quantity < 0 {
			causes = append(causes, metav1.StatusCause{
				Type:    metav1.CauseTypeFieldValueInvalid,
				Message: "must not be negative",
				Field:   sourceField.Child("bandwidthPerMigration").String(),
			})
		}
	}

	if spec.AllowPostCopy != nil && *spec.AllowPostCopy {
		namespace, err := admitter.Client.CoreV1().Namespaces().Get(context.Background(), policy.Namespace, metav1.GetOptions{})
		if err != nil {
			return webhookutils.ToAdmissionResponseError(err)
		}

		if !admitter.ClusterConfig.PSASeccompAllowsUserfaultfd() && !psa.IsNamespacePrivileged(namespace) {
			causes = append(causes, metav1.StatusCause{
				Type:    metav1.CauseTypeFieldValueInvalid,
				Message: "PostCopy is not allowed if the namespace is unprivileged",
				Field:   sourceField.Child("allowPostCopy").String(),
			})
		}
	}

	if len(causes) > 0 {
		return webhookutils.ToAdmissionResponse(causes)
	}

	reviewResponse := admissionv1.AdmissionResponse{
		Allowed: true,
	}
	return &reviewResponse
}
