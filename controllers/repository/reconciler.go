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

package repository

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/nokia/k8s-ipam/pkg/meta"
	"github.com/nokia/k8s-ipam/pkg/resource"
	"github.com/pkg/errors"

	"code.gitea.io/sdk/gitea"
	infrav1alpha1 "github.com/henderiw-nephio/repository/apis/infra/v1alpha1"
	ctrlconfig "github.com/henderiw-nephio/repository/controllers/config"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizer = "infra.nephio.org/finalizer"
	// errors
	errGetCr        = "cannot get cr"
	errUpdateStatus = "cannot update status"
)

//+kubebuilder:rbac:groups=infra.nephio.org,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infra.nephio.org,resources=repositories/status,verbs=get;update;patch

// SetupWithManager sets up the controller with the Manager.
func Setup(mgr ctrl.Manager, options *ctrlconfig.ControllerConfig) error {
	r := &reconciler{
		Client:      mgr.GetClient(),
		giteaClient: options.GiteaClient,
		finalizer:   resource.NewAPIFinalizer(mgr.GetClient(), finalizer),
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1alpha1.Repository{}).
		Complete(r)
}

type reconciler struct {
	client.Client
	giteaClient *gitea.Client
	finalizer   *resource.APIFinalizer

	l logr.Logger
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.l = log.FromContext(ctx)
	r.l.Info("reconcile", "req", req)

	cr := &infrav1alpha1.Repository{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		// if the resource no longer exists the reconcile loop is done
		if resource.IgnoreNotFound(err) != nil {
			r.l.Error(err, "cannot get resource")
			return ctrl.Result{}, errors.Wrap(resource.IgnoreNotFound(err), "cannot get resource")
		}
		return reconcile.Result{}, nil
	}

	if meta.WasDeleted(cr) {
		_, err := r.giteaClient.DeleteRepo("owner", cr.GetName())
		if err != nil {
			r.l.Error(err, "cannot delete repo")
			cr.SetConditions(infrav1alpha1.Failed(err.Error()))
			return reconcile.Result{Requeue: true}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
		}

		if err := r.finalizer.RemoveFinalizer(ctx, cr); err != nil {
			r.l.Error(err, "cannot remove finalizer")
			cr.SetConditions(infrav1alpha1.Failed(err.Error()))
			return reconcile.Result{Requeue: true}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
		}

		r.l.Info("Successfully deleted resource")
		return reconcile.Result{Requeue: false}, nil
	}

	if err := r.finalizer.AddFinalizer(ctx, cr); err != nil {
		// If this is the first time we encounter this issue we'll be requeued
		// implicitly when we update our status with the new error condition. If
		// not, we requeue explicitly, which will trigger backoff.
		r.l.Error(err, "cannot add finalizer")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return reconcile.Result{Requeue: true}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
	}

	repos, _, err := r.giteaClient.ListMyRepos(gitea.ListReposOptions{})
	if err != nil {
		r.l.Error(err, "cannot list repo")
		cr.SetConditions(infrav1alpha1.Failed(err.Error()))
		return reconcile.Result{}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
	}

	repoFound := false
	for _, repo := range repos {
		if repo.Name == cr.GetName() {
			repoFound = true
			break
		}
	}
	if !repoFound {
		_, _, err := r.giteaClient.CreateRepo(gitea.CreateRepoOption{
			Name:     cr.GetName(),
			AutoInit: true,
		})
		if err != nil {
			r.l.Error(err, "cannot create repo")
			cr.SetConditions(infrav1alpha1.Failed(err.Error()))
			return reconcile.Result{}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
		}
	}
	cr.SetConditions(infrav1alpha1.Ready())
	return ctrl.Result{}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
}
