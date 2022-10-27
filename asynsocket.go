package netgo

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sniperHW/netgo/poolbuff"
)

type errTimeout struct {
	error
}

func (e *errTimeout) Timeout() bool {
	return true
}

func (e *errTimeout) Temporary() bool {
	return false
}

var (
	ErrRecvTimeout     error = &errTimeout{error: errors.New("RecvTimeout")}
	ErrSendTimeout     error = &errTimeout{error: errors.New("SendTimeout")}
	ErrAsynSendTimeout error = &errTimeout{error: errors.New("ErrAsynSendTimeout")}
	ErrSocketClosed    error = errors.New("SocketClosed")
)

var MaxSendBlockSize int = 65535

type ObjCodec interface {
	Decode([]byte) (interface{}, error)
	Encode(net.Buffers, interface{}) (net.Buffers, int)
}

type AsynSocketOption struct {
	Codec            ObjCodec
	SendChanSize     int
	AsyncSendTimeout time.Duration
	AutoRecv         bool          //处理完packet后自动调用Recv
	AutoRecvTimeout  time.Duration //自动调用Recv时的超时时间
	Context          context.Context
}

type defaultCodec struct {
}

func (codec *defaultCodec) Encode(buffs net.Buffers, o interface{}) (net.Buffers, int) {
	if b, ok := o.([]byte); ok {
		return append(buffs, b), len(b)
	} else {
		return buffs, 0
	}
}

func (codec *defaultCodec) Decode(buff []byte) (interface{}, error) {
	packet := make([]byte, len(buff))
	copy(packet, buff)
	return packet, nil
}

// asynchronize encapsulation for Socket
type AsynSocket struct {
	socket           Socket
	codec            ObjCodec
	die              chan struct{}
	recvReq          chan time.Time
	sendReq          chan interface{}
	recvOnce         sync.Once
	sendOnce         sync.Once
	routineCount     int32
	closeOnce        sync.Once
	closeReason      atomic.Value
	doCloseOnce      sync.Once
	closeCallBack    atomic.Value //func(*AsynSocket, error) //call when routineCount trun to zero
	handlePakcet     atomic.Value //func(context.Context, *AsynSocket, interface{}) error
	onRecvTimeout    atomic.Value //func(*AsynSocket)
	asyncSendTimeout time.Duration
	autoRecv         bool
	autoRecvTimeout  time.Duration
	context          context.Context
}

func NewAsynSocket(socket Socket, option AsynSocketOption) *AsynSocket {

	if option.SendChanSize <= 0 {
		option.SendChanSize = 1
	}

	s := &AsynSocket{
		socket:           socket,
		die:              make(chan struct{}),
		recvReq:          make(chan time.Time, 1),
		sendReq:          make(chan interface{}, option.SendChanSize),
		asyncSendTimeout: option.AsyncSendTimeout,
		autoRecv:         option.AutoRecv,
		autoRecvTimeout:  option.AutoRecvTimeout,
		codec:            option.Codec,
		context:          option.Context,
	}

	if s.codec == nil {
		s.codec = &defaultCodec{}
	}

	if s.context == nil {
		s.context = context.Background()
	}

	s.closeCallBack.Store(func(*AsynSocket, error) {

	})

	s.onRecvTimeout.Store(func(*AsynSocket) {
		s.Close(ErrRecvTimeout)
	})

	s.handlePakcet.Store(func(context.Context, *AsynSocket, interface{}) error {
		s.Recv()
		return nil
	})

	return s
}

func (s *AsynSocket) SetCloseCallback(closeCallBack func(*AsynSocket, error)) *AsynSocket {
	if closeCallBack != nil {
		s.closeCallBack.Store(closeCallBack)
	}
	return s
}

func (s *AsynSocket) SetRecvTimeoutCallback(onRecvTimeout func(*AsynSocket)) *AsynSocket {
	if onRecvTimeout != nil {
		s.onRecvTimeout.Store(onRecvTimeout)
	}
	return s
}

// make sure to SetPacketHandler before the first Recv
//
// after Recv start recvloop, packethandler can't be change anymore
func (s *AsynSocket) SetPacketHandler(handlePakcet func(context.Context, *AsynSocket, interface{}) error) *AsynSocket {
	if handlePakcet != nil {
		s.handlePakcet.Store(handlePakcet)
	}
	return s
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

func (s *AsynSocket) doClose() {
	s.doCloseOnce.Do(func() {
		s.socket.Close()
		reason, _ := s.closeReason.Load().(error)
		s.closeCallBack.Load().(func(*AsynSocket, error))(s, reason)
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
			go s.doClose()
		}
	})
}

