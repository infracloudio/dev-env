/*
Copyright 2019 Suraj Banakar.

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

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	devv1alpha1 "devenv-controller/api/v1alpha1"

	crossplanemetav1 "github.com/crossplaneio/crossplane-runtime/pkg/meta"

	crossplaneruntime "github.com/crossplaneio/crossplane-runtime/apis/core/v1alpha1"
	computev1alpha1 "github.com/crossplaneio/crossplane/apis/compute/v1alpha1"
	crossplanegcpv1alpha1 "github.com/crossplaneio/stack-gcp/apis/container/v1alpha1"
	crossplanegcpv1beta1 "github.com/crossplaneio/stack-gcp/apis/container/v1beta1"
	argocdapplicationv1alpha1 "github.com/kanuahs/argo-cd/pkg/apis/application/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

// EnvironmentReconciler reconciles a Environment object
type EnvironmentReconciler struct {
	client.Client
	Log                 logr.Logger
	Scheme              *runtime.Scheme
	CrossplaneNamespace string
	ArgoCDNamespace     string
}

const (
	// ClassNameLabel is a label key used to dynamically match the cluster class
	ClassNameLabel        = "className"
	ClusterClaimFinalizer = "dev-environment/finalizers.clusterclaim.vadasambar.github.io"
	GCPNodePoolFinalizer  = "dev-environment/finalizers.gcpnodepool.vadasambar.github.io"
	EnvironmentFinalizer  = "dev-environment/finalizers.environment.vadasambar.github.io"
)

// +kubebuilder:rbac:groups=dev.vadasambar.github.io,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dev.vadasambar.github.io,resources=environments/status,verbs=get;update;patch

func (r *EnvironmentReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("environment", req.NamespacedName)

	env := &devv1alpha1.Environment{}
	if err := r.Client.Get(context.Background(), req.NamespacedName, env); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "could not get environment", "instance", "EnvironmentController")
		return ctrl.Result{Requeue: true}, err
	}
	r.Log.Info("environment object", "env", env)

	k8class, fetchClassErr := r.fetchClusterClass(env)
	if fetchClassErr != nil {
		r.Log.Error(fetchClassErr, "could not get cluster class referenced in the environment", "cluster-class",
			env.Spec.ClusterClassRef,
			"namespace", r.CrossplaneNamespace)
		return ctrl.Result{Requeue: true}, fetchClassErr
	}

	createdk8Cluster := &computev1alpha1.KubernetesCluster{}
	createdk8ClusterNamespacedName := types.NamespacedName{
		Name:      env.Spec.ClusterName,
		Namespace: r.CrossplaneNamespace,
	}

	getClusterErr := r.Client.Get(context.Background(), createdk8ClusterNamespacedName, createdk8Cluster)
	if getClusterErr != nil && kerrors.IsNotFound(getClusterErr) {
		var createClusterErr error
		createdk8Cluster, createClusterErr = r.createClusterClaim(env)

		if createClusterErr != nil {
			r.Log.Error(createClusterErr, "could not get created kubernetes clusterclaim", "cluster-class", k8class.GetName())
			return ctrl.Result{Requeue: true}, createClusterErr
		}
	}

	if fetchErr := r.fetchApp(env.Spec.Source.Name); fetchErr != nil && kerrors.IsNotFound(fetchErr) {
		r.Log.Info("creating argocd source application", "source", env.Spec.Source.Name)
		app, createAppErr := r.createArgoCDApp(env, r.getSourceApp(env))
		if createAppErr != nil {
			return ctrl.Result{Requeue: true}, createAppErr
		}
		r.Log.Info("created argocd source application", "source", env.Spec.Source.Name, "application", app)

	}

	for _, dependency := range env.Spec.Dependencies {
		if fetchErr := r.fetchApp(dependency.Name); fetchErr != nil && kerrors.IsNotFound(fetchErr) {
			r.Log.Info("creating argocd dependency application", "dependency", dependency.Name)
			app, createAppErr := r.createArgoCDApp(env, r.getDependencyApp(&dependency, env.Spec.ClusterName))
			if createAppErr != nil {
				return ctrl.Result{Requeue: true}, createAppErr
			}
			r.Log.Info("created argocd dependency application", "dependency", dependency.Name, "application", app)

		}
	}

	var managedResourceName string
	if createdk8Cluster.Spec.ResourceReference != nil {
		managedResourceName = createdk8Cluster.Spec.ResourceReference.Name
	} else {
		return ctrl.Result{Requeue: true}, nil
	}

	gkeNodepool := &crossplanegcpv1alpha1.NodePool{}

	gkeNodepoolNamespacedName := types.NamespacedName{
		Name: env.Spec.ClusterName,
	}
	getNodepoolErr := r.Client.Get(context.Background(), gkeNodepoolNamespacedName, gkeNodepool)
	if getNodepoolErr != nil && kerrors.IsNotFound(getNodepoolErr) {
		r.Log.Info("creating nodepool")
		createNodepoolErr := r.createNodePools(env, k8class, managedResourceName)
		if createNodepoolErr != nil {
			r.Log.Error(createNodepoolErr, "could not create nodepool for the cluster", "nodepool name", env.Spec.ClusterName, "cluster name", env.Spec.ClusterName)
			return ctrl.Result{Requeue: true}, createNodepoolErr
		}
		r.Log.Info("created nodepool")
	}

	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) fetchApp(name string) error {
	argoCDApplication := &argocdapplicationv1alpha1.Application{}
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: r.ArgoCDNamespace, Name: name}, argoCDApplication); err != nil {
		return err
	}

	return nil
}

func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&devv1alpha1.Environment{}).
		Complete(r)
}

func (r *EnvironmentReconciler) fetchClusterClass(env *devv1alpha1.Environment) (*crossplanegcpv1beta1.GKEClusterClass, error) {
	k8class := &crossplanegcpv1beta1.GKEClusterClass{}
	k8classNamespacedName := types.NamespacedName{
		Name: env.Spec.ClusterClassRef,
	}
	err := r.Client.Get(context.Background(), k8classNamespacedName, k8class)
	if err != nil {
		return nil, err
	}

	return k8class, nil
}

func (r *EnvironmentReconciler) createArgoCDApp(env *devv1alpha1.Environment, argocdApplication *argocdapplicationv1alpha1.Application) (*argocdapplicationv1alpha1.Application, error) {
	r.Log.Info("creating argocd application")

	if err := ctrl.SetControllerReference(env, argocdApplication, r.Scheme); err != nil {
		r.Log.Error(err, "failed to set owner reference on argocd application", "application", argocdApplication.GetName(), "namespace", r.ArgoCDNamespace)
		return nil, err
	}

	if err := r.Client.Create(context.Background(), argocdApplication); err != nil {
		r.Log.Error(err, "could not create argocd application", "application", argocdApplication.GetName(), "namespace", r.ArgoCDNamespace)
		return nil, err
	}

	createdArgoCDApp := &argocdapplicationv1alpha1.Application{}
	if err := r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: r.ArgoCDNamespace, Name: argocdApplication.GetName()},
		createdArgoCDApp); err != nil {
		r.Log.Error(err, "could not get created argocd application", "application", argocdApplication.GetName(), "namespace", r.ArgoCDNamespace)
		return nil, err
	}

	r.Log.Info("created argocd application")

	return createdArgoCDApp, nil
}

func (r *EnvironmentReconciler) getSourceApp(env *devv1alpha1.Environment) *argocdapplicationv1alpha1.Application {
	argocdApplication := &argocdapplicationv1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      env.Spec.Source.Name,
			Namespace: r.ArgoCDNamespace,
		},
		Spec: argocdapplicationv1alpha1.ApplicationSpec{
			Source: argocdapplicationv1alpha1.ApplicationSource{
				RepoURL:        env.Spec.Source.RepoURL,
				Path:           env.Spec.Source.Path,
				TargetRevision: env.Spec.Source.Revision,
			},
			Destination: argocdapplicationv1alpha1.ApplicationDestination{
				Namespace: env.Spec.Source.Namespace,
				Name:      env.Spec.ClusterName,
			},
			Project: "default",
			SyncPolicy: &argocdapplicationv1alpha1.SyncPolicy{
				Automated: &argocdapplicationv1alpha1.SyncPolicyAutomated{
					Prune:    true,
					SelfHeal: true,
				},
			},
		},
	}
	return argocdApplication
}

func (r *EnvironmentReconciler) getDependencyApp(dependency *devv1alpha1.DependencySrc, clusterName string) *argocdapplicationv1alpha1.Application {
	argocdApplication := &argocdapplicationv1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dependency.Name,
			Namespace: r.ArgoCDNamespace,
		},
		Spec: argocdapplicationv1alpha1.ApplicationSpec{
			Source: argocdapplicationv1alpha1.ApplicationSource{
				RepoURL:        dependency.RepoURL,
				Chart:          dependency.ChartName,
				TargetRevision: dependency.Revision,
			},
			Destination: argocdapplicationv1alpha1.ApplicationDestination{
				Namespace: dependency.Namespace,
				Name:      clusterName,
			},
			Project: "default",
			SyncPolicy: &argocdapplicationv1alpha1.SyncPolicy{
				Automated: &argocdapplicationv1alpha1.SyncPolicyAutomated{
					Prune:    true,
					SelfHeal: true,
				},
			},
		},
	}
	return argocdApplication
}

func (r *EnvironmentReconciler) createClusterClaim(env *devv1alpha1.Environment) (*computev1alpha1.KubernetesCluster, error) {
	r.Log.Info("creating kubernetes cluster claim", "cluster-name", env.Spec.ClusterName)
	newk8cluster := &computev1alpha1.KubernetesCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      env.Spec.ClusterName,
			Namespace: r.CrossplaneNamespace,
			Annotations: map[string]string{
				crossplanemetav1.ExternalNameAnnotationKey: env.Spec.ClusterName,
			},
		},
		Spec: computev1alpha1.KubernetesClusterSpec{
			ResourceClaimSpec: crossplaneruntime.ResourceClaimSpec{
				ClassSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						ClassNameLabel: env.Spec.ClusterClassRef,
					},
				},
				WriteConnectionSecretToReference: &crossplaneruntime.LocalSecretReference{
					Name: env.Spec.ClusterName,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(env, newk8cluster, r.Scheme); err != nil {
		r.Log.Error(err, "failed to set owner reference")
		return nil, err
	}

	if err := r.Client.Create(context.Background(), newk8cluster); err != nil && !kerrors.IsAlreadyExists(err) {
		r.Log.Error(err, "could not create kubernetescluster", "instance", "EnvironmentController")
		return nil, err
	}
	r.Log.Info("created kubernetes cluster claim", "obj", newk8cluster)

	createdk8Cluster := &computev1alpha1.KubernetesCluster{}
	createdk8ClusterNamespacedName := types.NamespacedName{
		Name:      env.Spec.ClusterName,
		Namespace: r.CrossplaneNamespace,
	}
	if err := r.Client.Get(context.Background(), createdk8ClusterNamespacedName, createdk8Cluster); err != nil {
		r.Log.Error(err, "could not get the cluster claim", "cluster claim", createdk8ClusterNamespacedName.Name, "namespace", createdk8ClusterNamespacedName.Namespace)
		return nil, err
	}

	return createdk8Cluster, nil
}

func (r *EnvironmentReconciler) createNodePools(env *devv1alpha1.Environment, k8class *crossplanegcpv1beta1.GKEClusterClass, managedResourceName string) error {

	// Note: Nodepools should be a part of cluster class but it hasn't been integrated with cluster class yet
	initialNodeCount := int64(2)
	nodePool := &crossplanegcpv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      env.Spec.ClusterName,
			Namespace: r.CrossplaneNamespace,
		},
		Spec: crossplanegcpv1alpha1.NodePoolSpec{
			ResourceSpec: crossplaneruntime.ResourceSpec{
				ProviderReference: &corev1.ObjectReference{
					Name: k8class.SpecTemplate.ProviderReference.Name,
				},
				WriteConnectionSecretToReference: &crossplaneruntime.SecretReference{
					Name:      fmt.Sprintf("%s-nodepool", env.Spec.ClusterName),
					Namespace: r.CrossplaneNamespace,
				},
			},

			ForProvider: crossplanegcpv1alpha1.NodePoolParameters{
				ClusterRef: &crossplanegcpv1alpha1.GKEClusterURIReferencerForNodePool{
					GKEClusterURIReferencer: crossplanegcpv1beta1.GKEClusterURIReferencer{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: managedResourceName,
						},
					},
				},
				InitialNodeCount: &initialNodeCount,
			},
		},
	}
	if err := ctrl.SetControllerReference(env, nodePool, r.Scheme); err != nil {
		r.Log.Error(err, "could not set owner reference on gke nodepool")
		return err
	}

	if err := r.Client.Create(context.Background(), nodePool); err != nil {
		return err
	}
	return nil
}
