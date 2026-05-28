// Copyright (c) 2026 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package validations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"

	"github.com/siderolabs/omni/internal/backend/runtime/omni/validated"
)

// MaxResourceIDLength caps the byte length of a resource ID.
const MaxResourceIDLength = 1024

// metadataValidationOptions enforces universal metadata rules: the resource ID must be non-empty,
// must fit within the length cap, and must not contain ASCII control characters. Applied across all
// resource types at create time. Updates are not validated because resource IDs are immutable.
func metadataValidationOptions() []validated.StateOption {
	return []validated.StateOption{
		validated.WithCreateValidations(func(_ context.Context, res resource.Resource, _ ...state.CreateOption) error {
			return validateResourceID(res.Metadata().ID())
		}),
	}
}

func validateResourceID(id string) error {
	if id == "" {
		return errors.New("resource ID must not be empty")
	}

	if len(id) > MaxResourceIDLength {
		return fmt.Errorf("resource ID is too long: %d bytes (max %d)", len(id), MaxResourceIDLength)
	}

	if strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7F }) {
		return errors.New("resource ID must not contain control characters")
	}

	return nil
}
