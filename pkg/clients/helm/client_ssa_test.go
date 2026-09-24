/*
Copyright 2026 The Crossplane Authors.

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

package helm

import (
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/google/go-cmp/cmp"
	"k8s.io/client-go/rest"
)

func TestNewClient_SSAForceConflictsWiring(t *testing.T) {
	// NewClient doesn't dial the API server, so a non-dialable host is safe.
	restConfig := &rest.Config{Host: "http://127.0.0.1:0"}

	type want struct {
		installForceConflicts   bool
		installServerSideApply  bool
		upgradeForceConflicts   bool
		upgradeServerSideApply  string
		rollbackForceConflicts  bool
		rollbackServerSideApply string
	}

	cases := map[string]struct {
		reason            string
		ssaForceConflicts bool
		want              want
	}{
		"Disabled": {
			reason:            "Without ssaForceConflicts, upgrade and rollback should keep Helm's default \"auto\" apply method.",
			ssaForceConflicts: false,
			want: want{
				installServerSideApply:  true,
				upgradeServerSideApply:  "auto",
				rollbackServerSideApply: "auto",
			},
		},
		"Enabled": {
			reason:            "With ssaForceConflicts, upgrade and rollback should force server-side apply, since Helm rejects ForceConflicts for releases that last used client-side apply.",
			ssaForceConflicts: true,
			want: want{
				installForceConflicts:   true,
				installServerSideApply:  true,
				upgradeForceConflicts:   true,
				upgradeServerSideApply:  "true",
				rollbackForceConflicts:  true,
				rollbackServerSideApply: "true",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := NewClient(logging.NewNopLogger(), restConfig, func(a *Args) {
				a.Namespace = "default"
				a.SSAForceConflicts = tc.ssaForceConflicts
			})
			if err != nil {
				t.Fatalf("NewClient(...): unexpected error: %v", err)
			}
			cc, ok := c.(*client)
			if !ok {
				t.Fatalf("NewClient(...) did not return a *client")
			}

			got := want{
				installForceConflicts:   cc.installClient.ForceConflicts,
				installServerSideApply:  cc.installClient.ServerSideApply,
				upgradeForceConflicts:   cc.upgradeClient.ForceConflicts,
				upgradeServerSideApply:  cc.upgradeClient.ServerSideApply,
				rollbackForceConflicts:  cc.rollbackClient.ForceConflicts,
				rollbackServerSideApply: cc.rollbackClient.ServerSideApply,
			}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(want{})); diff != "" {
				t.Errorf("\n%s\nNewClient(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}
