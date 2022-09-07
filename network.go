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

type SocketOption struct {
	UserData      interface{}
	CloseCallBack func(error)
}

type Socket interface {
	SendWithDeadline([]byte, time.Time) (int, error)
	Send([]byte) (int, error)
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	SetUserData(ud interface{})
	GetUserData() interface{}
	GetUnderConn() interface{}
	Close(error)
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
