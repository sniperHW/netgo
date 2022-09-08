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
}

type PacketReceiver interface {
	Recv(ReadAble) ([]byte, error)
}

type Socket interface {
	Send([]byte) (int, error)
	Recv() ([]byte, error)
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	SetUserData(ud interface{})
	GetUserData() interface{}
	GetUnderConn() interface{}
	Close()
	SetSendDeadline(time.Time)
	SetRecvDeadline(time.Time)
}

type userdata struct {
	data interface{}
}

type defaultPacketReceiver struct {
	recvbuf []byte
}

func (dr *defaultPacketReceiver) Recv(r ReadAble) ([]byte, error) {
	n, err := r.Read(dr.recvbuf)
	return dr.recvbuf[:n], err
}
