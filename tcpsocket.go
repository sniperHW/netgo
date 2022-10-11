package netgo

import (
	"net"
)

type tcpSocket struct {
	socketBase
}

func (tc *tcpSocket) GetUnderConn() interface{} {
	return tc.conn.(*net.TCPConn)
}

var _ Socket = &tcpSocket{}

func NewTcpSocket(conn *net.TCPConn, packetReceiver ...PacketReceiver) Socket {
	s := &tcpSocket{}
	s.init(conn, packetReceiver...)
	return s
}
