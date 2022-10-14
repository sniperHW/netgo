package netgo

import (
	"net"
	"time"
)

type unixSocket struct {
	socketBase
}

var _ Socket = &unixSocket{}

func (tc *unixSocket) SendBuffers(buffs net.Buffers, deadline ...time.Time) (int64, error) {
	d := time.Time{}
	if len(deadline) > 0 && !deadline[0].IsZero() {
		d = deadline[0]
	}
	if err := tc.conn.SetWriteDeadline(d); err != nil {
		return 0, err
	} else {
		return buffs.WriteTo(tc.conn.(*net.UnixConn))
	}
}

func NewUnixSocket(conn *net.UnixConn, packetReceiver ...PacketReceiver) Socket {
	s := &unixSocket{}
	s.init(conn, packetReceiver...)
	return s
}
