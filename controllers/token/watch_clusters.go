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

	"github.com/go-logr/logr"
	infrav1alpha1 "github.com/henderiw-nephio/repository/apis/infra/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type adder interface {
	Add(item interface{})
}

type EnqueueRequestForAllClusters struct {
	client client.Client
	l      logr.Logger
	ctx    context.Context
}

// Create enqueues a request for all ip allocation within the ipam
func (r *EnqueueRequestForAllClusters) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	r.add(evt.Object, q)
}

// Create enqueues a request for all ip allocation within the ipam
func (r *EnqueueRequestForAllClusters) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	r.add(evt.ObjectOld, q)
	r.add(evt.ObjectNew, q)
}

// Create enqueues a request for all ip allocation within the ipam
func (r *EnqueueRequestForAllClusters) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	r.add(evt.Object, q)
}

// Create enqueues a request for all ip allocation within the ipam
func (r *EnqueueRequestForAllClusters) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	r.add(evt.Object, q)
}

func (r *EnqueueRequestForAllClusters) add(obj runtime.Object, queue adder) {
	c, ok := obj.(*capiv1beta1.Cluster)
	if !ok {
		return
	}
	r.l = log.FromContext(r.ctx)
	r.l.Info("event", "kind", obj.GetObjectKind(), "name", c.GetName())

	rts := &infrav1alpha1.TokenList{}
	if err := r.client.List(r.ctx, rts); err != nil {
		r.l.Error(err, "cannot list repository tokens")
		return
	}

	for _, rt := range rts.Items {
		if rt.Spec.Cluster != nil &&
			(rt.Spec.Cluster.Name == c.GetName() && rt.Spec.Cluster.Namespace == c.GetNamespace()) {
			r.l.Info("event requeue repository token", "name", rt.GetName())
			queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: rt.GetNamespace(),
				Name:      rt.GetName()}})
		}
	}
}
