// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"image"

	"github.com/GTKottman/skimdit/internal/media"
)

func orientJPEG(img image.Image, data []byte) image.Image { return media.Orient(img, data) }

func sanitizeSVG(data []byte) ([]byte, error) { return media.CheckSVG(data) }