// send a asynchronize recv request
//
// if there is a packet received before timeout,handlePakcet would be call with packet as a parameter
//
// if recevie timeout,on onReceTimeout would be call
//
// if recvReq is full,drop the request
func (s *AsynSocket) Recv(deadline ...time.Time) *AsynSocket {
	s.recvOnce.Do(s.recvloop)
	d := time.Time{}
	if len(deadline) > 0 {
		d = deadline[0]
	}
	select {
	case <-s.die:
	case s.recvReq <- d:
	default:
	}
	return s
}

func (s *AsynSocket) recvloop() {
	atomic.AddInt32(&s.routineCount, 1)
	packetHandler := s.handlePakcet.Load().(func(context.Context, *AsynSocket, interface{}) error)
	go func() {
		defer func() {
			if atomic.AddInt32(&s.routineCount, -1) == 0 {
				s.doClose()
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
						packet, err = s.codec.Decode(buff)
					}
					if nil == err {
						if err = packetHandler(s.context, s, packet); err != nil {
							s.Close(err)
							return
						}
					} else {
						if IsNetTimeoutError(err) {
							s.onRecvTimeout.Load().(func(*AsynSocket))(s)
						} else {
							s.Close(err)
							return
						}
					}
					if s.autoRecv {
						if s.autoRecvTimeout > 0 {
							s.Recv(time.Now().Add(s.autoRecvTimeout))
						} else {
							s.Recv()
						}
					}
				}
			}
		}
	}()
}

func (s *AsynSocket) sendBuffs(buffs net.Buffers) (err error) {
	deadline := time.Time{}
	if s.asyncSendTimeout > 0 {
		deadline = time.Now().Add(s.asyncSendTimeout)
	}

	if buffersSender, ok := s.socket.(BuffersSender); ok {
		_, err = buffersSender.SendBuffers(buffs, deadline)
	} else {
		outputBuffer := poolbuff.Get()
		defer poolbuff.Put(outputBuffer)
		for _, v := range buffs {
			outputBuffer = append(outputBuffer, v...)
		}
		_, err = s.socket.Send(outputBuffer, deadline)
	}

	if nil != err {
		if IsNetTimeoutError(err) {
			err = ErrAsynSendTimeout
		}
	}
	return err
}

func (s *AsynSocket) sendloop() {
	atomic.AddInt32(&s.routineCount, 1)
	go func() {
		var (
			err error
		)
		defer func() {
			if atomic.AddInt32(&s.routineCount, -1) == 0 {
				s.doClose()
			}
		}()

		maxBuffSize := 1024
		total := 0
		n := 0
		buffs := net.Buffers{}
		for {
			select {
			case <-s.die:
				for len(s.sendReq) > 0 {
					o := <-s.sendReq
					buffs, n = s.codec.Encode(buffs, o)
					total += n
					if total >= MaxSendBlockSize || len(buffs) >= maxBuffSize {
						if s.sendBuffs(buffs) != nil {
							return
						} else {
							buffs = buffs[:0]
							total = 0
						}
					}
				}

				if total > 0 {
					s.sendBuffs(buffs)
				}
				return
			case o := <-s.sendReq:
				buffs, n = s.codec.Encode(buffs, o)
				total += n
				if (total >= MaxSendBlockSize || len(buffs) >= maxBuffSize) || (total > 0 && len(s.sendReq) == 0) {
					if err = s.sendBuffs(buffs); nil != err {
						s.Close(err)
						return
					}
					buffs = net.Buffers{}
					total = 0
				}
			}
		}
	}()
}

func (s *AsynSocket) getTimeout(deadline []time.Time) time.Duration {
	if len(deadline) > 0 {
		if deadline[0].IsZero() {
			return -1
		} else {
			return deadline[0].Sub(time.Now())
		}
	} else {
		return 0
	}
}

// deadline: 如果不传递，当发送chan满一直等待
// deadline.IsZero() || deadline.Before(time.Now):当chan满立即返回ErrSendBusy
// 否则当发送chan满等待到deadline,返回ErrSendTimeout
func (s *AsynSocket) Send(o interface{}, deadline ...time.Time) error {
	s.sendOnce.Do(s.sendloop)
	if timeout := s.getTimeout(deadline); timeout == 0 {
		//if senReq has no space wait forever
		select {
		case <-s.die:
			return ErrSocketClosed
		case s.sendReq <- o:
			return nil
		}
	} else if timeout > 0 {
		//if sendReq has no space,wait until deadline
		ticker := time.NewTicker(timeout)
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
			return ErrSendTimeout
		}
	}
}

func (s *AsynSocket) SendWithContext(ctx context.Context, o interface{}) error {
	s.sendOnce.Do(s.sendloop)
	select {
	case <-s.die:
		return ErrSocketClosed
	case s.sendReq <- o:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
