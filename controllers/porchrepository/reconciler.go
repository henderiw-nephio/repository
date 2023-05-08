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

package porchrepository

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/nokia/k8s-ipam/pkg/resource"
	"github.com/pkg/errors"

	porchconfigv1alpha1 "github.com/GoogleContainerTools/kpt/porch/api/porchconfig/v1alpha1"
	infrav1alpha1 "github.com/henderiw-nephio/repository/apis/infra/v1alpha1"
	ctrlconfig "github.com/henderiw-nephio/repository/controllers/config"
	"github.com/henderiw-nephio/repository/pkg/applicator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// errors
	errGetCr        = "cannot get cr"
	errUpdateStatus = "cannot update status"
)

//+kubebuilder:rbac:groups=infra.nephio.org,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infra.nephio.org,resources=repositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=repositories/status,verbs=get;update;patch

// SetupWithManager sets up the controller with the Manager.
func Setup(mgr ctrl.Manager, options *ctrlconfig.ControllerConfig) error {
	r := &reconciler{
		APIPatchingApplicator: applicator.NewAPIPatchingApplicator(mgr.GetClient()),
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1alpha1.Repository{}).
		Complete(r)
}

type reconciler struct {
	applicator.APIPatchingApplicator

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

	cr.SetConditions(infrav1alpha1.PorchRepoUnkown())
	if cr.GetCondition(infrav1alpha1.ConditionTypeReady).Status == metav1.ConditionTrue && cr.Status.URL != nil {
		// TODO set owner reference
		porchRepo := &porchconfigv1alpha1.Repository{
			TypeMeta: metav1.TypeMeta{
				APIVersion: porchconfigv1alpha1.GroupVersion.Identifier(),
				Kind:       reflect.TypeOf(porchconfigv1alpha1.Repository{}).Name(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      cr.GetName(),
				Namespace: cr.GetNamespace(),
			},
			Spec: porchconfigv1alpha1.RepositorySpec{
				Deployment: true,
				Type:       porchconfigv1alpha1.RepositoryTypeGit,
				Git: &porchconfigv1alpha1.GitRepository{
					Repo:         *cr.Status.URL,
					Branch:       "main",
					Directory:    "/",
					CreateBranch: true,
					SecretRef: porchconfigv1alpha1.SecretRef{
						Name: "git-repo-access-token",
					},
				},
			},
		}
		if err := r.Apply(ctx, porchRepo); err != nil {
			r.l.Error(err, "cannot apply resource")
			cr.SetConditions(infrav1alpha1.PorchRepoFailed())
			return reconcile.Result{}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
		}
		cr.SetConditions(infrav1alpha1.PorchRepoReady())
	}

	return ctrl.Result{}, errors.Wrap(r.Status().Update(ctx, cr), errUpdateStatus)
}
