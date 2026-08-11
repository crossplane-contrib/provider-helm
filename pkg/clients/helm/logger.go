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

package helm

import (
	"context"
	"log/slog"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
)

// slogHandler forwards helm v4's slog-based internal logging to the provider
// logger. Helm discards these logs unless a handler is set on the
// action.Configuration.
type slogHandler struct {
	log logging.Logger
}

var _ slog.Handler = slogHandler{}

// Enabled reports every level as enabled; the provider logger decides whether
// debug output is emitted.
func (h slogHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle routes records below INFO to Debug and the rest to Info, keeping the
// original level as an attribute for WARN and above since logging.Logger has
// no equivalent levels.
func (h slogHandler) Handle(_ context.Context, r slog.Record) error {
	keysAndValues := make([]any, 0, 2*r.NumAttrs()+2)
	r.Attrs(func(a slog.Attr) bool {
		keysAndValues = append(keysAndValues, a.Key, a.Value.Any())
		return true
	})
	switch {
	case r.Level < slog.LevelInfo:
		h.log.Debug(r.Message, keysAndValues...)
	case r.Level > slog.LevelInfo:
		h.log.Info(r.Message, append(keysAndValues, "level", r.Level.String())...)
	default:
		h.log.Info(r.Message, keysAndValues...)
	}
	return nil
}

func (h slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	keysAndValues := make([]any, 0, 2*len(attrs))
	for _, a := range attrs {
		keysAndValues = append(keysAndValues, a.Key, a.Value.Any())
	}
	return slogHandler{log: h.log.WithValues(keysAndValues...)}
}

// WithGroup flattens groups, as logging.Logger has no grouping concept.
func (h slogHandler) WithGroup(string) slog.Handler { return h }
