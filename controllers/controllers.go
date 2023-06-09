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

package controllers

import (
	"context"

	ctrlconfig "github.com/henderiw-nephio/repository/controllers/config"
	"github.com/henderiw-nephio/repository/controllers/repository"
	"github.com/henderiw-nephio/repository/controllers/token"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Setup  controllers.
func Setup(ctx context.Context, mgr ctrl.Manager, opts *ctrlconfig.ControllerConfig) error {
	for _, setup := range []func(mgr ctrl.Manager, cfg *ctrlconfig.ControllerConfig) error{
		repository.Setup,
		token.Setup,
	} {
		if err := setup(mgr, opts); err != nil {
			return err
		}
	}
	return nil
}
