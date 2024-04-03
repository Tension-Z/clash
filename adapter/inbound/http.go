package inbound

import (
	"net"
	"net/netip"

	C "github.com/Tension-Z/clash/constant"
	"github.com/Tension-Z/clash/context"
	"github.com/Tension-Z/clash/transport/socks5"
)

// NewHTTP receive normal http request and return HTTPContext
func NewHTTP(target socks5.Addr, source net.Addr, originTarget net.Addr, conn net.Conn) *context.ConnContext {
	metadata := parseSocksAddr(target)
	metadata.NetWork = C.TCP
	metadata.Type = C.HTTP
	if ip, port, err := parseAddr(source); err == nil {
		metadata.SrcIP = ip
		metadata.SrcPort = C.Port(port)
	}
	if originTarget != nil {
		if addrPort, err := netip.ParseAddrPort(originTarget.String()); err == nil {
			metadata.OriginDst = addrPort
		}
	}
	return context.NewConnContext(conn, metadata)
}
