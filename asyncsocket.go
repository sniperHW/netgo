package network

import (
	"errors"
	//"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type OutputBufLimit struct {
	OutPutLimitSoft        int
	OutPutLimitSoftSeconds int
	OutPutLimitHard        int
}

var (
	ErrRecvTimeout  error = errors.New("RecvTimeout")
	ErrSendTimeout  error = errors.New("SendTimeout")
	ErrSocketClosed error = errors.New("SocketClosed")
	ErrPushTimeout  error = errors.New("PushTimeout")
)

var (
	DefaultOutPutLimitSoft        int = 128 * 1024      //128k
	DefaultOutPutLimitSoftSeconds int = 10              //10s
	DefaultOutPutLimitHard        int = 4 * 1024 * 1024 //4M
)

/*
 * 对Socket interface的异步封装
 * 提供异步recv和send处理
 */

type Decoder interface {
	//将[]byte解码成对象

	//如果成功返回对象,否则返回错误

	Decode([]byte) (interface{}, error)
}

type EnCoder interface {

	//将对象编码到[]byte中

	//如果成功返回添了对象编码的[]byte,否则返回错误

	//如果最后一个参数为true,表示当前Encode对象为本批次的最后一个

	//例如如果为true,encoder可以考虑将o添加进[]byte后，将整个[]byte做压缩或加密，产生一个合并后的大包

	Encode([]byte, interface{}, bool) ([]byte, error)
}

type AsynSocketOption struct {
	Decoder       Decoder
	Encoder       EnCoder
	CloseCallBack func(*AsynSocket, error)
	SendChanSize  int
	HandlePakcet  func(*AsynSocket, interface{}, error)
}

type defaultEncoder struct {
}

func (de *defaultEncoder) Encode(buff []byte, o interface{}, last bool) ([]byte, error) {
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
	socket                   Socket
	decoder                  Decoder
	encoder                  EnCoder
	recvCloseCh              chan struct{}
	sendCloseCh              chan struct{}
	recvRequestCh            chan time.Time
	sendRequestCh            chan interface{}
	outputLimit              OutputBufLimit
	obufSoftLimitReachedTime int64
	recvOnce                 sync.Once
	sendOnce                 sync.Once
	routineCount             int32
	closeOnce                sync.Once
	closeReason              atomic.Value
	closeCallBack            func(*AsynSocket, error)
	handlePakcet             func(*AsynSocket, interface{}, error)
}

func NewAsynSocket(socket Socket, option AsynSocketOption) (*AsynSocket, error) {
	if nil == socket {
		return nil, errors.New("socket is nil")
	}
	if nil == option.HandlePakcet {
		return nil, errors.New("HandlePakcet is nil")
	}

	if nil == option.Decoder {
		option.Decoder = &defalutDecoder{}
	}
	if nil == option.Encoder {
		option.Encoder = &defaultEncoder{}
	}
	if nil == option.CloseCallBack {
		option.CloseCallBack = func(*AsynSocket, error) {
		}
	}

	if option.SendChanSize <= 0 {
		option.SendChanSize = 1
	}

	s := &AsynSocket{
		socket:        socket,
		decoder:       option.Decoder,
		encoder:       option.Encoder,
		closeCallBack: option.CloseCallBack,
		recvCloseCh:   make(chan struct{}),
		sendCloseCh:   make(chan struct{}),
		recvRequestCh: make(chan time.Time, 1),
		sendRequestCh: make(chan interface{}, option.SendChanSize),
		handlePakcet:  option.HandlePakcet,
	}

	return s, nil
}

func (s *AsynSocket) doCloseCallback() {
	reason := s.closeReason.Load()
	if nil != reason {
		s.closeCallBack(s, reason.(error))
	} else {
		s.closeCallBack(s, nil)
	}
}

func (s *AsynSocket) Close(err error) {
	s.closeOnce.Do(func() {
		atomic.AddInt32(&s.routineCount, 1)
		if nil != err {
			s.closeReason.Store(err)
		}
		close(s.recvCloseCh)
		close(s.sendCloseCh)
		s.socket.Close()
		if 0 == atomic.AddInt32(&s.routineCount, -1) {
			s.doCloseCallback()
		}
	})
}

//发起一个异步读请求

func (s *AsynSocket) Recv(deadline time.Time) {
	if deadline.IsZero() {
		select {
		case <-s.recvCloseCh:
			s.handlePakcet(s, nil, ErrSocketClosed)
		case s.recvRequestCh <- deadline:
			s.recvOnce.Do(s.recvloop)
		}
	} else {
		ticker := time.NewTicker(deadline.Sub(time.Now()))
		defer ticker.Stop()
		select {
		case <-s.recvCloseCh:
			s.handlePakcet(s, nil, ErrSocketClosed)
		case <-ticker.C:
			go s.handlePakcet(s, nil, ErrRecvTimeout)
		case s.recvRequestCh <- deadline:
			s.recvOnce.Do(s.recvloop)
		}
	}
}

func (s *AsynSocket) recvloop() {
	atomic.AddInt32(&s.routineCount, 1)
	go func() {
		for {
			select {
			case <-s.recvCloseCh:
				if atomic.AddInt32(&s.routineCount, -1) == 0 {
					s.doCloseCallback()
				}
				return
			case deadline := <-s.recvRequestCh:
				if buff, err := s.socket.Recv(deadline); nil == err {
					packet, err := s.decoder.Decode(buff)
					s.handlePakcet(s, packet, err)
				} else {
					s.handlePakcet(s, nil, err)
				}
			}
		}
	}()
}

//同步encode与发送

//返回编码后的buff，以及成功发送的字节数

func (s *AsynSocket) Send(o interface{}, deadline time.Time) ([]byte, int, error) {
	select {
	case <-s.sendCloseCh:
		return nil, 0, ErrSocketClosed
	default:
		buff, err := s.encoder.Encode([]byte{}, o, true)
		if nil != err {
			return nil, 0, err
		} else {
			n, err := s.socket.Send(buff, deadline)
			return buff, n, err
		}
	}
}

//异步投递

//在单独的goroutine encode与发送

//异步队列满阻塞Push直到deadline

func (s *AsynSocket) Push(o interface{}, deadline time.Time) error {
	return nil
}
