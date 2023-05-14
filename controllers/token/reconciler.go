/*
Copyright 2023 The Nephio Authors.

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

package token

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/nokia/k8s-ipam/pkg/meta"
	"github.com/nokia/k8s-ipam/pkg/resource"
	"github.com/pkg/errors"

	"code.gitea.io/sdk/gitea"
	infrav1alpha1 "github.com/henderiw-nephio/repository/apis/infra/v1alpha1"
	ctrlconfig "github.com/henderiw-nephio/repository/controllers/config"
	"github.com/henderiw-nephio/repository/pkg/applicator"
	"github.com/henderiw-nephio/repository/pkg/giteaclient"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	finalizer = "infra.nephio.org/finalizer"
	// errors
	errGetCr        = "cannot get cr"
	errUpdateStatus = "cannot update status"
)

//+kubebuilder:rbac:groups=infra.nephio.org,resources=tokens,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infra.nephio.org,resources=tokens/status,verbs=get;update;patch

// SetupWithManager sets up the controller with the Manager.
func Setup(mgr ctrl.Manager, options *ctrlconfig.ControllerConfig) error {
	r := &reconciler{
		APIPatchingApplicator: applicator.NewAPIPatchingApplicator(mgr.GetClient()),
		giteaClient:           options.GiteaClient,
		finalizer:             resource.NewAPIFinalizer(mgr.GetClient(), finalizer),
	}

	/*
		clusterHandler := &EnqueueRequestForAllClusters{
			client: mgr.GetClient(),
			ctx:    context.Background(),
		}
	*/

	return ctrl.NewControllerManagedBy(mgr).
		Named("TokenController").
		For(&infrav1alpha1.Token{}).
		//Watches(&source.Kind{Type: &capiv1beta1.Cluster{}}, clusterHandler).
		Complete(r)
}

type reconciler struct {
	applicator.APIPatchingApplicator
	giteaClient giteaclient.GiteaClient
	finalizer   *resource.APIFinalizer

	l logr.Logger
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.l = log.FromContext(ctx)
	r.l.Info("reconcile", "req", req)

	cr := &infrav1alpha1.Token{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		// if the resource no longer exists the reconcile loop is done
		if resource.IgnoreNotFound(err) != nil {
			r.l.Error(err, "cannot get resource")
			return ctrl.Result{}, errors.Wrap(resource.IgnoreNotFound(err), "cannot get resource")
		}
		return ctrl.Result{}, nil
	}

	// check if client exists otherwise retry
	giteaClient := r.giteaClient.Get()
	if giteaClient == nil {
		err := fmt.Errorf("gitea server unreachable")
		r.l.Error(err, "cannot connect to gitea server")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
	}

	client, err := r.getClusterClient(ctx, cr.Spec.Cluster)
	if err != nil {
		r.l.Error(err, "cannot connect to cluster client")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
	}

	if meta.WasDeleted(cr) {
		if err := r.deleteToken(ctx, client, giteaClient, cr); err != nil {
			return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
		}

		if err := r.finalizer.RemoveFinalizer(ctx, cr); err != nil {
			r.l.Error(err, "cannot remove finalizer")
			cr.SetConditions(infrav1alpha1.Failed(err.Error()))
			return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
		}

		r.l.Info("Successfully deleted resource")
		return ctrl.Result{Requeue: false}, nil
	}

	if err := r.finalizer.AddFinalizer(ctx, cr); err != nil {
		// If this is the first time we encounter this issue we'll be requeued
		// implicitly when we update our status with the new error condition. If
		// not, we requeue explicitly, which will trigger backoff.
		r.l.Error(err, "cannot add finalizer")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
	}

	// create token and secret
	if err := r.createToken(ctx, client, giteaClient, cr); err != nil {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
	}
	cr.SetConditions(infrav1alpha1.Ready())
	return ctrl.Result{}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
}

func (r *reconciler) createToken(ctx context.Context, client applicator.APIPatchingApplicator, giteaClient *gitea.Client, cr *infrav1alpha1.Token) error {

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: os.Getenv("GIT_NAMESPACE"),
		Name:      os.Getenv("GIT_SECRET_NAME"),
	},
		secret); err != nil {
		r.l.Error(err, "cannot list repo")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return errors.Wrap(err, "cannot get secret")
	}

	tokens, _, err := giteaClient.ListAccessTokens(gitea.ListAccessTokensOptions{})
	if err != nil {
		r.l.Error(err, "cannot list repo")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return err
	}
	tokenFound := false
	for _, repo := range tokens {
		if repo.Name == cr.GetTokenName() {
			tokenFound = true
			break
		}
	}
	if !tokenFound {
		token, _, err := giteaClient.CreateAccessToken(gitea.CreateAccessTokenOption{
			Name: cr.GetTokenName(),
		})
		if err != nil {
			r.l.Error(err, "cannot create token")
			cr.SetConditions(infrav1alpha1.Failed(err.Error()))
			return err
		}
		r.l.Info("token created", "name", cr.GetName())
		// owner reference dont work since this is a cross-namespace resource
		secret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.Identifier(),
				Kind:       reflect.TypeOf(corev1.Secret{}).Name(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cr.GetNamespace(),
				Name:      cr.GetName(),
			},
			Data: map[string][]byte{
				"username": secret.Data["username"],
				"token":    []byte(token.Token),
			},
			Type: corev1.SecretTypeBasicAuth,
		}
		if err := client.Apply(ctx, secret); err != nil {
			cr.SetConditions(infrav1alpha1.Failed(err.Error()))
			r.l.Error(err, "cannot create secret")
			return err
		}
		r.l.Info("secret for token created", "name", cr.GetName())

	}
	return nil
}

func (r *reconciler) deleteToken(ctx context.Context, client applicator.APIPatchingApplicator, giteaClient *gitea.Client, cr *infrav1alpha1.Token) error {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.Identifier(),
			Kind:       reflect.TypeOf(corev1.Secret{}).Name(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cr.GetNamespace(),
			Name:      cr.GetName(),
		},
	}
	err := client.Delete(ctx, secret)
	if resource.IgnoreNotFound(err) != nil {
		r.l.Error(err, "cannot delete access token secret")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return err
	}

	r.l.Info("token deleted", "name", cr.GetTokenName())
	_, err = giteaClient.DeleteAccessToken(cr.GetTokenName())
	if err != nil {
		r.l.Error(err, "cannot delete token")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return err
	}
	r.l.Info("token deleted", "name", cr.GetTokenName())
	return nil
}

func (r *reconciler) getClusterClient(ctx context.Context, cluster *corev1.ObjectReference) (applicator.APIPatchingApplicator, error) {
	if cluster == nil {
		// if the cluster is local we return the local client
		return r.APIPatchingApplicator, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-kubeconfig", cluster.Name),
		Namespace: cluster.Namespace,
	}, secret); err != nil {
		r.l.Error(err, "cannot get secret")
		return applicator.APIPatchingApplicator{}, err
	}

	//r.l.Info("cluster", "config", string(secret.Data["value"]))

	config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["value"])
	if err != nil {
		r.l.Error(err, "cannot get rest Config from kubeconfig")
		return applicator.APIPatchingApplicator{}, err
	}
	clClient, err := client.New(config, client.Options{})
	if err != nil {
		r.l.Error(err, "cannot get client from rest config")
		return applicator.APIPatchingApplicator{}, err
	}
	return applicator.NewAPIPatchingApplicator(clClient), nil
}
