package qdisc

import "testing"

func TestIsKataTapDevice(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"exact match", "tap0_kata", true},
		{"double digit", "tap10_kata", true},
		{"missing digit", "tap_kata", false},
		{"wrong suffix", "tap0_qemu", false},
		{"prefix only", "tap0", false},
		{"embedded", "xtap0_kata", false},
		{"trailing", "tap0_katax", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsKataTapDevice(tc.in); got != tc.want {
				t.Fatalf("IsKataTapDevice(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
