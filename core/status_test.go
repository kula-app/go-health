package core

import (
	"net/http"
	"testing"
)

func TestStatus_HTTPStatusCode(t *testing.T) {
	tests := []struct {
		name string
		s    Status
		want int
	}{
		{"pass returns 200", StatusPass, http.StatusOK},
		{"warn returns 200", StatusWarn, http.StatusOK},
		{"fail returns 503", StatusFail, http.StatusServiceUnavailable},
		{"unknown returns 200", Status("unknown"), http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.HTTPStatusCode(); got != tt.want {
				t.Errorf("HTTPStatusCode() = %d, want %d", got, tt.want)
			}
		})
	}
}
