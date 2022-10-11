package netgo

import (
	"context"
	"errors"
	"github.com/sniperHW/netgo/poolbuff"
	"net"
	"sync"
	"sync/atomic"
	"time"
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
	Encode([]byte, interface{}) []byte
}

type AsynSocketOption struct {
	Codec            ObjCodec
	SendChanSize     int
	AsyncSendTimeout time.Duration
	OutputBuffer     OutputBuffer
	AutoRecv         bool          //处理完packet后自动调用Recv
	AutoRecvTimeout  time.Duration //自动调用Recv时的超时时间
}

type defaultCodec struct {
}

func (codec *defaultCodec) Encode(buff []byte, o interface{}) []byte {
	if b, ok := o.([]byte); ok {
		return append(buff, b...)
	} else {
		return buff
	}
}

func (codec *defaultCodec) Decode(buff []byte) (interface{}, error) {
	packet := make([]byte, len(buff))
	copy(packet, buff)
	return packet, nil
}

//每发送一个对象，都将产生一次Send系统调用，对于大量小对象的情况将严重影响效率
//
//更合理做法是提供一个缓冲区，将待发送对象pack到缓冲区中，当缓冲区累积到一定数量
//
//或后面没有待发送对象，再一次情况将整个缓冲区交给Send
//
//OutputBuffer是这种缓冲区的一个接口抽象，可根据具体需要实现OutputBuffer
type OutputBuffer interface {
	//pack用的缓冲区
	GetBuffer() []byte

	//pack完成后,将更新后的缓冲区交给PackBuffer更新内部缓冲区
	//
	//下次再调用GetBuffer将返回更新后的缓冲区
	OnUpdate([]byte) []byte

	//buff使用完毕后释放
	ReleaseBuffer()

	//sender退出时调用，如果内部buff是从pool获取的,Clear应该将buff归还pool
	Clear()

	ResetBuffer()
}

//asynchronize encapsulation for Socket
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
	closeCallBack    func(*AsynSocket, error) //call when routineCount trun to zero
	handlePakcet     func(*AsynSocket, interface{}) error
	onRecvTimeout    func(*AsynSocket)
	asyncSendTimeout time.Duration
	outputBuffer     OutputBuffer
	autoRecv         bool
	autoRecvTimeout  time.Duration
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
		outputBuffer:     option.OutputBuffer,
		autoRecv:         option.AutoRecv,
		autoRecvTimeout:  option.AutoRecvTimeout,
		codec:            option.Codec,
	}

	if s.codec == nil {
		s.codec = &defaultCodec{}
	}

	s.closeCallBack = func(*AsynSocket, error) {

	}

	s.onRecvTimeout = func(*AsynSocket) {
		s.Close(ErrRecvTimeout)
	}

	s.handlePakcet = func(*AsynSocket, interface{}) error {
		s.Recv()
		return nil
	}

	if s.outputBuffer == nil {
		s.outputBuffer = poolbuff.New()
	}

	return s
}

func (s *AsynSocket) SetCloseCallback(closeCallBack func(*AsynSocket, error)) *AsynSocket {
	if closeCallBack != nil {
		s.closeCallBack = closeCallBack
	}
	return s
}

func (s *AsynSocket) SetRecvTimeoutCallback(onRecvTimeout func(*AsynSocket)) *AsynSocket {
	if onRecvTimeout != nil {
		s.onRecvTimeout = onRecvTimeout
	}
	return s
}

func (s *AsynSocket) SetPacketHandler(handlePakcet func(*AsynSocket, interface{}) error) *AsynSocket {
	if handlePakcet != nil {
		s.handlePakcet = handlePakcet
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
		s.closeCallBack(s, reason)
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
						if err = s.handlePakcet(s, packet); err != nil {
							s.Close(err)
							return
						}
					} else {
						if IsNetTimeoutError(err) {
							s.onRecvTimeout(s)
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
	atomic.AddInt32(&s.routineCount, 1)
	go func() {
		var err error
		defer func() {
			if atomic.AddInt32(&s.routineCount, -1) == 0 {
				s.doClose()
			}
			s.outputBuffer.Clear()
		}()
		for {
			select {
			case <-s.die:
				for len(s.sendReq) > 0 {
					o := <-s.sendReq
					buff := s.outputBuffer.OnUpdate(s.codec.Encode(s.outputBuffer.GetBuffer(), o))
					if len(buff) >= MaxSendBlockSize {
						if s.send(buff) != nil {
							return
						} else {
							s.outputBuffer.ResetBuffer()
						}
					}
				}

				if buff := s.outputBuffer.GetBuffer(); len(buff) > 0 {
					s.send(buff)
				}
				return
			case o := <-s.sendReq:
				buff := s.outputBuffer.OnUpdate(s.codec.Encode(s.outputBuffer.GetBuffer(), o))
				l := len(buff)
				if l >= MaxSendBlockSize || (l > 0 && len(s.sendReq) == 0) {
					if err = s.send(buff); nil != err {
						s.Close(err)
						return
					}
					s.outputBuffer.ReleaseBuffer()
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

//deadline: 如果不传递，当发送chan满一直等待
//deadline.IsZero() || deadline.Before(time.Now):当chan满立即返回ErrSendBusy
//否则当发送chan满等待到deadline,返回ErrSendTimeout
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
