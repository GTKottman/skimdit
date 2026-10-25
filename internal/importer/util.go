// SPDX-License-Identifier: AGPL-3.0-or-later

package importer

import "net/url"

func urlUnescape(s string) (string, error) { return url.PathUnescape(s) }
