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

const (
	// MaxResourceIDLength caps the byte length of a resource ID.
	MaxResourceIDLength = 1024

	// MaxLabelKeyLength caps the byte length of a label key.
	MaxLabelKeyLength = 1024

	// MaxLabelValueLength caps the byte length of a label value.
	MaxLabelValueLength = 16 * 1024

	// MaxLabelsCount caps the number of labels on a resource.
	MaxLabelsCount = 256

	// MaxAnnotationKeyLength caps the byte length of an annotation key.
	MaxAnnotationKeyLength = MaxLabelKeyLength

	// MaxAnnotationValueLength caps the byte length of an annotation value.
	MaxAnnotationValueLength = MaxLabelValueLength

	// MaxAnnotationsCount caps the number of annotations on a resource.
	MaxAnnotationsCount = MaxLabelsCount
)

// metadataValidationOptions enforces universal metadata rules: resource ID rules at create time
// (non-empty, no control characters, length cap), label and annotation rules at both create and
// update (key non-empty, key/value within length cap, no control characters, count cap), and a ban
// on user-driven changes to the finalizers list. Finalizers belong to controllers and are managed
// via the dedicated AddFinalizer / RemoveFinalizer state API which bypasses this validator. Create
// requires the finalizers list to be empty; Update requires it to match the stored list exactly.
// Label and annotation updates validate only entries that are new or changed compared to the
// existing resource, so existing offending values do not block unrelated updates.
func metadataValidationOptions() []validated.StateOption {
	return []validated.StateOption{
		validated.WithCreateValidations(
			func(_ context.Context, res resource.Resource, _ ...state.CreateOption) error {
				return validateResourceID(res.Metadata().ID())
			},
			func(_ context.Context, res resource.Resource, _ ...state.CreateOption) error {
				return validateMetadataMap("label", nil, res.Metadata().Labels().Raw(), MaxLabelsCount, MaxLabelKeyLength, MaxLabelValueLength)
			},
			func(_ context.Context, res resource.Resource, _ ...state.CreateOption) error {
				return validateMetadataMap("annotation", nil, res.Metadata().Annotations().Raw(), MaxAnnotationsCount, MaxAnnotationKeyLength, MaxAnnotationValueLength)
			},
			func(_ context.Context, res resource.Resource, _ ...state.CreateOption) error {
				return validateFinalizers(nil, res.Metadata().Finalizers())
			},
		),
		validated.WithUpdateValidations(
			func(_ context.Context, oldRes, newRes resource.Resource, _ ...state.UpdateOption) error {
				return validateMetadataMap("label", existingLabels(oldRes), newRes.Metadata().Labels().Raw(), MaxLabelsCount, MaxLabelKeyLength, MaxLabelValueLength)
			},
			func(_ context.Context, oldRes, newRes resource.Resource, _ ...state.UpdateOption) error {
				return validateMetadataMap("annotation", existingAnnotations(oldRes), newRes.Metadata().Annotations().Raw(), MaxAnnotationsCount, MaxAnnotationKeyLength, MaxAnnotationValueLength)
			},
			func(_ context.Context, oldRes, newRes resource.Resource, _ ...state.UpdateOption) error {
				return validateFinalizers(oldRes.Metadata().Finalizers(), newRes.Metadata().Finalizers())
			},
		),
	}
}

// validateFinalizers blocks user-driven changes to the finalizers list. On create, old is nil and
// the new list must be empty. On update, the submitted list must be set-equal to the stored list:
// clients cannot add new finalizers (which would create unrecoverable tearingDown resources) and
// cannot drop existing ones (which would let resources skip controller cleanup). Controllers manage
// their own finalizers via the AddFinalizer / RemoveFinalizer state API, which does not go through
// this validator.
func validateFinalizers(old, current *resource.Finalizers) error {
	if old == nil {
		if current != nil && !current.Empty() {
			return errors.New("finalizers must not be set by clients on resource creation")
		}

		return nil
	}

	oldList := *old

	var currentList resource.Finalizers
	if current != nil {
		currentList = *current
	}

	for _, fin := range currentList {
		if !old.Has(fin) {
			return fmt.Errorf("finalizer %q must not be added by clients", fin)
		}
	}

	for _, fin := range oldList {
		if !currentList.Has(fin) {
			return fmt.Errorf("finalizer %q must not be removed by clients", fin)
		}
	}

	return nil
}

func validateResourceID(id string) error {
	if id == "" {
		return errors.New("resource ID must not be empty")
	}

	if len(id) > MaxResourceIDLength {
		return fmt.Errorf("resource ID is too long: %d bytes (max %d)", len(id), MaxResourceIDLength)
	}

	if strings.ContainsFunc(id, isControlChar) {
		return errors.New("resource ID must not contain control characters")
	}

	return nil
}

// validateMetadataMap applies entry and count rules to a label or annotation map. On update, the
// caller passes the old map so unchanged entries are skipped and the count cap only fires when the
// new map both exceeds the cap and grew compared to the old.
//
//nolint:unparam
func validateMetadataMap(kind string, old, current map[string]string, maxCount, maxKey, maxValue int) error {
	if len(current) > maxCount && len(current) > len(old) {
		return fmt.Errorf("too many %ss: %d (max %d)", kind, len(current), maxCount)
	}

	for k, v := range current {
		if oldV, existed := old[k]; existed && oldV == v {
			continue
		}

		if err := validateMetadataEntry(kind, k, v, maxKey, maxValue); err != nil {
			return err
		}
	}

	return nil
}

func validateMetadataEntry(kind, key, value string, maxKey, maxValue int) error {
	if key == "" {
		return fmt.Errorf("%s key must not be empty", kind)
	}

	if len(key) > maxKey {
		return fmt.Errorf("%s key is too long: %d bytes (max %d)", kind, len(key), maxKey)
	}

	if strings.ContainsFunc(key, isControlChar) {
		return fmt.Errorf("%s key %q must not contain control characters", kind, key)
	}

	if len(value) > maxValue {
		return fmt.Errorf("%s value for key %q is too long: %d bytes (max %d)", kind, key, len(value), maxValue)
	}

	if strings.ContainsFunc(value, isControlChar) {
		return fmt.Errorf("%s value for key %q must not contain control characters", kind, key)
	}

	return nil
}

func isControlChar(r rune) bool {
	return r < 0x20 || r == 0x7F
}

func existingLabels(res resource.Resource) map[string]string {
	if res == nil {
		return nil
	}

	return res.Metadata().Labels().Raw()
}

func existingAnnotations(res resource.Resource) map[string]string {
	if res == nil {
		return nil
	}

	return res.Metadata().Annotations().Raw()
}
