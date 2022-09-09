package network

import (
	"errors"
	gorilla "github.com/gorilla/websocket"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type webSocket struct {
	userData  atomic.Value
	conn      *gorilla.Conn
	closeOnce sync.Once
}

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
	if ud := wc.userData.Load(); nil == ud {
		return nil
	} else {
		return ud.(userdata).data
	}
}

func (wc *webSocket) GetUnderConn() interface{} {
	return wc.conn
}

func (wc *webSocket) Close() {
	wc.closeOnce.Do(func() {
		runtime.SetFinalizer(wc, nil)
		wc.conn.SetWriteDeadline(time.Now().Add(time.Second))
		wc.conn.WriteMessage(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, ""))
		wc.conn.Close()
	})
}

func (wc *webSocket) Send(data []byte, deadline ...time.Time) (int, error) {
	var err error
	if len(deadline) > 0 && !deadline[0].IsZero() {
		wc.conn.SetWriteDeadline(deadline[0])
		err = wc.conn.WriteMessage(gorilla.BinaryMessage, data)
		wc.conn.SetWriteDeadline(time.Time{})
	} else {
		err = wc.conn.WriteMessage(gorilla.BinaryMessage, data)
	}
	if nil == err {
		return len(data), nil
	} else {
		return 0, err
	}
}

func (wc *webSocket) Recv(deadline ...time.Time) ([]byte, error) {
	var (
		packet []byte
		err    error
	)
	if len(deadline) > 0 && !deadline[0].IsZero() {
		wc.conn.SetReadDeadline(deadline[0])
		_, packet, err = wc.conn.ReadMessage()
		wc.conn.SetReadDeadline(time.Time{})
	} else {
		_, packet, err = wc.conn.ReadMessage()
	}
	return packet, err
}

func NewWebSocket(conn *gorilla.Conn) (Socket, error) {
	if nil == conn {
		return nil, errors.New("conn is nil")
	}

	s := &webSocket{
		conn: conn,
	}

	runtime.SetFinalizer(s, func(s *webSocket) {
		s.Close()
	})

	return s, nil
}
