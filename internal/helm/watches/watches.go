// Copyright 2019 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package watches

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"text/template"

	sprig "github.com/go-task/slim-sprig"
	"helm.sh/helm/v3/pkg/chartutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

const WatchesFile = "watches.yaml"

// Watch defines options for configuring a watch for a Helm-based
// custom resource.
type Watch struct {
	schema.GroupVersionKind `json:",inline"`
	ChartDir                string               `json:"chart"`
	WatchDependentResources *bool                `json:"watchDependentResources,omitempty"`
	OverrideValues          map[string]string    `json:"overrideValues,omitempty"`
	Selector                metav1.LabelSelector `json:"selector"`
	ReconcilePeriod         metav1.Duration      `json:"reconcilePeriod,omitempty"`
	DryRunOption            string               `json:"dryRunOption,omitempty"`
}

// UnmarshalYAML unmarshals an individual watch from the Helm watches.yaml file
// into a Watch struct.
//
// Deprecated: This function is no longer used internally to unmarshal
// watches.yaml data. To ensure the correct defaults are applied when loading
// watches.yaml, use Load() or LoadReader() instead of this function and/or
// yaml.Unmarshal().
func (w *Watch) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// by default, the operator will watch dependent resources
	trueVal := true
	w.WatchDependentResources = &trueVal

	// hide watch data in plain struct to prevent unmarshal from calling
	// UnmarshalYAML again
	type plain Watch

	return unmarshal((*plain)(w))
}

// Load loads a slice of Watches from the watch file at `path`. For each entry
// in the watches file, it verifies the configuration. If an error is
// encountered loading the file or verifying the configuration, it will be
// returned.
func Load(path string) ([]Watch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open watches file: %w", err)
	}
	w, err := LoadReader(f)

	// Make sure to close the file, regardless of the error returned by
	// LoadReader.
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("could not close watches file: %w", err)
	}
	return w, err
}

// LoadReader loads a slice of Watches from the provided reader. For each entry
// in the watches file, it verifies the configuration. If an error is
// encountered reading or verifying the configuration, it will be returned.
func LoadReader(reader io.Reader) ([]Watch, error) {
	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	watches := []Watch{}
	err = yaml.Unmarshal(b, &watches)
	if err != nil {
		return nil, err
	}

	watchesMap := make(map[schema.GroupVersionKind]struct{})
	for i, w := range watches {
		gvk := w.GroupVersionKind

		if err := verifyGVK(gvk); err != nil {
			return nil, fmt.Errorf("invalid GVK: %s: %w", gvk, err)
		}

		if _, err := chartutil.IsChartDir(w.ChartDir); err != nil {
			return nil, fmt.Errorf("invalid chart directory %s: %w", w.ChartDir, err)
		}

		if _, ok := watchesMap[gvk]; ok {
			return nil, fmt.Errorf("duplicate GVK: %s", gvk)
		}
		watchesMap[gvk] = struct{}{}
		if w.WatchDependentResources == nil {
			trueVal := true
			w.WatchDependentResources = &trueVal
		}
		w.OverrideValues, err = expandOverrideValues(w.OverrideValues)
		if err != nil {
			return nil, fmt.Errorf("failed to expand override values: %v", err)
		}
		watches[i] = w
	}
	return watches, nil
}

func expandOverrideValues(in map[string]string) (map[string]string, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]string)
	for k, v := range in {
		envV := os.ExpandEnv(v)

		v := &bytes.Buffer{}
		tmplV, err := template.New(k).Funcs(sprig.TxtFuncMap()).Parse(envV)
		if err != nil {
			return nil, fmt.Errorf("invalid template string %q: %v", envV, err)
		}
		if err := tmplV.Execute(v, nil); err != nil {
			return nil, fmt.Errorf("failed to execute template %q: %v", envV, err)
		}
		out[k] = v.String()
	}
	return out, nil
}

func verifyGVK(gvk schema.GroupVersionKind) error {
	// A GVK without a group is valid. Certain scenarios may cause a GVK
	// without a group to fail in other ways later in the initialization
	// process.
	if gvk.Version == "" {
		return errors.New("version must not be empty")
	}
	if gvk.Kind == "" {
		return errors.New("kind must not be empty")
	}
	return nil
}
