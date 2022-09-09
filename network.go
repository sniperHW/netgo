package network

import (
	"net"
	"time"
)

func IsNetTimeoutError(err error) bool {
	switch err.(type) {
	case net.Error:
		if err.(net.Error).Timeout() {
			return true
		}
	default:
	}
	return false
}

type ReadAble interface {
	Read([]byte) (int, error)
	SetReadDeadline(time.Time) error
}

type PacketReceiver interface {
	Recv(ReadAble, time.Time) ([]byte, error)
}

type Socket interface {
	Send([]byte, ...time.Time) (int, error)
	Recv(...time.Time) ([]byte, error)
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	SetUserData(ud interface{})
	GetUserData() interface{}
	GetUnderConn() interface{}
	Close()
}

type userdata struct {
	data interface{}
}

type defaultPacketReceiver struct {
	recvbuf []byte
}

func (dr *defaultPacketReceiver) Recv(r ReadAble, deadline time.Time) ([]byte, error) {
	var (
		n   int
		err error
	)

	if deadline.IsZero() {
		n, err = r.Read(dr.recvbuf)
	} else {
		r.SetReadDeadline(deadline)
		n, err = r.Read(dr.recvbuf)
		r.SetReadDeadline(time.Time{})
	}
	return dr.recvbuf[:n], err
}
