// SPDX-License-Identifier: AGPL-3.0-or-later

// Package themes embeds the default theme into the binary.
package themes

import "embed"

// Default is the built-in theme.
//
//go:embed all:default
var Default embed.FS
