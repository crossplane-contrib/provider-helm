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
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
)

const (
	errFailedToGetSecret    = "failed to get secret from namespace \"%s\""
	errSecretDataIsNil      = "secret data is nil"
	errFailedToGetConfigMap = "failed to get configmap from namespace \"%s\""
	errConfigMapDataIsNil   = "configmap data is nil"

	errSourceNotSetForValueFrom        = "source not set for value from"
	errFailedToGetDataFromSecretRef    = "failed to get data from secret ref"
	errFailedToGetDataFromConfigMapRef = "failed to get data from configmap ref"
	errMissingKeyForValuesFrom         = "missing key \"%s\" in values from source"
)

func getSecretData(ctx context.Context, kube client.Client, nn types.NamespacedName) (map[string][]byte, error) {
	s := &corev1.Secret{}
	if err := kube.Get(ctx, nn, s); err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf(errFailedToGetSecret, nn.Namespace))
	}
	if s.Data == nil {
		return nil, errors.New(errSecretDataIsNil)
	}
	return s.Data, nil
}

func getConfigMapData(ctx context.Context, kube client.Client, nn types.NamespacedName) (map[string]string, error) {
	cm := &corev1.ConfigMap{}
	if err := kube.Get(ctx, nn, cm); err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf(errFailedToGetConfigMap, nn.Namespace))
	}
	if cm.Data == nil {
		return nil, errors.New(errConfigMapDataIsNil)
	}
	return cm.Data, nil
}

func getDataValueFromSource(ctx context.Context, kube client.Client, source v1alpha1.ValueFromSource, defaultKey string) (string, error) { // nolint:gocyclo
	if source.SecretKeyRef != nil {
		r := source.SecretKeyRef
		d, err := getSecretData(ctx, kube, types.NamespacedName{Name: r.Name, Namespace: r.Namespace})
		if kerrors.IsNotFound(errors.Cause(err)) && !r.Optional {
			return "", errors.Wrap(err, errFailedToGetDataFromSecretRef)
		}
		if err != nil && !kerrors.IsNotFound(errors.Cause(err)) {
			return "", errors.Wrap(err, errFailedToGetDataFromSecretRef)
		}
		k := defaultKey
		if r.Key != "" {
			k = r.Key
		}
		valBytes, ok := d[k]
		if !ok && !r.Optional {
			return "", errors.New(fmt.Sprintf(errMissingKeyForValuesFrom, k))
		}
		return string(valBytes), nil
	}
	if source.ConfigMapKeyRef != nil {
		r := source.ConfigMapKeyRef
		d, err := getConfigMapData(ctx, kube, types.NamespacedName{Name: r.Name, Namespace: r.Namespace})
		if kerrors.IsNotFound(errors.Cause(err)) && !r.Optional {
			return "", errors.Wrap(err, errFailedToGetDataFromConfigMapRef)
		}
		if err != nil && !kerrors.IsNotFound(errors.Cause(err)) {
			return "", errors.Wrap(err, errFailedToGetDataFromConfigMapRef)
		}
		k := defaultKey
		if r.Key != "" {
			k = r.Key
		}
		valString, ok := d[k]
		if !ok && !r.Optional {
			return "", errors.New(fmt.Sprintf(errMissingKeyForValuesFrom, k))
		}
		return valString, nil
	}
	return "", errors.New(errSourceNotSetForValueFrom)
}
