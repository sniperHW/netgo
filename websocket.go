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

func (wc *webSocket) SetSendDeadline(deadline time.Time) {
	wc.conn.SetWriteDeadline(deadline)
}

func (wc *webSocket) SetRecvDeadline(deadline time.Time) {
	wc.conn.SetReadDeadline(deadline)
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

func (wc *webSocket) Send(data []byte) (int, error) {
	err := wc.conn.WriteMessage(gorilla.BinaryMessage, data)
	if nil == err {
		return len(data), nil
	} else {
		return 0, err
	}
}

func (wc *webSocket) Recv() ([]byte, error) {
	_, packet, err := wc.conn.ReadMessage()
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
