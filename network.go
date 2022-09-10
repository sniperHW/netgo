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

type ObjDecoder interface {
	//将[]byte解码成对象

	//如果成功返回对象,否则返回错误

	Decode([]byte) (interface{}, error)
}

type ObjPacker interface {

	//将对象编码,打包成一个网络包

	//如果成功返回添了对象编码的[]byte,否则返回错误

	Pack([]byte, interface{}) ([]byte, error)
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
}

func (dr *defaultPacketReceiver) Recv(r ReadAble, deadline time.Time) ([]byte, error) {
	var (
		n   int
		err error
	)

	buff := make([]byte, 4096)

	if deadline.IsZero() {
		r.SetReadDeadline(time.Time{})
		n, err = r.Read(buff)
	} else {
		r.SetReadDeadline(deadline)
		n, err = r.Read(buff)
	}
	return buff[:n], err
}
