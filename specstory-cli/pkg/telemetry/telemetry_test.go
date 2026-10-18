package telemetry

import (
	"net"
	"testing"
	"time"
)

func TestEndpointReachable(t *testing.T) {
	// Real listener for the reachable case.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	liveAddr := listener.Addr().String()

	// A port that was just released gives us a deterministic "nothing listening"
	// address without guessing port numbers.
	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start throwaway listener: %v", err)
	}
	closedAddr := closedListener.Addr().String()
	_ = closedListener.Close()

	tests := []struct {
		name string
		host string
		want bool
	}{
		{
			name: "listening endpoint is reachable",
			host: liveAddr,
			want: true,
		},
		{
			name: "closed port is unreachable",
			host: closedAddr,
			want: false,
		},
		{
			name: "malformed host is unreachable",
			host: "not a host:port",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			got := endpointReachable(tt.host)
			elapsed := time.Since(start)

			if got != tt.want {
				t.Errorf("endpointReachable(%q) = %v, want %v", tt.host, got, tt.want)
			}
			// The probe must never block much past its timeout — that would
			// reintroduce the startup stall this check exists to prevent.
			if elapsed > endpointDialTimeout+200*time.Millisecond {
				t.Errorf("endpointReachable(%q) took %v, expected under %v", tt.host, elapsed, endpointDialTimeout)
			}
		})
	}
}
