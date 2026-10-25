// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import (
	"strings"
	"testing"
)

func TestStringNamesVersionAndAPI(t *testing.T) {
	s := String()
	for _, want := range []string{"skimdit ", Version, "engine_api 1"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, missing %q", s, want)
		}
	}
}
