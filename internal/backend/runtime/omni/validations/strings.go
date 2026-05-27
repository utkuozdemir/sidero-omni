// Copyright (c) 2026 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package validations

import (
	"fmt"
	"strings"
)

// validateUserString applies hygiene checks to a user-supplied string field: a byte-length cap
// and a rejection of ASCII control characters. Empty values are accepted.
func validateUserString(fieldName, value string, maxLen int) error {
	if value == "" {
		return nil
	}

	if len(value) > maxLen {
		return fmt.Errorf("%s is too long: %d bytes (max %d)", fieldName, len(value), maxLen)
	}

	if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7F }) {
		return fmt.Errorf("%s contains a control character", fieldName)
	}

	return nil
}

// validateUserStringSlice applies hygiene checks to a list of user-supplied strings: an element
// count cap plus the same per-element rules as validateUserString.
func validateUserStringSlice(fieldName string, values []string, maxCount, maxElemLen int) error {
	if len(values) > maxCount {
		return fmt.Errorf("%s has too many entries: %d (max %d)", fieldName, len(values), maxCount)
	}

	for i, v := range values {
		if err := validateUserString(fmt.Sprintf("%s[%d]", fieldName, i), v, maxElemLen); err != nil {
			return err
		}
	}

	return nil
}
