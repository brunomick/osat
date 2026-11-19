//go:build windows

package rdpwin

import (
	"testing"

	"osat/internal/browser"
)

func TestDesktopSizeStaysInWindowsLimits(t *testing.T) {
	for _, c := range []struct{ w, h, ww, wh int }{
		{1281, 720, 1280, 720}, // ancho múltiplo de 4
		{1602, 960, 1600, 960}, // par pero no múltiplo de 4: xrdp lo dibuja torcido
		{100, 50, 200, 200},    // mínimo 200
		{9000, 9000, 8192, 8192},
	} {
		if w, h := desktopSize(browser.Bounds{W: c.w, H: c.h}); w != c.ww || h != c.wh {
			t.Errorf("desktopSize(%d×%d) = %d×%d, want %d×%d", c.w, c.h, w, h, c.ww, c.wh)
		}
	}
}

func TestScaleFactors(t *testing.T) {
	for _, c := range []struct {
		scale       float64
		desk, devic uint32
	}{
		{0, 100, 100}, {1, 100, 100}, {1.25, 125, 140}, {1.5, 150, 140}, {2, 200, 180}, {6, 500, 180},
	} {
		if d, v := scaleFactors(c.scale); d != c.desk || v != c.devic {
			t.Errorf("scaleFactors(%v) = %d, %d; want %d, %d", c.scale, d, v, c.desk, c.devic)
		}
	}
}
