// Copyright (c) 2026 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package validations

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"

	"github.com/siderolabs/omni/client/pkg/omni/resources/omni"
)

// validateExtensions checks each requested extension name against the TalosExtensions catalog for
// the given Talos version. An empty list is accepted. A non-empty list with an empty Talos version
// is rejected, since there is no catalog to validate against.
func validateExtensions(ctx context.Context, st state.State, talosVersion string, names []string) error {
	if len(names) == 0 {
		return nil
	}

	if talosVersion == "" {
		return errors.New("extensions require a Talos version to validate against")
	}

	catalog, err := safe.StateGet[*omni.TalosExtensions](ctx, st, omni.NewTalosExtensions(talosVersion).Metadata())
	if err != nil {
		if state.IsNotFoundError(err) {
			return fmt.Errorf("no Talos extensions catalog for version %q", talosVersion)
		}

		return fmt.Errorf("failed to look up Talos extensions catalog for version %q: %w", talosVersion, err)
	}

	available := make(map[string]struct{}, len(catalog.TypedSpec().Value.GetItems()))
	for _, item := range catalog.TypedSpec().Value.GetItems() {
		available[item.GetName()] = struct{}{}
	}

	for i, name := range names {
		if _, ok := available[name]; !ok {
			return fmt.Errorf("extension %q (entry %d) is not available for Talos version %q", name, i, talosVersion)
		}
	}

	return nil
}
