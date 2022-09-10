package network

import (
	"errors"
	//"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrRecvTimeout     error = errors.New("RecvTimeout")
	ErrSendTimeout     error = errors.New("SendTimeout")
	ErrAsynSendTimeout error = errors.New("ErrAsynSendTimeout")
	ErrSocketClosed    error = errors.New("SocketClosed")
	ErrSendBusy        error = errors.New("ErrSendBusy")
)

/*
 * 对Socket interface的异步封装
 * 提供异步recv和send处理
 */

type AsynSocketOption struct {
	Decoder          ObjDecoder
	Packer           ObjPacker
	CloseCallBack    func(*AsynSocket, error)
	SendChanSize     int
	HandlePakcet     func(*AsynSocket, interface{})
	OnEncodeError    func(*AsynSocket, error)
	AsyncSendTimeout time.Duration
}

type defaultPacker struct {
}

func (de *defaultPacker) Pack(buff []byte, o interface{}) ([]byte, error) {
	switch o.(type) {
	case []byte:
		return append(buff, o.([]byte)...), nil
	default:
		return buff, errors.New("o should be []byte")
	}
}

type defalutDecoder struct {
}

func (dd *defalutDecoder) Decode(buff []byte) (interface{}, error) {
	packet := make([]byte, len(buff))
	copy(packet, buff)
	return packet, nil
}

type AsynSocket struct {
	socket           Socket
	decoder          ObjDecoder
	packer           ObjPacker
	closeCh          chan struct{}
	recvRequestCh    chan time.Time
	sendRequestCh    chan interface{}
	recvOnce         sync.Once
	sendOnce         sync.Once
	routineCount     int32
	closeOnce        sync.Once
	closeReason      atomic.Value
	doCloseOnce      sync.Once
	closeCallBack    func(*AsynSocket, error)
	handlePakcet     func(*AsynSocket, interface{})
	onEncodeError    func(*AsynSocket, error)
	asyncSendTimeout time.Duration
	shutdownRead     int32
	sendError        int32
}

func NewAsynSocket(socket Socket, option AsynSocketOption) (*AsynSocket, error) {
	if nil == socket {
		return nil, errors.New("socket is nil")
	}

	if nil == option.HandlePakcet {
		return nil, errors.New("HandlePakcet is nil")
	}

	if option.SendChanSize <= 0 {
		option.SendChanSize = 1
	}

	s := &AsynSocket{
		socket:           socket,
		decoder:          option.Decoder,
		packer:           option.Packer,
		closeCallBack:    option.CloseCallBack,
		closeCh:          make(chan struct{}),
		recvRequestCh:    make(chan time.Time, 1),
		sendRequestCh:    make(chan interface{}, option.SendChanSize),
		handlePakcet:     option.HandlePakcet,
		asyncSendTimeout: option.AsyncSendTimeout,
		onEncodeError:    option.OnEncodeError,
	}

	if nil == s.decoder {
		s.decoder = &defalutDecoder{}
	}
	if nil == s.packer {
		s.packer = &defaultPacker{}
	}
	if nil == s.closeCallBack {
		s.closeCallBack = func(*AsynSocket, error) {
		}
	}
	if nil == s.onEncodeError {
		s.onEncodeError = func(*AsynSocket, error) {
		}
	}

	return s, nil
}

func (s *AsynSocket) LocalAddr() net.Addr {
	return s.socket.LocalAddr()
}

func (s *AsynSocket) RemoteAddr() net.Addr {
	return s.socket.RemoteAddr()
}

func (s *AsynSocket) SetUserData(ud interface{}) {
	s.socket.SetUserData(ud)
}

func (s *AsynSocket) GetUserData() interface{} {
	return s.socket.GetUserData()
}

func (s *AsynSocket) GetUnderConn() interface{} {
	return s.socket.GetUnderConn()
}

func (s *AsynSocket) doCloseCallback() {
	s.doCloseOnce.Do(func() {
		s.socket.Close()
		reason := s.closeReason.Load()
		if nil != reason {
			s.closeCallBack(s, reason.(error))
		} else {
			s.closeCallBack(s, nil)
		}
	})
}

func (s *AsynSocket) Close(err error) {
	s.closeOnce.Do(func() {
		atomic.AddInt32(&s.routineCount, 1)
		if nil != err {
			s.closeReason.Store(err)
		}
		atomic.StoreInt32(&s.shutdownRead, 1)
		close(s.closeCh)
		if 0 == atomic.AddInt32(&s.routineCount, -1) {
			s.doCloseCallback()
		}
	})
}

