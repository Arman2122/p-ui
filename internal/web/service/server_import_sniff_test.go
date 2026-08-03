package service

import "testing"

func TestIsPgDumpHeader(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
		want   bool
	}{
		{"pg custom archive", []byte("PGDMP\x01\x10\x04"), true},
		{"plain-format postgres dump", []byte("--\n-- PostgreSQL database dump\n--"), false},
		{"truncated magic", []byte("PGD"), false},
		{"empty file", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPgDumpHeader(tc.header); got != tc.want {
				t.Errorf("isPgDumpHeader(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}
