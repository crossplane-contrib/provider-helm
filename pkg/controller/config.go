/*
Copyright 2020 The Crossplane Authors.

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

package controller

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	errFailedToGetSecret    = "failed to get secret from namespace \"%s\""
	errSecretDataIsNil      = "secret data is nil"
	errFailedToGetConfigMap = "failed to get configmap from namespace \"%s\""
	errConfigMapDataIsNil   = "configmap data is nil"
)

func getSecretData(ctx context.Context, kube client.Client, key types.NamespacedName) (map[string][]byte, error) {
	s := &corev1.Secret{}
	if err := kube.Get(ctx, key, s); err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf(errFailedToGetSecret, key.Namespace))
	}
	if s.Data == nil {
		return nil, errors.New(errSecretDataIsNil)
	}
	return s.Data, nil
}

// getConfigMapData
func _(ctx context.Context, kube client.Client, key types.NamespacedName) (map[string]string, error) {
	cm := &corev1.ConfigMap{}
	if err := kube.Get(ctx, key, cm); err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf(errFailedToGetConfigMap, key.Namespace))
	}
	if cm.Data == nil {
		return nil, errors.New(errConfigMapDataIsNil)
	}
	return cm.Data, nil
}
