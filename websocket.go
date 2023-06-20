package netgo

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	gorilla "github.com/gorilla/websocket"
)

type webSocketReadable struct {
	conn *gorilla.Conn
	msg  []byte
}

func (readable *webSocketReadable) Read(buff []byte) (int, error) {
	var err error
	remain := len(readable.msg)
	if remain == 0 {
		_, readable.msg, err = readable.conn.ReadMessage()
		if nil != err {
			return 0, err
		} else {
			remain = len(readable.msg)
		}
	}

	if remain > len(buff) {
		copy(buff, readable.msg[:len(buff)])
		readable.msg = readable.msg[len(buff):]
		return len(buff), nil
	} else {
		copy(buff, readable.msg)
		readable.msg = nil
		return remain, nil
	}
}

func (readable *webSocketReadable) SetReadDeadline(deadline time.Time) error {
	return readable.conn.SetReadDeadline(deadline)
}

type webSocket struct {
	userData       atomic.Value
	packetReceiver PacketReceiver
	conn           *gorilla.Conn
	closeOnce      sync.Once
	readableObj    *webSocketReadable
}

var _ Socket = &webSocket{}

func (wc *webSocket) LocalAddr() net.Addr {
	return wc.conn.LocalAddr()
}

func (wc *webSocket) RemoteAddr() net.Addr {
	return wc.conn.RemoteAddr()
}

func (wc *webSocket) SetUserData(ud interface{}) {
	wc.userData.Store(userdata{
		data: ud,
	})
}

func (wc *webSocket) GetUserData() interface{} {
	if ud, ok := wc.userData.Load().(userdata); ok {
		return ud.data
	} else {
		return nil
	}
}

func (wc *webSocket) GetUnderConn() interface{} {
	return wc.conn
}

func (wc *webSocket) Close() {
	wc.closeOnce.Do(func() {
		wc.conn.SetWriteDeadline(time.Now().Add(time.Second))
		wc.conn.WriteMessage(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, ""))
		wc.conn.Close()
	})
}

func (wc *webSocket) Send(data []byte, deadline ...time.Time) (int, error) {
	var err error
	d := time.Time{}
	if len(deadline) > 0 && !deadline[0].IsZero() {
		d = deadline[0]
	}

	if err = wc.conn.SetWriteDeadline(d); err != nil {
		return 0, err
	} else if err = wc.conn.WriteMessage(gorilla.BinaryMessage, data); err != nil {
		return 0, err
	} else {
		return len(data), nil
	}
}

func (wc *webSocket) Recv(deadline ...time.Time) (packet []byte, err error) {
	if nil == wc.readableObj {
		d := time.Time{}
		if len(deadline) > 0 && !deadline[0].IsZero() {
			d = deadline[0]
		}
		if err = wc.conn.SetReadDeadline(d); err != nil {
			return
		} else {
			_, packet, err = wc.conn.ReadMessage()
		}
	} else {
		if len(deadline) > 0 && !deadline[0].IsZero() {
			packet, err = wc.packetReceiver.Recv(wc.readableObj, deadline[0])
		} else {
			packet, err = wc.packetReceiver.Recv(wc.readableObj, time.Time{})
		}
	}
	return
}

func NewWebSocket(conn *gorilla.Conn, packetReceiver ...PacketReceiver) Socket {
	s := &webSocket{
		conn: conn,
	}

	if len(packetReceiver) > 0 && packetReceiver[0] != nil {
		s.packetReceiver = packetReceiver[0]
		s.readableObj = &webSocketReadable{
			conn: conn,
		}
	}

	return s
}
