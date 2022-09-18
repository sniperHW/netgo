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
	PackBufferPolicy PackBufferPolicy
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

//pack缓冲策略，为了减少系统调用Send的次数，sender从sendReq获取尽量多的待发送对象
//
//将对象pack到单一的缓冲区中，然后一次性将缓冲区发送出去
type PackBufferPolicy interface {
	//pack用的缓冲区
	GetBuffer() []byte

	//pack完成后,将更新后的缓冲区交给PackBufferPolicy更新内部缓冲区
	//
	//下次再调用GetBuffer将返回更新后的缓冲区
	OnUpdate([]byte)

	//发送成功后调用，传入成功发送的字节数量
	//
	//PackBufferPolicy重置缓冲区
	OnSendOK(int)

	//sender退出时调用，如果内部buff是从pool获取的,OnSenderExit应该将buff归还pool
	OnSenderExit()
}

type movingAverage struct {
	head    int
	window  []int
	total   int
	wc      int
	average int
}

func (ma *movingAverage) add(v int) {
	if ma.wc < len(ma.window) {
		//窗口没有填满
		ma.window[ma.head] = v
		ma.head = (ma.head + 1) % len(ma.window)
		ma.wc++
	} else {
		ma.total -= ma.window[ma.head]
		ma.window[ma.head] = v
		ma.head = (ma.head + 1) % len(ma.window)
	}
	ma.total += v
	ma.average = ma.total / ma.wc
}

//默认PackBufferPolicy
//
//使用预先分配的initBuffSize大小的buff
type defalutPackBufferPolicy struct {
	average      movingAverage
	initBuffSize int
	buff         []byte
}

func (d *defalutPackBufferPolicy) OnUpdate(buff []byte) {
	d.buff = buff
}

func (d *defalutPackBufferPolicy) OnSendOK(n int) {
	d.average.add(n)
	if cap(d.buff) > d.initBuffSize && d.average.average < d.initBuffSize {
		//如果buff的容量超过了initBuffSize，且最近buff用量的移动平均数小于initBuffSize
		//用initBuffSize收缩buff的大小,避免占用大量未被使用的内存空间
		d.buff = make([]byte, 0, d.initBuffSize)
	} else {
		d.buff = d.buff[:0]
	}
}

func (d *defalutPackBufferPolicy) GetBuffer() []byte {
	return d.buff
}

func (d *defalutPackBufferPolicy) OnSenderExit() {

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
	packBufferPolicy PackBufferPolicy
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
		handlePakcet:     option.HandlePakcet,
		asyncSendTimeout: option.AsyncSendTimeout,
		onEncodeError:    option.OnEncodeError,
		onRecvTimeout:    option.OnRecvTimeout,
		packBufferPolicy: option.PackBufferPolicy,
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
	if nil == s.packBufferPolicy {
		s.packBufferPolicy = &defalutPackBufferPolicy{
			initBuffSize: 1024,
			buff:         make([]byte, 0, 1024),
			average: movingAverage{
				window: make([]int, 10),
			},
		}
	}

	if option.SendChanSize > 0 {
		s.sendReq = make(chan interface{}, option.SendChanSize)
	} else {
		s.sendReq = make(chan interface{})
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
		var err error
		defer func() {
			if atomic.AddInt32(&s.routineCount, -1) == 0 {
				s.doClose()
			}
			s.packBufferPolicy.OnSenderExit()
		}()
		for {
			select {
			case <-s.die:
				var buff []byte
				for len(s.sendReq) > 0 {
					o := <-s.sendReq
					buff = s.packBufferPolicy.GetBuffer()
					buff, _ = s.packer.Pack(buff, o)
					s.packBufferPolicy.OnUpdate(buff)
					if len(buff) >= maxSendBlockSize {
						if s.send(buff) != nil {
							return
						} else {
							s.packBufferPolicy.OnSendOK(len(buff))
						}
					}
				}

				if len(buff) > 0 {
					if s.send(buff) != nil {
						s.packBufferPolicy.OnSendOK(len(buff))
					}
				}
				return
			case o := <-s.sendReq:
				buff := s.packBufferPolicy.GetBuffer()
				if buff, err = s.packer.Pack(buff, o); err != nil {
					s.onEncodeError(s, err)
				} else {
					s.packBufferPolicy.OnUpdate((buff))
				}
				l := len(buff)
				if l >= maxSendBlockSize || (l > 0 && len(s.sendReq) == 0) {
					if err = s.send(buff); nil != err {
						s.Close(err)
						return
					}
					s.packBufferPolicy.OnSendOK(l)
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
