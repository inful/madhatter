package rota

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestModNonNegative pins the safe-modulo arithmetic used by the rotation
// engine. The function is called with negative indices when "walking back"
// around the rotation ring; the underlying Go `%` operator returns a
// negative remainder for negative dividends, which would otherwise index
// out of bounds.
func TestModNonNegative(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		x, m int
		want int
	}{
		{"positive dividend", 5, 3, 2},
		{"exact multiple", 6, 3, 0},
		{"zero dividend", 0, 3, 0},
		{"negative dividend one step back", -1, 3, 2},
		{"negative dividend two steps back", -2, 3, 1},
		{"negative exact multiple", -3, 3, 0},
		{"negative dividend more than one cycle", -4, 3, 2},
		{"large negative dividend", -10, 4, 2},
		{"modulus one always zero", 7, 1, 0},
		{"dividend larger than modulus", 11, 5, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := modNonNegative(tc.x, tc.m)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestModNonNegative_ZeroModulus documents the divide-by-zero guard: the
// implementation does not panic but Go's runtime will on `m == 0`.
// Pin the panic so a future refactor that swaps in `int % int` directly
// still surfaces the bug loudly.
func TestModNonNegative_ZeroModulus(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		_ = modNonNegative(5, 0)
	}, "modulo by zero must panic; a silent zero-result would corrupt the rotation ring")
}
