package netgo

import (
	"net"
	"time"
)

type tcpSocket struct {
	socketBase
}

func (tc *tcpSocket) SendBuffers(buffs net.Buffers, deadline ...time.Time) (int64, error) {
	d := time.Time{}
	if len(deadline) > 0 && !deadline[0].IsZero() {
		d = deadline[0]
	}
	if err := tc.conn.SetWriteDeadline(d); err != nil {
		return 0, err
	} else {
		return buffs.WriteTo(tc.conn.(*net.TCPConn))
	}
}

var _ Socket = &tcpSocket{}

func NewTcpSocket(conn *net.TCPConn, packetReceiver ...PacketReceiver) Socket {
	s := &tcpSocket{}
	s.init(conn, packetReceiver...)
	return s
}
