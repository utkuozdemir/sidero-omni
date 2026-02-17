// Copyright (c) 2026 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

// Package lifecycle provides interfaces for managing component lifecycles.
package lifecycle

import "context"

// Runnable is a component that can be started and blocks until it finishes or the context is canceled.
type Runnable interface {
	Run(ctx context.Context) error
}
