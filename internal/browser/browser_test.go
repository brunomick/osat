package browser

import "testing"

func TestBoundsPhysical(t *testing.T) {
	b := Bounds{X: 10, Y: 21, W: 101, H: 50}
	for _, c := range []struct {
		scale float64
		want  Bounds
	}{
		{1, Bounds{10, 21, 101, 50}},
		{1.25, Bounds{13, 26, 126, 63}}, // 12,5→13; 26,25→26; borde derecho 138,75→139; inferior 88,75→89
		{1.5, Bounds{15, 32, 152, 75}},  // 31,5→32; borde derecho 166,5→167; inferior 106,5→107
		{0, Bounds{10, 21, 101, 50}},    // escala no válida = 1
		{-2, Bounds{10, 21, 101, 50}},
	} {
		if got := b.Physical(c.scale); got != c.want {
			t.Errorf("Physical(%v) = %+v, quería %+v", c.scale, got, c.want)
		}
	}
}

// Dos vistas pegadas no tienen que dejar un hueco ni solaparse al redondear.
func TestBoundsPhysicalAdjacent(t *testing.T) {
	left := Bounds{X: 0, Y: 0, W: 333, H: 100}
	right := Bounds{X: 333, Y: 0, W: 333, H: 100}
	for _, s := range []float64{1, 1.25, 1.5, 1.75} {
		l, r := left.Physical(s), right.Physical(s)
		if l.X+l.W != r.X {
			t.Errorf("escala %v: la izquierda termina en %d y la derecha empieza en %d", s, l.X+l.W, r.X)
		}
	}
}
