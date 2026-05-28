// Copyright (c) 2026 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package validations

import (
	"context"
	"fmt"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"

	"github.com/siderolabs/omni/client/pkg/constants"
	"github.com/siderolabs/omni/client/pkg/omni/resources/omni"
)

// MaxExtensionsCount caps the number of entries in a repeated extensions list.
const MaxExtensionsCount = 64

// validateExtensions checks each requested extension name against the TalosExtensions catalog. An
// empty list is accepted. An empty Talos version is the "automatic" sentinel used by resources
// like InstallationMediaConfig; in that case the default Talos version's catalog is used so the
// names still get validated. The list is rejected if it exceeds MaxExtensionsCount or contains
// duplicate entries.
func validateExtensions(ctx context.Context, st state.State, talosVersion string, names []string) error {
	if len(names) == 0 {
		return nil
	}

	if len(names) > MaxExtensionsCount {
		return fmt.Errorf("extensions list has %d entries, exceeds maximum of %d", len(names), MaxExtensionsCount)
	}

	if talosVersion == "" {
		talosVersion = constants.DefaultTalosVersion
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

	seen := make(map[string]struct{}, len(names))

	for i, name := range names {
		if _, ok := available[name]; !ok {
			return fmt.Errorf("extension %q (entry %d) is not available for Talos version %q", name, i, talosVersion)
		}

		if _, ok := seen[name]; ok {
			return fmt.Errorf("extension %q (entry %d) is listed more than once", name, i)
		}

		seen[name] = struct{}{}
	}

	return nil
}
