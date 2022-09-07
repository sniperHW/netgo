package network

import (
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type TcpRecvOption struct {
	RecvTimeout   time.Duration
	Receiver      PacketReceiver
	OnRecvTimeout func()
	OnPacket      func([]byte)
}

type TcpSocket struct {
	mu            sync.Mutex
	userData      atomic.Value
	closeReason   atomic.Value
	conn          net.Conn
	closeCallBack func(error)
	closed        int32
	started       bool
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

func (tc *TcpSocket) getCloseReason() error {
	r := tc.closeReason.Load()
	if nil == r {
		return nil
	} else {
		return r.(error)
	}
}

func (tc *TcpSocket) Close(reason error) {
	doCloseCallback := false
	tc.mu.Lock()
	if atomic.CompareAndSwapInt32(&tc.closed, 0, 1) {
		runtime.SetFinalizer(tc, nil)
		if nil != reason {
			tc.closeReason.Store(reason)
		}
		tc.conn.Close()
		if !tc.started {
			doCloseCallback = true
		}
	}
	tc.mu.Unlock()
	if doCloseCallback {
		tc.closeCallBack(reason)
	}
}

func (tc *TcpSocket) recvloop(o TcpRecvOption) {
	defer func() {
		tc.closeCallBack(tc.getCloseReason())
	}()
	for atomic.LoadInt32(&tc.closed) == 0 {
		if o.RecvTimeout > 0 {
			tc.conn.SetReadDeadline(time.Now().Add(o.RecvTimeout))
		}
		packet, err := o.Receiver.Recv(tc.conn)
		if nil != err && atomic.LoadInt32(&tc.closed) == 0 {
			if IsNetTimeoutError(err) && nil != o.OnRecvTimeout {
				o.OnRecvTimeout()
			} else {
				tc.Close(err)
			}
		} else if len(packet) > 0 {
			o.OnPacket(packet)
		}
	}
}

func (tc *TcpSocket) SendWithDeadline(data []byte, deadline time.Time) (int, error) {
	tc.conn.SetWriteDeadline(deadline)
	n, err := tc.conn.Write(data)
	tc.conn.SetWriteDeadline(time.Time{})
	return n, err
}

func (tc *TcpSocket) Send(data []byte) (int, error) {
	return tc.conn.Write(data)
}

func (tc *TcpSocket) StartRecv(o TcpRecvOption) error {
	if nil == o.OnPacket {
		return errors.New("OnPacket shouldn`t be nil")
	}

	if o.Receiver == nil {
		o.Receiver = &defaultPacketReceiver{
			recvbuf: make([]byte, 65535),
		}
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()
	if atomic.LoadInt32(&tc.closed) == 0 {
		tc.started = true
		go tc.recvloop(o)
		return nil
	} else {
		return errors.New("socket closed")
	}
}

func NewTcpSocket(conn net.Conn, o SocketOption) (Socket, error) {
	if nil == conn {
		return nil, errors.New("conn is nil")
	}

	switch conn.(type) {
	case *net.TCPConn:
	default:
		return nil, errors.New("conn should be TCPConn")
	}

	if o.CloseCallBack == nil {
		o.CloseCallBack = func(error) {}
	}

	s := &TcpSocket{
		conn:          conn,
		closeCallBack: o.CloseCallBack,
	}

	s.SetUserData(o.UserData)

	runtime.SetFinalizer(s, func(s *TcpSocket) {
		s.Close(errors.New("gc"))
	})

	return s, nil
}
