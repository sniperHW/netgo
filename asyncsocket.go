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
	ErrRecvTimeout    error = errors.New("RecvTimeout")
	ErrSendTimeout    error = errors.New("SendTimeout")
	ErrSocketClosed   error = errors.New("SocketClosed")
	ErrPushTimeout    error = errors.New("PushTimeout")
	ErrAsyncSendBlock error = errors.New("ErrAsyncSendBlock")
	ErrPushBusy       error = errors.New("ErrPushBusy")
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

	Encode([]byte, interface{}) ([]byte, error)
}

type AsynSocketOption struct {
	Decoder            Decoder
	Encoder            EnCoder
	CloseCallBack      func(*AsynSocket, error)
	SendChanSize       int
	HandlePakcet       func(*AsynSocket, interface{}, error)
	AsyncSendBlockTime time.Duration //异步发送调用send时的最大阻塞时间，如果超过这个时间将用ErrAsyncSendBlock错误关闭AsynSocket
}

type defaultEncoder struct {
}

func (de *defaultEncoder) Encode(buff []byte, o interface{}) ([]byte, error) {
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
	socket             Socket
	decoder            Decoder
	encoder            EnCoder
	closeCh            chan struct{}
	recvRequestCh      chan time.Time
	sendRequestCh      chan interface{}
	recvOnce           sync.Once
	sendOnce           sync.Once
	routineCount       int32
	closeOnce          sync.Once
	closeReason        atomic.Value
	doCloseOnce        sync.Once
	closeCallBack      func(*AsynSocket, error)
	handlePakcet       func(*AsynSocket, interface{}, error)
	asyncSendBlockTime time.Duration
	shutdownRead       int32
	sendError          int32
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
		socket:             socket,
		decoder:            option.Decoder,
		encoder:            option.Encoder,
		closeCallBack:      option.CloseCallBack,
		closeCh:            make(chan struct{}),
		recvRequestCh:      make(chan time.Time, 1),
		sendRequestCh:      make(chan interface{}, option.SendChanSize),
		handlePakcet:       option.HandlePakcet,
		asyncSendBlockTime: option.AsyncSendBlockTime,
	}

	return s, nil
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

//发起一个异步读请求

func (s *AsynSocket) Recv(timeout ...time.Duration) {
	var _timeout time.Duration
	if len(timeout) > 0 {
		_timeout = timeout[0]
	}

	if _timeout <= 0 {
		select {
		case <-s.closeCh:
			s.handlePakcet(s, nil, ErrSocketClosed)
		case s.recvRequestCh <- time.Time{}:
			s.recvOnce.Do(s.recvloop)
		}
	} else {
		ticker := time.NewTicker(_timeout)
		defer ticker.Stop()
		select {
		case <-s.closeCh:
			s.handlePakcet(s, nil, ErrSocketClosed)
		case <-ticker.C:
			go s.handlePakcet(s, nil, ErrRecvTimeout)
		case s.recvRequestCh <- time.Now().Add(_timeout):
			s.recvOnce.Do(s.recvloop)
		}
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

		for {
			select {
			case <-s.closeCh:
				return
			case deadline := <-s.recvRequestCh:
				if atomic.LoadInt32(&s.shutdownRead) == 1 {
					return
				} else {
					buff, err := s.socket.Recv(deadline)
					if atomic.LoadInt32(&s.shutdownRead) == 1 {
						return
					} else if nil == err {
						packet, err := s.decoder.Decode(buff)
						s.handlePakcet(s, packet, err)
					} else {
						if IsNetTimeoutError(err) {
							err = ErrRecvTimeout
						}
						s.handlePakcet(s, nil, err)
					}
				}
			}
		}
	}()
}

/*
 *  Send以及AsynSend不支持超时重试的原因：
 *
 *  Send和AsynSend可能同时发生，例如Send超时并发送部分数据，之后AsynSend执行并全部发送完毕，此时如果用户将未发送部分使用Send重发
 *  接收方将接收到乱序的数据。
 */

//同步encode与发送

//返回编码后的buff，如果发送超时将关闭asynsocket

func (s *AsynSocket) Send(o interface{}, timeout ...time.Duration) error {
	var _timeout time.Duration
	if len(timeout) > 0 {
		_timeout = timeout[0]
	}
	select {
	case <-s.closeCh:
		return ErrSocketClosed
	default:
		buff, err := s.encoder.Encode([]byte{}, o)
		if nil != err {
			return err
		} else {
			deadline := time.Time{}
			if _timeout > 0 {
				deadline = time.Now().Add(_timeout)
			}
			_, err := s.socket.Send(buff, deadline)
			if nil != err {
				atomic.StoreInt32(&s.sendError, 1)
				if IsNetTimeoutError(err) {
					err = ErrSendTimeout
				}
				s.Close(err)
			}
			return err
		}
	}
}

func (s *AsynSocket) send(buff []byte) error {
	deadline := time.Time{}
	if s.asyncSendBlockTime > 0 {
		deadline = time.Now().Add(s.asyncSendBlockTime)
	}
	if _, err := s.socket.Send(buff, deadline); nil != err {
		atomic.StoreInt32(&s.sendError, 1)
		if IsNetTimeoutError(err) {
			err = ErrSendTimeout
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
						if buff, err = s.encoder.Encode(buff, o); nil != err {
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
				if buff, err = s.encoder.Encode(buff, o); nil != err {
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

func (s *AsynSocket) Push(o interface{}, timeout ...time.Duration) error {
	var _timeout time.Duration
	if len(timeout) > 0 {
		_timeout = timeout[0]
	}
	if _timeout == 0 {
		//一直等待
		select {
		case <-s.closeCh:
			return ErrSocketClosed
		case s.sendRequestCh <- o:
			s.sendOnce.Do(s.sendloop)
			return nil
		}
	} else if _timeout > 0 {
		//等待到deadline
		ticker := time.NewTicker(_timeout)
		defer ticker.Stop()
		select {
		case <-s.closeCh:
			return ErrSocketClosed
		case <-ticker.C:
			return ErrPushTimeout
		case s.sendRequestCh <- o:
			s.sendOnce.Do(s.sendloop)
			return nil
		}
	} else {
		//如果无法投入队列返回busy
		select {
		case <-s.closeCh:
			return ErrSocketClosed
		case s.sendRequestCh <- o:
			s.sendOnce.Do(s.sendloop)
			return nil
		default:
			return ErrPushBusy
		}
	}
}
