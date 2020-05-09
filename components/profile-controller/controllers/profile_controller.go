/*
Copyright 2019 The Kubeflow Authors.

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

package controllers

import (
	"context"
	"fmt"
	"github.com/ghodss/yaml"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	istiorbac "github.com/kubeflow/kubeflow/components/profile-controller/api/istiorbac/v1alpha1"
	profilev1beta1 "github.com/kubeflow/kubeflow/components/profile-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const SERVICEROLEISTIO = "ns-access-istio"
const SERVICEROLEBINDINGISTIO = "owner-binding-istio"

const KFQUOTA = "kf-resource-quota"
const PROFILEFINALIZER = "profile-finalizer"

// annotation key, consumed by kfam API
const USER = "user"
const ROLE = "role"
const ADMIN = "admin"

// Kubeflow default role names
// TODO: Make kubeflow roles configurable (krishnadurai)
// This will enable customization of roles.
const (
	kubeflowAdmin              = "kubeflow-admin"
	kubeflowEdit               = "kubeflow-edit"
	kubeflowView               = "kubeflow-view"
	istioInjectionLabel        = "istio-injection"
	katibMetricsCollectorLabel = "katib-metricscollector-injection"
)

const DEFAULT_EDITOR = "default-editor"
const DEFAULT_VIEWER = "default-viewer"

type Plugin interface {
	// Called when profile CR is created / updated
	ApplyPlugin(*ProfileReconciler, *profilev1beta1.Profile) error
	// Called when profile CR is being deleted, to cleanup any non-k8s resources created via ApplyPlugin
	// RevokePlugin logic need to be IDEMPOTENT
	RevokePlugin(*ProfileReconciler, *profilev1beta1.Profile) error
}

// ProfileReconciler reconciles a Profile object
type ProfileReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Log              logr.Logger
	UserIdHeader     string
	UserIdPrefix     string
	WorkloadIdentity string
}

// Reconcile reads that state of the cluster for a Profile object and makes changes based on the state read
// and what is in the Profile.Spec
// Automatically generate RBAC rules to allow the Controller to read and write Deployments
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=serviceaccount,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubeflow.org,resources=profiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubeflow.org,resources=profiles/status,verbs=get;update;patch
func (r *ProfileReconciler) Reconcile(request ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	logger := r.Log.WithValues("profile", request.NamespacedName)

	// Fetch the Profile instance
	instance := &profilev1beta1.Profile{}
	logger.Info("Start to Reconcile.", "namespace", request.Namespace, "name", request.Name)
	err := r.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Update namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{"owner": instance.Spec.Owner.Name},
			// inject istio sidecar to all pods in target namespace by default.
			Labels: map[string]string{
				istioInjectionLabel:        "enabled",
				katibMetricsCollectorLabel: "enabled",
			},
			Name: instance.Name,
		},
	}
	if err := controllerutil.SetControllerReference(instance, ns, r.Scheme); err != nil {
		return reconcile.Result{}, err
	}
	foundNs := &corev1.Namespace{}
	err = r.Get(ctx, types.NamespacedName{Name: ns.Name}, foundNs)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Namespace: " + ns.Name)
			err = r.Create(ctx, ns)
			if err != nil {
				return reconcile.Result{}, err
			}
			// wait 15 seconds for new namespace creation.
			err = backoff.Retry(
				func() error {
					return r.Get(ctx, types.NamespacedName{Name: ns.Name}, foundNs)
				},
				backoff.WithMaxRetries(backoff.NewConstantBackOff(3*time.Second), 5))
			if err != nil {
				return r.appendErrorConditionAndReturn(ctx, instance,
					"Owning namespace failed to create within 15 seconds")
			}
			logger.Info("Created Namespace: "+foundNs.Name, "status", foundNs.Status.Phase)
		} else {
			return reconcile.Result{}, err
		}
	} else {
		// Check exising namespace ownership before move forward
		owner, ok := foundNs.Annotations["owner"]
		if ok && owner == instance.Spec.Owner.Name {
			if _, ok = foundNs.Labels[katibMetricsCollectorLabel]; !ok {
				foundNs.Labels[katibMetricsCollectorLabel] = "enabled"
				err = r.Update(ctx, foundNs)
				if err != nil {
					return reconcile.Result{}, err
				}
			}
		} else {
			logger.Info(fmt.Sprintf("namespace already exist, but not owned by profile creator %v",
				instance.Spec.Owner.Name))
			return r.appendErrorConditionAndReturn(ctx, instance, fmt.Sprintf(
				"namespace already exist, but not owned by profile creator %v", instance.Spec.Owner.Name))
		}
	}

	// Update Istio Rbac
	// Create Istio ServiceRole and ServiceRoleBinding in target namespace; which will give ns owner permission to access services in ns.
	if err = r.updateIstioRbac(instance); err != nil {
		logger.Info("Failed Updating Istio rbac permission", "namespace", instance.Name, "error", err)
		return reconcile.Result{}, err
	}

	// Update service accounts
	// Create service account "default-editor" in target namespace.
	// "default-editor" would have kubeflowEdit permission: edit all resources in target namespace except rbac.
	if err = r.updateServiceAccount(instance, DEFAULT_EDITOR, kubeflowEdit); err != nil {
		logger.Info("Failed Updating ServiceAccount", "namespace", instance.Name, "name",
			"defaultEdittor", "error", err)
		return reconcile.Result{}, err
	}
	// Create service account "default-viewer" in target namespace.
	// "default-viewer" would have k8s default "view" permission: view all resources in target namespace.
	if err = r.updateServiceAccount(instance, DEFAULT_VIEWER, kubeflowView); err != nil {
		logger.Info("Failed Updating ServiceAccount", "namespace", instance.Name, "name",
			"defaultViewer", "error", err)
		return reconcile.Result{}, err
	}

	// TODO: add role for impersonate permission

	// Update owner rbac permission
	// When ClusterRole was referred by namespaced roleBinding, the result permission will be namespaced as well.
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{USER: instance.Spec.Owner.Name, ROLE: ADMIN},
			Name:        "namespaceAdmin",
			Namespace:   instance.Name,
		},
		// Use default ClusterRole 'admin' for profile/namespace owner
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     kubeflowAdmin,
		},
		Subjects: []rbacv1.Subject{
			instance.Spec.Owner,
		},
	}
	if err = r.updateRoleBinding(instance, roleBinding); err != nil {
		logger.Info("Failed Updating Owner Rolebinding", "namespace", instance.Name, "name",
			"defaultEdittor", "error", err)
		return reconcile.Result{}, err
	}
	// Create resource quota for target namespace if resources are specified in profile.
	if len(instance.Spec.ResourceQuotaSpec.Hard) > 0 {
		resourceQuota := &corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{
				Name:      KFQUOTA,
				Namespace: instance.Name,
			},
			Spec: instance.Spec.ResourceQuotaSpec,
		}
		if err = r.updateResourceQuota(instance, resourceQuota); err != nil {
			logger.Info("Failed Updating resource quota", "namespace", instance.Name, "error", err)
			return reconcile.Result{}, err
		}
	} else {
		logger.Info("No update on resource quota", "spec", instance.Spec.ResourceQuotaSpec.String())
	}
	if err := r.PatchDefaultPluginSpec(ctx, instance); err != nil {
		return reconcile.Result{}, err
	}
	if plugins, err := r.GetPluginSpec(instance); err == nil {
		for _, plugin := range plugins {
			if err := plugin.ApplyPlugin(r, instance); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(instance.ObjectMeta.Finalizers, PROFILEFINALIZER) {
			instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, PROFILEFINALIZER)
			if err := r.Update(ctx, instance); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(instance.ObjectMeta.Finalizers, PROFILEFINALIZER) {
			// our finalizer is present, so lets revoke all Plugins to clean up any external dependencies
			if plugins, err := r.GetPluginSpec(instance); err == nil {
				for _, plugin := range plugins {
					if err := plugin.RevokePlugin(r, instance); err != nil {
						return reconcile.Result{}, err
					}
				}
			}

			// remove our finalizer from the list and update it.
			instance.ObjectMeta.Finalizers = removeString(instance.ObjectMeta.Finalizers, PROFILEFINALIZER)
			if err := r.Update(ctx, instance); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// appendErrorConditionAndReturn append failure status to profile CR and mark Reconcile done. If update condition failed, request will be requeued.
func (r *ProfileReconciler) appendErrorConditionAndReturn(ctx context.Context, instance *profilev1beta1.Profile,
	message string) (ctrl.Result, error) {
	instance.Status.Conditions = append(instance.Status.Conditions, profilev1beta1.ProfileCondition{
		Type:    profilev1beta1.ProfileFailed,
		Message: message,
	})
	if err := r.Update(ctx, instance); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *ProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&profilev1beta1.Profile{}).
		Owns(&istiorbac.ServiceRole{}).
		Owns(&istiorbac.ServiceRoleBinding{}).
		Owns(&corev1.Namespace{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.RoleBinding{}).
		Complete(r)
}

// updateIstioRbac create or update Istio rbac resources in target namespace owned by "profileIns". The goal is to allow service access for profile owner
func (r *ProfileReconciler) updateIstioRbac(profileIns *profilev1beta1.Profile) error {
	logger := r.Log.WithValues("profile", profileIns.Name)
	istioServiceRole := &istiorbac.ServiceRole{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{USER: profileIns.Spec.Owner.Name, ROLE: ADMIN},
			Name:        SERVICEROLEISTIO,
			Namespace:   profileIns.Name,
		},
		Spec: istiorbac.ServiceRoleSpec{
			Rules: []*istiorbac.AccessRule{
				{
					Services: []string{"*"},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(profileIns, istioServiceRole, r.Scheme); err != nil {
		return err
	}
	foundSr := &istiorbac.ServiceRole{}
	err := r.Get(context.TODO(), types.NamespacedName{Name: istioServiceRole.Name,
		Namespace: istioServiceRole.Namespace}, foundSr)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Istio ServiceRole", "namespace", istioServiceRole.Namespace,
				"name", istioServiceRole.Name)
			err = r.Create(context.TODO(), istioServiceRole)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		if !reflect.DeepEqual(istioServiceRole.Spec, foundSr.Spec) {
			foundSr.Spec = istioServiceRole.Spec
			logger.Info("Updating Istio ServiceRole", "namespace", istioServiceRole.Namespace,
				"name", istioServiceRole.Name)
			err = r.Update(context.TODO(), foundSr)
			if err != nil {
				return err
			}
		}
	}
	istioServiceRoleBinding := &istiorbac.ServiceRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{USER: profileIns.Spec.Owner.Name, ROLE: ADMIN},
			Name:        SERVICEROLEBINDINGISTIO,
			Namespace:   profileIns.Name,
		},
		Spec: istiorbac.ServiceRoleBindingSpec{
			Subjects: []*istiorbac.Subject{
				{
					Properties: map[string]string{fmt.Sprintf("request.headers[%v]", r.UserIdHeader): r.UserIdPrefix + profileIns.Spec.Owner.Name},
				},
			},
			RoleRef: &istiorbac.RoleRef{
				Kind: "ServiceRole",
				Name: SERVICEROLEISTIO,
			},
		},
	}
	if err := controllerutil.SetControllerReference(profileIns, istioServiceRoleBinding, r.Scheme); err != nil {
		return err
	}
	foundSrb := &istiorbac.ServiceRoleBinding{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: istioServiceRoleBinding.Name,
		Namespace: istioServiceRoleBinding.Namespace}, foundSrb)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Istio ServiceRoleBinding", "namespace", istioServiceRoleBinding.Namespace,
				"name", istioServiceRoleBinding.Name)
			err = r.Create(context.TODO(), istioServiceRoleBinding)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		if !reflect.DeepEqual(istioServiceRoleBinding.Spec, foundSrb.Spec) {
			foundSrb.Spec = istioServiceRoleBinding.Spec
			logger.Info("Updating Istio ServiceRoleBinding", "namespace", istioServiceRoleBinding.Namespace,
				"name", istioServiceRoleBinding.Name)
			err = r.Update(context.TODO(), foundSrb)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// updateServiceAccount create or update service account "saName" with role "ClusterRoleName" in target namespace owned by "profileIns"
func (r *ProfileReconciler) updateResourceQuota(profileIns *profilev1beta1.Profile,
	resourceQuota *corev1.ResourceQuota) error {
	ctx := context.Background()
	logger := r.Log.WithValues("profile", profileIns.Name)
	if err := controllerutil.SetControllerReference(profileIns, resourceQuota, r.Scheme); err != nil {
		return err
	}
	found := &corev1.ResourceQuota{}
	err := r.Get(ctx, types.NamespacedName{Name: resourceQuota.Name, Namespace: resourceQuota.Namespace}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating ResourceQuota", "namespace", resourceQuota.Namespace, "name", resourceQuota.Name)
			err = r.Create(ctx, resourceQuota)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		if !(reflect.DeepEqual(resourceQuota.Spec, found.Spec)) {
			found.Spec = resourceQuota.Spec
			logger.Info("Updating ResourceQuota", "namespace", resourceQuota.Namespace, "name", resourceQuota.Name)
			err = r.Update(ctx, found)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// updateServiceAccount create or update service account "saName" with role "ClusterRoleName" in target namespace owned by "profileIns"
func (r *ProfileReconciler) updateServiceAccount(profileIns *profilev1beta1.Profile, saName string,
	ClusterRoleName string) error {
	logger := r.Log.WithValues("profile", profileIns.Name)
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: profileIns.Name,
		},
	}
	if err := controllerutil.SetControllerReference(profileIns, serviceAccount, r.Scheme); err != nil {
		return err
	}
	found := &corev1.ServiceAccount{}
	err := r.Get(context.TODO(), types.NamespacedName{Name: serviceAccount.Name, Namespace: serviceAccount.Namespace}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating ServiceAccount", "namespace", serviceAccount.Namespace,
				"name", serviceAccount.Name)
			err = r.Create(context.TODO(), serviceAccount)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: profileIns.Name,
		},
		// Use default ClusterRole 'admin' for profile/namespace owner
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     ClusterRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      saName,
				Namespace: profileIns.Name,
			},
		},
	}
	return r.updateRoleBinding(profileIns, roleBinding)
}

// updateRoleBinding create or update roleBinding "roleBinding" in target namespace owned by "profileIns"
func (r *ProfileReconciler) updateRoleBinding(profileIns *profilev1beta1.Profile,
	roleBinding *rbacv1.RoleBinding) error {
	logger := r.Log.WithValues("profile", profileIns.Name)
	if err := controllerutil.SetControllerReference(profileIns, roleBinding, r.Scheme); err != nil {
		return err
	}
	found := &rbacv1.RoleBinding{}
	err := r.Get(context.TODO(), types.NamespacedName{Name: roleBinding.Name, Namespace: roleBinding.Namespace}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating RoleBinding", "namespace", roleBinding.Namespace, "name", roleBinding.Name)
			err = r.Create(context.TODO(), roleBinding)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		if !(reflect.DeepEqual(roleBinding.RoleRef, found.RoleRef) && reflect.DeepEqual(roleBinding.Subjects, found.Subjects)) {
			found.RoleRef = roleBinding.RoleRef
			found.Subjects = roleBinding.Subjects
			logger.Info("Updating RoleBinding", "namespace", roleBinding.Namespace, "name", roleBinding.Name)
			err = r.Update(context.TODO(), found)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// GetPluginSpec will try to unmarshal the plugin spec inside profile for the specified plugin
// Returns an error if the plugin isn't defined or if there is a problem
func (r *ProfileReconciler) GetPluginSpec(profileIns *profilev1beta1.Profile) ([]Plugin, error) {
	logger := r.Log.WithValues("profile", profileIns.Name)
	plugins := []Plugin{}
	for _, p := range profileIns.Spec.Plugins {
		var pluginIns Plugin
		switch p.Kind {
		case KIND_WORKLOAD_IDENTITY:
			pluginIns = &GcpWorkloadIdentity{}
		default:
			logger.Info("Plugin not recgonized: ", "Kind", p.Kind)
			continue
		}

		// To deserialize it to a specific type we need to first serialize it to bytes
		// and then unserialize it.
		specBytes, err := yaml.Marshal(p.Spec)

		if err != nil {
			logger.Info("Could not marshal plugin ", p.Kind, "; error: ", err)
			return nil, err
		}

		err = yaml.Unmarshal(specBytes, pluginIns)
		if err != nil {
			logger.Info("Could not unmarshal plugin ", p.Kind, "; error: ", err)
			return nil, err
		}
		plugins = append(plugins, pluginIns)
	}
	return plugins, nil
}

// PatchDefaultPluginSpec patch default plugins to profile CR instance if user doesn't specify plugin of same kind in CR.
func (r *ProfileReconciler) PatchDefaultPluginSpec(ctx context.Context, profileIns *profilev1beta1.Profile) error {
	// read existing plugins into map
	plugins := make(map[string]profilev1beta1.Plugin)
	for _, p := range profileIns.Spec.Plugins {
		plugins[p.Kind] = p
	}
	// Patch default plugins if same kind doesn't exist yet.
	if r.WorkloadIdentity != "" {
		if _, ok := plugins[KIND_WORKLOAD_IDENTITY]; !ok {
			profileIns.Spec.Plugins = append(profileIns.Spec.Plugins, profilev1beta1.Plugin{
				TypeMeta: metav1.TypeMeta{
					Kind: KIND_WORKLOAD_IDENTITY,
				},
				Spec: &runtime.RawExtension{
					Raw: []byte(fmt.Sprintf(`{"GcpServiceAccount": "%v"}`, r.WorkloadIdentity)),
				},
			})
		}
	}
	if err := r.Update(ctx, profileIns); err != nil {
		return err
	}
	return nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}
