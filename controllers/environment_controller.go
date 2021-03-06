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
	"strconv"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	devv1alpha1 "devenv-controller/api/v1alpha1"

	crossplanemetav1 "github.com/crossplane/crossplane-runtime/pkg/meta"

	crossplaneruntime "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	computev1alpha1 "github.com/crossplane/crossplane/apis/compute/v1alpha1"
	crossplanegcpv1alpha1 "github.com/crossplane/provider-gcp/apis/container/v1alpha1"
	crossplanegcpv1beta1 "github.com/crossplane/provider-gcp/apis/container/v1beta1"
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

	if env.Spec.TTL != "" && r.areArgoCDAppDependenciesReady(env) && r.isClusterBound(env) && r.isArgoCDAppReady(env.Spec.Source.Name) {
		if env.Status.TTLStartTimestamp.IsZero() {
			now := metav1.Now()
			env.Status.TTLStartTimestamp = &now
			ttlTimeStampUpdationErr := r.Status().Update(context.Background(), env)
			if ttlTimeStampUpdationErr != nil {
				r.Log.Error(ttlTimeStampUpdationErr, "could not update ttlStartTimestamp", "env", env)
				return ctrl.Result{Requeue: true}, ttlTimeStampUpdationErr
			}
		} else {
			var ttl time.Duration
			ttlDurationStr := env.Spec.TTL[:len(env.Spec.TTL)-1]
			ttlDuration, _ := strconv.Atoi(ttlDurationStr)
			ttlUnit := env.Spec.TTL[len(env.Spec.TTL)-1 : len(env.Spec.TTL)]
			switch ttlUnit {
			case "m":
				ttl = time.Duration(ttlDuration) * time.Minute
			case "h":
				ttl = time.Duration(ttlDuration) * time.Hour
			case "d":
				ttl = time.Duration(ttlDuration) * time.Hour * 24
			case "y":
				ttl = time.Duration(ttlDuration) * time.Hour * 24 * 365
			}

			if time.Now().UTC().After(env.Status.TTLStartTimestamp.Add(ttl)) {
				r.Log.Info(fmt.Sprintf("cluster '%s' exceeded TTL of %s (%s - %s)", env.Spec.ClusterName, env.Spec.TTL, env.Status.TTLStartTimestamp, metav1.Now()))
				r.Log.Info("deleting the cluster")
				deleteErr := r.Delete(context.Background(), env)
				if deleteErr != nil && !kerrors.IsNotFound(deleteErr) {
					r.Log.Error(deleteErr, "could not delete the environment even after exceeding TTL")
					return ctrl.Result{Requeue: true}, deleteErr
				}

			}

		}

	}

	k8class, fetchClassErr := r.fetchClusterClass(env)
	if fetchClassErr != nil {
		r.Log.Error(fetchClassErr, "could not get cluster class referenced in the environment", "cluster-class",
			env.Spec.ClusterClassLabel,
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

	return r.updateStatus(env)
}

func (r *EnvironmentReconciler) updateStatus(env *devv1alpha1.Environment) (ctrl.Result, error) {

	if r.isClusterBound(env) && r.isArgoCDAppReady(env.Spec.Source.Name) && r.areArgoCDAppDependenciesReady(env) {
		env.Status.Ready = true
		if err := r.Status().Update(context.Background(), env); err != nil {
			r.Log.Error(err, "could not update `Status` of env", "object", env)
			return ctrl.Result{RequeueAfter: time.Second * 10}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 10}, nil
	}

	env.Status.Ready = false
	env.Status.TTLStartTimestamp = nil
	r.Log.Info("status before updating", "env.Status.TTLStartTimestamp", env.Status)
	if err := r.Status().Update(context.Background(), env); err != nil {
		r.Log.Error(err, "could not update `Status` of env", "object", env)
	}

	return ctrl.Result{RequeueAfter: time.Second * 10}, nil
}

func (r *EnvironmentReconciler) areArgoCDAppDependenciesReady(env *devv1alpha1.Environment) bool {
	argocdDependenciesReady := true
	for _, dependency := range env.Spec.Dependencies {
		if argocdDependenciesReady == false {
			return argocdDependenciesReady
		}

		if r.isArgoCDAppReady(dependency.Name) {
			argocdDependenciesReady = argocdDependenciesReady && true
		}
	}

	return argocdDependenciesReady
}

func (r *EnvironmentReconciler) isEverythingReady(env *devv1alpha1.Environment) bool {
	argocdDependenciesReady := true
	for _, dependency := range env.Spec.Dependencies {
		if argocdDependenciesReady == false {
			break
		}

		if r.isArgoCDAppReady(dependency.Name) {
			argocdDependenciesReady = argocdDependenciesReady && true
		}
	}

	if r.isClusterBound(env) && r.isArgoCDAppReady(env.Spec.Source.Name) && argocdDependenciesReady {
		return true
	}

	return false
}

func (r *EnvironmentReconciler) isClusterBound(env *devv1alpha1.Environment) bool {
	kubernetesCluster := &computev1alpha1.KubernetesCluster{}
	var err error
	if err = r.Client.Get(context.Background(), types.NamespacedName{Namespace: r.CrossplaneNamespace, Name: env.Spec.ClusterName}, kubernetesCluster); err == nil {
		if kubernetesCluster.Status.BindingStatus.Phase == crossplaneruntime.BindingPhaseBound {
			r.Log.Info("cluster has been provisioned")
			return true
		}

		r.Log.Info("cluster is not ready yet")
		return false
	}

	r.Log.Error(err, "could not get kubernetesCluster", "NamespacedName", types.NamespacedName{Namespace: r.CrossplaneNamespace, Name: env.Spec.ClusterName}, "dependency", kubernetesCluster)
	return false
}

func (r *EnvironmentReconciler) isArgoCDAppReady(name string) bool {
	argocdApp := &argocdapplicationv1alpha1.Application{}
	var err error
	if err = r.Client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: r.ArgoCDNamespace}, argocdApp); err == nil {

		if argocdApp.Status.Health.Status == argocdapplicationv1alpha1.HealthStatusHealthy &&
			argocdApp.Status.Sync.Status == argocdapplicationv1alpha1.SyncStatusCodeSynced {
			r.Log.Info("argocd app is ready")
			return true
		}

		r.Log.Info("argocd app is not ready yet")
		return false

	}

	r.Log.Error(err, "could not get argocdApp", "NamespacedName", types.NamespacedName{Name: name, Namespace: r.ArgoCDNamespace}, "dependency", argocdApp)
	return false
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
		Name: env.Spec.ClusterClassLabel,
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
						ClassNameLabel: env.Spec.ClusterClassLabel,
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
