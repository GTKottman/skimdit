// SPDX-License-Identifier: AGPL-3.0-or-later

// Package version holds the Skimdit version and the theme/plugin API level.
package version

import (
	"fmt"
	"runtime"
)

// These are set at release time with -ldflags "-X".
var (
	Version = "0.0.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// EngineAPI is the theme and plugin contract version. It is bumped only on
// breaking changes, and the previous value stays supported for 12 months.
const EngineAPI = 1

// String returns the one-line version report printed by `skimdit version`.
func String() string {
	return fmt.Sprintf("skimdit %s (commit %s, built %s, engine_api %d, %s/%s)",
		Version, Commit, Date, EngineAPI, runtime.GOOS, runtime.GOARCH)
}
