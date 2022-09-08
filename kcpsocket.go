package network

import (
	"errors"
	"github.com/xtaci/kcp-go/v5"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type kcpSocket struct {
	userData       atomic.Value
	packetReceiver PacketReceiver
	conn           *kcp.UDPSession
	closeOnce      sync.Once
}

func (kc *kcpSocket) SetSendDeadline(deadline time.Time) {
	kc.conn.SetWriteDeadline(deadline)
}

func (kc *kcpSocket) SetRecvDeadline(deadline time.Time) {
	kc.conn.SetReadDeadline(deadline)
}

func (kc *kcpSocket) LocalAddr() net.Addr {
	return kc.conn.LocalAddr()
}

func (kc *kcpSocket) RemoteAddr() net.Addr {
	return kc.conn.RemoteAddr()
}

func (kc *kcpSocket) SetUserData(ud interface{}) {
	kc.userData.Store(userdata{
		data: ud,
	})
}

func (kc *kcpSocket) GetUserData() interface{} {
	if ud := kc.userData.Load(); nil == ud {
		return nil
	} else {
		return ud.(userdata).data
	}
}

func (kc *kcpSocket) GetUnderConn() interface{} {
	return kc.conn
}

func (kc *kcpSocket) Close() {
	kc.closeOnce.Do(func() {
		runtime.SetFinalizer(kc, nil)
		kc.conn.Close()
	})
}

func (kc *kcpSocket) Send(data []byte) (int, error) {
	return kc.conn.Write(data)
}

func (kc *kcpSocket) Recv() ([]byte, error) {
	return kc.packetReceiver.Recv(kc.conn)
}

func NewKcpSocket(conn *kcp.UDPSession, packetReceiver ...PacketReceiver) (Socket, error) {
	if nil == conn {
		return nil, errors.New("conn is nil")
	}

	s := &kcpSocket{
		conn: conn,
	}

	if len(packetReceiver) == 0 || packetReceiver[0] == nil {
		s.packetReceiver = &defaultPacketReceiver{
			recvbuf: make([]byte, 65535),
		}
	} else {
		s.packetReceiver = packetReceiver[0]
	}

	runtime.SetFinalizer(s, func(s *kcpSocket) {
		s.Close()
	})

	return s, nil
}
