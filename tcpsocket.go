package network

import (
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type TcpSocket struct {
	userData       atomic.Value
	packetReceiver PacketReceiver
	conn           net.Conn
	closeOnce      sync.Once
}

func (tc *TcpSocket) SetSendDeadline(deadline time.Time) {
	tc.conn.SetWriteDeadline(deadline)
}

func (tc *TcpSocket) SetRecvDeadline(deadline time.Time) {
	tc.conn.SetReadDeadline(deadline)
}

func (tc *TcpSocket) LocalAddr() net.Addr {
	return tc.conn.LocalAddr()
}

func (tc *TcpSocket) RemoteAddr() net.Addr {
	return tc.conn.RemoteAddr()
}

func (tc *TcpSocket) SetUserData(ud interface{}) {
	tc.userData.Store(userdata{
		data: ud,
	})
}

func (tc *TcpSocket) GetUserData() interface{} {
	if ud := tc.userData.Load(); nil == ud {
		return nil
	} else {
		return ud.(userdata).data
	}
}

func (tc *TcpSocket) GetUnderConn() interface{} {
	return tc.conn
}

func (tc *TcpSocket) Close() {
	tc.closeOnce.Do(func() {
		runtime.SetFinalizer(tc, nil)
		tc.conn.Close()
	})
}

func (tc *TcpSocket) Send(data []byte) (int, error) {
	return tc.conn.Write(data)
}

func (tc *TcpSocket) Recv() ([]byte, error) {
	return tc.packetReceiver.Recv(tc.conn)
}

func NewTcpSocket(conn net.Conn, packetReceiver ...PacketReceiver) (Socket, error) {
	if nil == conn {
		return nil, errors.New("conn is nil")
	}

	switch conn.(type) {
	case *net.TCPConn:
	default:
		return nil, errors.New("conn should be TCPConn")
	}

	s := &TcpSocket{
		conn: conn,
	}

	if len(packetReceiver) == 0 || packetReceiver[0] == nil {
		s.packetReceiver = &defaultPacketReceiver{
			recvbuf: make([]byte, 65535),
		}
	} else {
		s.packetReceiver = packetReceiver[0]
	}

	runtime.SetFinalizer(s, func(s *TcpSocket) {
		s.Close()
	})

	return s, nil
}
