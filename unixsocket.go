package netgo

import (
	"net"
)

type unixSocket struct {
	socketBase
}

var _ Socket = &unixSocket{}

func (tc *unixSocket) GetUnderConn() interface{} {
	return tc.conn.(*net.UnixConn)
}

func NewUnixSocket(conn *net.UnixConn, packetReceiver ...PacketReceiver) Socket {
	s := &unixSocket{}
	s.init(conn, packetReceiver...)
	return s
}
