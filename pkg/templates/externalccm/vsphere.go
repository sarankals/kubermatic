/*
Copyright 2019 The KubeOne Authors.

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

package externalccm

import (
	"context"

	"github.com/pkg/errors"

	"github.com/kubermatic/kubeone/pkg/clientutil"
	"github.com/kubermatic/kubeone/pkg/state"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
)

const (
	vSphereImage          = "gcr.io/cloud-provider-vsphere/cpi/release/manager:v1.1.0"
	vSphereSAName         = "cloud-controller-manager"
	vSphereDeploymentName = "vsphere-cloud-controller-manager"
)

func ensurevSphere(s *state.State) error {
	if s.DynamicClient == nil {
		return errors.New("kubernetes client not initialized")
	}

	if s.Cluster.CloudProvider.CloudConfig == "" {
		return errors.New("cloudConfig not defined")
	}

	sa := vSphereServiceAccount()
	cr := vSphereClusterRole()
	k8sobject := []runtime.Object{
		sa,
		cr,
		genClusterRoleBinding("system:cloud-controller-manager", cr, sa),
		vSphereRoleBinding(sa),
		vSphereDaemonSet(sa),
	}

	ctx := context.Background()
	for _, obj := range k8sobject {
		if err := clientutil.CreateOrUpdate(ctx, s.DynamicClient, obj); err != nil {
			return errors.Wrapf(err, "failed to ensure vSphere CCM %T", obj)
		}
	}

	return nil
}

func vSphereServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vSphereSAName,
			Namespace: metav1.NamespaceSystem,
		},
	}
}

func vSphereClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:cloud-controller-manager",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create", "patch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"nodes/status"},
				Verbs:     []string{"patch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"services"},
				Verbs:     []string{"list", "patch", "update", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"serviceaccounts"},
				Verbs:     []string{"create", "get", "list", "watch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs:     []string{"get", "list", "update", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"endpoints"},
				Verbs:     []string{"create", "get", "list", "watch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

func vSphereRoleBinding(sa *corev1.ServiceAccount) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "servicecatalog.k8s.io:apiserver-authentication-reader",
			Namespace: metav1.NamespaceSystem,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     "extension-apiserver-authentication-reader",
		},
		Subjects: []rbacv1.Subject{
			{
				APIGroup: corev1.GroupName,
				Kind:     sa.Kind,
				Name:     sa.Name,
			},
		},
	}
}

func vSphereDaemonSet(sa *corev1.ServiceAccount) *appsv1.DaemonSet {
	labels := map[string]string{
		"k8s-app": "vsphere-cloud-controller-manager",
	}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vSphereDeploymentName,
			Namespace: metav1.NamespaceSystem,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						"node-role.kubernetes.io/master": "",
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser: pointer.Int64Ptr(0),
					},
					Tolerations: []corev1.Toleration{
						{
							Key:    "node-role.kubernetes.io/master",
							Effect: corev1.TaintEffectNoSchedule,
						},
						{
							Key:    "node.cloudprovider.kubernetes.io/uninitialized",
							Value:  "true",
							Effect: corev1.TaintEffectNoSchedule,
						},
					},
					ServiceAccountName: vSphereSAName,
					Containers: []corev1.Container{
						{
							Name:  "vsphere-cloud-controller-manager",
							Image: vSphereImage,
							Args: []string{
								"--v=2",
								"--cloud-provider=vsphere",
								"--cloud-config=/etc/cloud/vsphere.conf",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/etc/cloud",
									Name:      "vsphere-config-volume",
									ReadOnly:  true,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("200m"),
								},
							},
						},
					},
					// HostNetwork: true,
					Volumes: []corev1.Volume{
						{
							Name: "vsphere-config-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									// TODO
								},
							},
						},
					},
				},
			},
		},
	}
}
