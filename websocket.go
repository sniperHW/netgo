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

type WsRecvOption struct {
	RecvTimeout   time.Duration
	OnRecvTimeout func()
	OnPacket      func([]byte)
	PingHandler   func(string) error
	PongHandler   func(string) error
}

type WebSocket struct {
	mu            sync.Mutex
	userData      atomic.Value
	closeReason   atomic.Value
	conn          *gorilla.Conn
	closeCallBack func(error)
	closed        int32
	started       bool
	buff          []byte
}

func (wc *WebSocket) LocalAddr() net.Addr {
	return wc.conn.LocalAddr()
}

func (wc *WebSocket) RemoteAddr() net.Addr {
	return wc.conn.RemoteAddr()
}

func (wc *WebSocket) SetUserData(ud interface{}) {
	wc.userData.Store(userdata{
		data: ud,
	})
}

func (wc *WebSocket) GetUserData() interface{} {
	if ud := wc.userData.Load(); nil == ud {
		return nil
	} else {
		return ud.(userdata).data
	}
}

func (wc *WebSocket) GetUnderConn() interface{} {
	return wc.conn
}

func (wc *WebSocket) getCloseReason() error {
	r := wc.closeReason.Load()
	if nil == r {
		return nil
	} else {
		return r.(error)
	}
}

func (wc *WebSocket) Close(reason error) {
	doCloseCallback := false
	wc.mu.Lock()
	if atomic.CompareAndSwapInt32(&wc.closed, 0, 1) {
		runtime.SetFinalizer(wc, nil)
		if nil != reason {
			wc.closeReason.Store(reason)
		}
		if !wc.started {
			doCloseCallback = true
		}
	}
	wc.mu.Unlock()

	go func() {
		//防止WriteMessage阻塞太久设置写超时
		wc.conn.SetWriteDeadline(time.Now().Add(time.Second))
		wc.conn.WriteMessage(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, ""))
		wc.conn.Close()
		if doCloseCallback {
			wc.closeCallBack(reason)
		}
	}()
}

func (wc *WebSocket) recvloop(o WsRecvOption) {
	defer func() {
		wc.closeCallBack(wc.getCloseReason())
	}()
	for atomic.LoadInt32(&wc.closed) == 0 {
		if o.RecvTimeout > 0 {
			wc.conn.SetReadDeadline(time.Now().Add(o.RecvTimeout))
		}
		_, packet, err := wc.conn.ReadMessage()
		if nil != err && atomic.LoadInt32(&wc.closed) == 0 {
			if IsNetTimeoutError(err) && nil != o.OnRecvTimeout {
				o.OnRecvTimeout()
			} else {
				wc.Close(err)
			}
		} else if len(packet) > 0 {
			o.OnPacket(packet)
		}
	}
}

func (wc *WebSocket) SendWithDeadline(data []byte, deadline time.Time) (int, error) {
	if atomic.LoadInt32(&wc.closed) != 0 {
		return 0, errors.New("socket closed")
	} else {
		wc.conn.SetWriteDeadline(deadline)
		err := wc.conn.WriteMessage(gorilla.BinaryMessage, data)
		wc.conn.SetWriteDeadline(time.Time{})
		if nil == err {
			return len(data), nil
		} else {
			return 0, err
		}
	}
}

func (wc *WebSocket) Send(data []byte) (int, error) {
	if atomic.LoadInt32(&wc.closed) != 0 {
		return 0, errors.New("socket closed")
	} else {
		err := wc.conn.WriteMessage(gorilla.BinaryMessage, data)
		if nil == err {
			return len(data), nil
		} else {
			return 0, err
		}
	}
}

func (wc *WebSocket) StartRecv(o WsRecvOption) error {
	if nil == o.OnPacket {
		return errors.New("OnPacket shouldn`t be nil")
	}

	wc.mu.Lock()
	defer wc.mu.Unlock()
	if atomic.LoadInt32(&wc.closed) == 0 {
		wc.started = true
		go wc.recvloop(o)
		return nil
	} else {
		return errors.New("socket closed")
	}
}

func NewWebSocket(conn *gorilla.Conn, o SocketOption) (Socket, error) {
	if nil == conn {
		return nil, errors.New("conn is nil")
	}

	if o.CloseCallBack == nil {
		o.CloseCallBack = func(error) {}
	}

	s := &WebSocket{
		conn:          conn,
		closeCallBack: o.CloseCallBack,
	}

	s.SetUserData(o.UserData)

	conn.SetCloseHandler(func(code int, text string) error {
		s.Close(nil)
		return nil
	})

	runtime.SetFinalizer(s, func(s *WebSocket) {
		s.Close(errors.New("gc"))
	})

	return s, nil
}