func (s *AsynSocket) getTimeout(timeout []time.Duration) time.Duration {
	if len(timeout) > 0 {
		return timeout[0]
	} else {
		return 0
	}
}

//发起异步读请求
func (s *AsynSocket) Recv(timeout ...time.Duration) {
	deadline := time.Time{}
	if t := s.getTimeout(timeout); t > 0 {
		deadline = time.Now().Add(t)
	}
	select {
	case <-s.closeCh:
	case s.recvRequestCh <- deadline:
		s.recvOnce.Do(s.recvloop)
	default:
	}
}

func (s *AsynSocket) recvloop() {
	atomic.AddInt32(&s.routineCount, 1)
	go func() {
		defer func() {
			if atomic.AddInt32(&s.routineCount, -1) == 0 {
				s.doCloseCallback()
			}
		}()

		var (
			buff   []byte
			err    error
			packet interface{}
		)

		for {
			select {
			case <-s.closeCh:
				return
			case deadline := <-s.recvRequestCh:
				buff, err = s.socket.Recv(deadline)
				if atomic.LoadInt32(&s.shutdownRead) == 1 {
					return
				} else {
					if nil == err {
						packet, err = s.decoder.Decode(buff)
					}
					if nil == err {
						s.handlePakcet(s, packet)
					} else {
						if IsNetTimeoutError(err) {
							err = ErrRecvTimeout
						}
						s.Close(err)
					}
				}
			}
		}
	}()
}

func (s *AsynSocket) send(buff []byte) error {
	deadline := time.Time{}
	if s.asyncSendTimeout > 0 {
		deadline = time.Now().Add(s.asyncSendTimeout)
	}
	if _, err := s.socket.Send(buff, deadline); nil != err {
		atomic.StoreInt32(&s.sendError, 1)
		if IsNetTimeoutError(err) {
			err = ErrAsynSendTimeout
		}
		s.Close(err)
		return err
	} else {
		return nil
	}
}

func (s *AsynSocket) sendloop() {
	const maxSendBlockSize int = 65535
	atomic.AddInt32(&s.routineCount, 1)
	go func() {
		var buff []byte
		var err error
		defer func() {
			if atomic.AddInt32(&s.routineCount, -1) == 0 {
				s.doCloseCallback()
			}
		}()
		for {
			select {
			case <-s.closeCh:
				if atomic.LoadInt32(&s.sendError) == 0 {
					//在没有错误的情况下尝试将sendRequestCh中的内容发送完
					for len(s.sendRequestCh) > 0 {
						o := <-s.sendRequestCh
						size := len(buff)
						if buff, err = s.packer.Pack(buff, o); nil != err {
							s.onEncodeError(s, err)
							buff = buff[:size]
						} else if len(buff) >= maxSendBlockSize {
							if nil != s.send(buff) {
								return
							} else {
								buff = buff[:0]
							}
						}
					}

					if len(buff) > 0 {
						s.send(buff)
					}
				}
				return
			case o := <-s.sendRequestCh:
				size := len(buff)
				if buff, err = s.packer.Pack(buff, o); nil != err {
					s.onEncodeError(s, err)
					buff = buff[:size]
				} else if len(buff) >= maxSendBlockSize || len(s.sendRequestCh) == 0 {
					s.send(buff)
					buff = buff[:0]
				}
			}
		}
	}()
}

//异步投递

//在单独的goroutine encode与发送

//timeout > 0:最多阻塞到timeout,如果无法投递返回ErrPushTimeout

//timeout == 0:永久阻塞，知道投递成功或socket被关闭

//timeout < 0:如果无法投递返回ErrPushBusy

func (s *AsynSocket) Send(o interface{}, timeout ...time.Duration) error {
	s.sendOnce.Do(s.sendloop)
	if t := s.getTimeout(timeout); t == 0 {
		//一直等待
		select {
		case <-s.closeCh:
			return ErrSocketClosed
		case s.sendRequestCh <- o:
			return nil
		}
	} else if t > 0 {
		//等待到deadline
		ticker := time.NewTicker(t)
		defer ticker.Stop()
		select {
		case <-s.closeCh:
			return ErrSocketClosed
		case <-ticker.C:
			return ErrSendTimeout
		case s.sendRequestCh <- o:
			return nil
		}
	} else {
		//如果无法投入队列返回busy
		select {
		case <-s.closeCh:
			return ErrSocketClosed
		case s.sendRequestCh <- o:
			return nil
		default:
			return ErrSendBusy
		}
	}
}
