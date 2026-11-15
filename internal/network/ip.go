package network

import "net"

// GetOutboundIP returns the preferred local IP address of this machine for LAN access.
func GetOutboundIP() string {
	// Attempt 1: Dial UDP to get preferred outbound interface IP
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
		if ok && !localAddr.IP.IsLoopback() && localAddr.IP.To4() != nil {
			return localAddr.IP.String()
		}
	}

	// Attempt 2: Iterate local interfaces for active, non-loopback IPv4 address
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip != nil && !ip.IsLoopback() && ip.To4() != nil {
					return ip.String()
				}
			}
		}
	}

	return "127.0.0.1"
}
