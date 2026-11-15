package network_test

import (
	"testing"

	"netsathi/internal/network"
)

func TestGetOutboundIP(t *testing.T) {
	ip := network.GetOutboundIP()
	if ip == "" {
		t.Error("expected non-empty outbound IP")
	}
}

