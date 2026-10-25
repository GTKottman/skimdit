// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/store"
)

// ResetAccess is the escape hatch for a lost password or device, run from a
// shell on the server: it sets a new password, turns off the code app,
// removes every passkey and signs out every browser.
func ResetAccess(root, password string) error {
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.Path(cfg.Server.Data))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetPassword(hash); err != nil {
		return err
	}
	_ = st.SetTOTP("")
	keys, _ := st.Passkeys()
	for _, k := range keys {
		_ = st.DeletePasskey(k.ID)
	}
	return st.DeleteSessions()
}
