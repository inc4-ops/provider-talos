// Copyright 2026 inc4
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/crossplane/upjet/v2/pkg/controller"

	cluster "github.com/inc4-ops/provider-talos/internal/controller/namespaced/cluster/cluster"
	kubeconfig "github.com/inc4-ops/provider-talos/internal/controller/namespaced/cluster/kubeconfig"
	factoryschematic "github.com/inc4-ops/provider-talos/internal/controller/namespaced/image/factoryschematic"
	configurationapply "github.com/inc4-ops/provider-talos/internal/controller/namespaced/machine/configurationapply"
	providerconfig "github.com/inc4-ops/provider-talos/internal/controller/namespaced/providerconfig"
)

// Setup creates all controllers with the supplied logger and adds them to
// the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		cluster.Setup,
		kubeconfig.Setup,
		factoryschematic.Setup,
		configurationapply.Setup,
		providerconfig.Setup,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}

// SetupGated creates all controllers with the supplied logger and adds them to
// the supplied manager gated.
func SetupGated(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		cluster.SetupGated,
		kubeconfig.SetupGated,
		factoryschematic.SetupGated,
		configurationapply.SetupGated,
		providerconfig.SetupGated,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}

// SetupWebhookWithManager registers conversion webhooks for all resource kinds in the group.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	for _, setup := range []func(ctrl.Manager) error{
		cluster.SetupWebhookWithManager,
		kubeconfig.SetupWebhookWithManager,
		factoryschematic.SetupWebhookWithManager,
		configurationapply.SetupWebhookWithManager,
		providerconfig.SetupWebhookWithManager,
	} {
		if err := setup(mgr); err != nil {
			return err
		}
	}
	return nil
}
