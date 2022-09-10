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

type AsynSocketOption struct {
	Decoder          ObjDecoder
	Packer           ObjPacker
	CloseCallBack    func(*AsynSocket, error)
	SendChanSize     int
	HandlePakcet     func(*AsynSocket, interface{})
	OnRecvTimeout    func(*AsynSocket)
	OnEncodeError    func(*AsynSocket, error)
	AsyncSendTimeout time.Duration
}

type defaultPacker struct {
}

func (de *defaultPacker) Pack(buff []byte, o interface{}) ([]byte, error) {
	if b, ok := o.([]byte); ok {
		return append(buff, b...), nil
	} else {
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

//asynchronize encapsulation for Socket
type AsynSocket struct {
	socket           Socket
	decoder          ObjDecoder
	packer           ObjPacker
	die              chan struct{}
	recvReq          chan time.Time
	sendReq          chan interface{}
	recvOnce         sync.Once
	sendOnce         sync.Once
	routineCount     int32
	closeOnce        sync.Once
	closeReason      atomic.Value
	doCloseOnce      sync.Once
	closeCallBack    func(*AsynSocket, error) //call when routineCount trun to zero
	handlePakcet     func(*AsynSocket, interface{})
	onEncodeError    func(*AsynSocket, error)
	onRecvTimeout    func(*AsynSocket)
	asyncSendTimeout time.Duration
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
		die:              make(chan struct{}),
		recvReq:          make(chan time.Time, 1),
		sendReq:          make(chan interface{}, option.SendChanSize),
		handlePakcet:     option.HandlePakcet,
		asyncSendTimeout: option.AsyncSendTimeout,
		onEncodeError:    option.OnEncodeError,
		onRecvTimeout:    option.OnRecvTimeout,
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
	if nil == s.onRecvTimeout {
		s.onRecvTimeout = func(*AsynSocket) {
			s.Close(ErrRecvTimeout)
		}
	}

	return s, nil
}

func (s *AsynSocket) GetUnderSocket() Socket {
	return s.socket
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
		atomic.AddInt32(&s.routineCount, 1) //add 1,to prevent recvloop and sendloop call closeCallBack
		if nil != err {
			s.closeReason.Store(err)
		}
		close(s.die)
		if atomic.AddInt32(&s.routineCount, -1) == 0 {
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

// send a asynchronize recv request
//
// if there is a packet received before timeout,handlePakcet would be call with packet as a parameter
//
// if recevie timeout,on onReceTimeout would be call
//
// if recvReq is full,drop the request
func (s *AsynSocket) Recv(timeout ...time.Duration) {
	s.recvOnce.Do(s.recvloop)
	deadline := time.Time{}
	if t := s.getTimeout(timeout); t > 0 {
		deadline = time.Now().Add(t)
	}
	select {
	case <-s.die:
	case s.recvReq <- deadline:
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
			case <-s.die:
				return
			case deadline := <-s.recvReq:
				buff, err = s.socket.Recv(deadline)
				select {
				case <-s.die:
					return
				default:
					if nil == err {
						packet, err = s.decoder.Decode(buff)
					}
					if nil == err {
						s.handlePakcet(s, packet)
					} else {
						if IsNetTimeoutError(err) {
							s.onRecvTimeout(s)
						} else {
							s.Close(err)
						}
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
		if IsNetTimeoutError(err) {
			err = ErrAsynSendTimeout
		}
		return err
	} else {
		return nil
	}
}

func (s *AsynSocket) sendloop() {
	const maxSendBlockSize int = 65535
	atomic.AddInt32(&s.routineCount, 1)
	go func() {
		var buff []byte = make([]byte, 0, 4096)
		var err error
		defer func() {
			if atomic.AddInt32(&s.routineCount, -1) == 0 {
				s.doCloseCallback()
			}
		}()
		for {
			select {
			case <-s.die:
				for len(s.sendReq) > 0 {
					o := <-s.sendReq
					if buff, err = s.packer.Pack(buff, o); nil != err {
						s.onEncodeError(s, err)
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
				return
			case o := <-s.sendReq:
				if buff, err = s.packer.Pack(buff, o); nil != err {
					s.onEncodeError(s, err)
				} else if len(buff) >= maxSendBlockSize || len(s.sendReq) == 0 {
					if err = s.send(buff); nil != err {
						s.Close(err)
						return
					}
					if len(buff) > maxSendBlockSize*2 {
						buff = make([]byte, 0, 4096)
					} else {
						buff = buff[:0]
					}
				}
			}
		}
	}()
}

func (s *AsynSocket) Send(o interface{}, timeout ...time.Duration) error {
	s.sendOnce.Do(s.sendloop)
	if t := s.getTimeout(timeout); t == 0 {
		//if senReq has no space wait forever
		select {
		case <-s.die:
			return ErrSocketClosed
		case s.sendReq <- o:
			return nil
		}
	} else if t > 0 {
		//if sendReq has no space,wait until deadline
		ticker := time.NewTicker(t)
		defer ticker.Stop()
		select {
		case <-s.die:
			return ErrSocketClosed
		case <-ticker.C:
			return ErrSendTimeout
		case s.sendReq <- o:
			return nil
		}
	} else {
		//if sendReq has no space,return busy
		select {
		case <-s.die:
			return ErrSocketClosed
		case s.sendReq <- o:
			return nil
		default:
			return ErrSendBusy
		}
	}
}
