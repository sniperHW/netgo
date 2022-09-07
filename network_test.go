package network

//go test -race -covermode=atomic -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	"errors"
	"fmt"
	//"github.com/stretchr/testify/assert"
	//"io"
	"net"
	//"runtime"
	//"strings"
	//"sync"
	gorilla "github.com/gorilla/websocket"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestWebSocket(t *testing.T) {
	{

		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		upgrader := &gorilla.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		}

		http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
			conn, _ := upgrader.Upgrade(w, r, nil)

			conn.SetPingHandler(func(appData string) error {
				fmt.Println("on ping")
				conn.WriteMessage(gorilla.PongMessage, []byte(appData))
				return nil
			})

			fmt.Println("on client")
			s, _ := NewWebSocket(conn, SocketOption{
				CloseCallBack: func(e error) {
					fmt.Println("server Socket close", e)
				},
			})

			s.(*WebSocket).StartRecv(WsRecvOption{
				RecvTimeout: time.Second,
				OnRecvTimeout: func() {
					fmt.Println("server Socket recv timeout")
					s.Close(errors.New("recv timeout"))
				},
				OnPacket: func(packet []byte) {
					s.Send(packet)
				},
				PingHandler: func(appData string) error {
					fmt.Println("on ping")
					conn.WriteMessage(gorilla.PongMessage, []byte(appData))
					return nil
				},
			})
		})

		go func() {
			http.Serve(listener, nil)
		}()

		{

			u := url.URL{Scheme: "ws", Host: "localhost:8110", Path: "/echo"}
			dialer := gorilla.DefaultDialer

			conn, _, _ := dialer.Dial(u.String(), nil)

			respChan := make(chan struct{})
			s, _ := NewWebSocket(conn, SocketOption{})

			conn.WriteMessage(gorilla.PingMessage, []byte("hello"))

			conn.SetPongHandler(func(appData string) error {
				fmt.Println("on pong")
				close(respChan)
				return nil
			})

			s.(*WebSocket).StartRecv(WsRecvOption{
				OnPacket: func(packet []byte) {
					fmt.Println(string(packet))
				},
				PongHandler: func(appData string) error {
					fmt.Println("on pong")
					close(respChan)
					return nil
				},
			})

			<-respChan

			s.Close(nil)
		}

		{
			u := url.URL{Scheme: "ws", Host: "localhost:8110", Path: "/echo"}
			dialer := gorilla.DefaultDialer

			conn, _, _ := dialer.Dial(u.String(), nil)

			respChan := make(chan struct{})
			s, _ := NewWebSocket(conn, SocketOption{})

			s.(*WebSocket).StartRecv(WsRecvOption{
				OnPacket: func(packet []byte) {
					fmt.Println(string(packet))
					close(respChan)
				},
			})

			s.Send([]byte("hello"))

			<-respChan

			s.Close(nil)
		}

		listener.Close()

	}
}

func TestTCPSocket(t *testing.T) {

	{
		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				} else {
					fmt.Println("on client")
					s, _ := NewTcpSocket(conn, SocketOption{
						CloseCallBack: func(e error) {
							fmt.Println("server Socket close", e)
						},
					})

					s.(*TcpSocket).StartRecv(TcpRecvOption{
						RecvTimeout: time.Second,
						OnRecvTimeout: func() {
							fmt.Println("server Socket recv timeout")
							s.Close(errors.New("recv timeout"))
						},
						OnPacket: func(packet []byte) {
							s.Send(packet)
						},
					})
				}
			}
		}()

		dialer := &net.Dialer{}

		{
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			respChan := make(chan struct{})
			s, _ := NewTcpSocket(conn, SocketOption{})

			s.(*TcpSocket).StartRecv(TcpRecvOption{
				OnPacket: func(packet []byte) {
					fmt.Println(string(packet))
					close(respChan)
				},
			})

			s.Send([]byte("hello"))

			<-respChan

			s.Close(nil)
		}

		{
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			respChan := make(chan struct{})
			s, _ := NewTcpSocket(conn, SocketOption{
				CloseCallBack: func(e error) {
					fmt.Println("client Socket close", e)
					close(respChan)
				}})

			s.(*TcpSocket).StartRecv(TcpRecvOption{
				OnPacket: func(packet []byte) {
					fmt.Println(string(packet))
				},
			})

			<-respChan
		}

		listener.Close()
	}
}

/*
func TestSendTimeout(t *testing.T) {

	{

		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		var mu sync.Mutex

		var holdSession *Socket

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				} else {
					conn.(*net.TCPConn).SetReadBuffer(0)
					mu.Lock()
					holdSession = NewSocket(conn, OutputBufLimit{})
					mu.Unlock()
					//不启动接收
				}
			}
		}()

		dialer := &net.Dialer{}
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		conn.(*net.TCPConn).SetWriteBuffer(0)
		session := NewSocket(conn, OutputBufLimit{})

		die := make(chan struct{})

		session.SetCloseCallBack(func(sess *Socket, reason error) {
			close(die)
		})

		session.SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})

		session.SetEncoder(&encoder{})

		session.SetSendTimeout(time.Second)

		triger := false

		session.SetErrorCallBack(func(sess *Socket, err error) {
			assert.Equal(t, ErrSendTimeout, err)
			if triger { //第二次触发再close
				fmt.Println("here")
				sess.Close(err, 0)
			} else {
				triger = true
			}
		})

		session.BeginRecv(func(s *Socket, msg interface{}) {
		})

		go func() {
			for {
				err := session.Send(strings.Repeat("a", 65535))
				if nil != err {
					fmt.Println("break here", err)
					break
				}
			}
		}()
		<-die
		mu.Lock()
		holdSession.Close(nil, 0)
		mu.Unlock()

		listener.Close()
	}
}

func TestSocket(t *testing.T) {

	{

		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		die := make(chan struct{})

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				} else {
					session := NewSocket(conn, OutputBufLimit{})
					session.GetNetConn()
					session.SetEncoder(&encoder{})
					session.SetRecvTimeout(time.Second * 1).SetSendTimeout(time.Second * 1)
					session.SetCloseCallBack(func(s *Socket, reason error) {
						fmt.Println("server close")
						close(die)
					}).SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})

					session.SetErrorCallBack(func(s *Socket, err error) {
						fmt.Println("err", err)
						s.Close(err, 0)
						assert.Equal(t, s.Send("hello"), ErrSocketClose)
					}).BeginRecv(func(s *Socket, msg interface{}) {
						fmt.Println("recv", string(msg.([]byte)))
						s.Send(msg)
					})
				}
			}
		}()

		fmt.Println("00")
		dialer := &net.Dialer{}
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		session := NewSocket(conn, OutputBufLimit{})

		respChan := make(chan interface{})

		session.SetUserData(1)
		assert.Equal(t, 1, session.GetUserData().(int))

		session.SetCloseCallBack(func(sess *Socket, reason error) {
			fmt.Println("client close")
		}).SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)}).SetEncoder(&encoder{})

		session.SetErrorCallBack(func(s *Socket, err error) {
			s.Close(err, 0)
			assert.Equal(t, true, s.IsClosed())
		}).BeginRecv(func(s *Socket, msg interface{}) {
			respChan <- msg
		})

		fmt.Println("0011")

		assert.Equal(t, nil, session.Send("hello"))

		resp := <-respChan

		fmt.Println("0022")

		assert.Equal(t, resp.([]byte), []byte("hello"))

		<-die

		listener.Close()
	}
	fmt.Println("11")
	{

		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				} else {
					NewSocket(conn, OutputBufLimit{}).SetEncoder(&encoder{}).
						SetRecvTimeout(time.Second * 1).
						SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)}).
						BeginRecv(func(s *Socket, msg interface{}) {
							s.Send(msg)
							s.Close(nil, time.Second)
						})
				}
			}
		}()
		fmt.Println("22")
		{
			dialer := &net.Dialer{}
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			session := NewSocket(conn, OutputBufLimit{})

			respChan := make(chan interface{})

			session.SetEncoder(&encoder{})

			session.SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})

			session.BeginRecv(func(s *Socket, msg interface{}) {
				respChan <- msg
			})

			session.Send("hello")

			resp := <-respChan

			assert.Equal(t, resp.([]byte), []byte("hello"))
		}
		fmt.Println("33")
		{
			dialer := &net.Dialer{}
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			session := NewSocket(conn, OutputBufLimit{})

			session.SetEncoder(&encoder{})

			session.SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})

			session.SetCloseCallBack(func(sess *Socket, reason error) {

			})

			session.Close(nil, 0)

			err := session.BeginRecv(func(s *Socket, msg interface{}) {

			})

			assert.Equal(t, ErrSocketClose, err)
		}

		{
			dialer := &net.Dialer{}
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			session := NewSocket(conn, OutputBufLimit{})
			session.SetCloseCallBack(func(sess *Socket, reason error) {
				fmt.Println("reason", reason)
			})
			_ = session.LocalAddr()
			_ = session.RemoteAddr()
			session = nil
			for i := 0; i < 2; i++ {
				time.Sleep(time.Second)
				runtime.GC()
			}
		}

		fmt.Println("here----------")

		{
			dialer := &net.Dialer{}
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			session := NewSocket(conn, OutputBufLimit{})

			die := make(chan struct{})

			session.SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})

			session.SetEncoder(&errencoder{}).SetRecvTimeout(time.Second).BeginRecv(func(s *Socket, msg interface{}) {

			})

			session.SetCloseCallBack(func(sess *Socket, reason error) {
				fmt.Println("close", reason)
				close(die)
			})

			session.Send("hello")

			<-die
		}

		listener.Close()
	}
	fmt.Println("here----------2")
	{

		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		die := make(chan struct{})

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				} else {
					NewSocket(conn, OutputBufLimit{}).SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)}).BeginRecv(func(s *Socket, msg interface{}) {
						close(die)
					})
				}
			}
		}()

		dialer := &net.Dialer{}
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		session := NewSocket(conn, OutputBufLimit{})

		session.SetEncoder(&encoder{})

		session.Send("hello")

		session.Close(nil, time.Second)

		_ = <-die

		listener.Close()
	}
	fmt.Println("here----------3")
	{

		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		serverdie := make(chan struct{})
		clientdie := make(chan struct{})

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				} else {
					session := NewSocket(conn, OutputBufLimit{})
					session.SetRecvTimeout(time.Second * 1)
					session.SetSendTimeout(time.Second * 1)
					session.SetCloseCallBack(func(sess *Socket, reason error) {
						fmt.Println("server die")
						close(serverdie)
					})
					session.SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})
					session.BeginRecv(func(s *Socket, msg interface{}) {
					})

				}
			}
		}()

		dialer := &net.Dialer{}
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		session := NewSocket(conn, OutputBufLimit{})
		session.SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})
		session.SetEncoder(&encoder{}).SetCloseCallBack(func(sess *Socket, reason error) {
			fmt.Println("client die")
			close(clientdie)
		}).BeginRecv(func(s *Socket, msg interface{}) {
		})

		go func() {
			for {
				if err := session.Send("hello"); nil != err {
					if err == ErrSocketClose {
						break
					}
				}
			}
		}()

		go func() {
			time.Sleep(time.Second * 2)
			session.Close(nil, time.Second)
		}()

		<-clientdie
		<-serverdie

		listener.Close()
	}

}

func TestShutDownRead(t *testing.T) {
	tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

	listener, _ := net.ListenTCP("tcp", tcpAddr)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			} else {
				NewSocket(conn, OutputBufLimit{}).SetCloseCallBack(func(sess *Socket, reason error) {
					fmt.Println("server close")
				}).SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)}).BeginRecv(func(s *Socket, msg interface{}) {
				})
			}
		}
	}()

	dialer := &net.Dialer{}
	conn, _ := dialer.Dial("tcp", "localhost:8110")
	session := NewSocket(conn, OutputBufLimit{})

	die := make(chan struct{})

	session.SetCloseCallBack(func(sess *Socket, reason error) {
		fmt.Println("client close", reason)
		close(die)
	}).SetEncoder(&encoder{}).SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)}).SetRecvTimeout(time.Second * 1)

	session.BeginRecv(func(s *Socket, msg interface{}) {
	})

	fmt.Println("ShutdownRead1")

	session.ShutdownRead()

	fmt.Println("ShutdownRead2")

	time.Sleep(time.Second * 2)

	session.Close(nil, 0)

	<-die

	listener.Close()

}

func TestShutDownWrite(t *testing.T) {
	tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

	listener, _ := net.ListenTCP("tcp", tcpAddr)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			} else {
				s := NewSocket(conn, OutputBufLimit{}).SetCloseCallBack(func(sess *Socket, reason error) {
					fmt.Printf("server close %p\n", sess)
				}).SetErrorCallBack(func(sess *Socket, reason error) {
					if reason == io.EOF {
						fmt.Println("send ")
						fmt.Println(sess.Send("hello"))
					}
					sess.Close(nil, time.Second)
				})

				s.SetEncoder(&encoder{})
				s.SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})
				s.BeginRecv(func(s *Socket, msg interface{}) {
					fmt.Println("ondata", string(msg.([]byte)))
				})
			}
		}
	}()

	fmt.Println("11111111111")

	{
		dialer := &net.Dialer{}
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		session := NewSocket(conn, OutputBufLimit{}).SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})

		die := make(chan struct{})

		session.SetCloseCallBack(func(sess *Socket, reason error) {
			fmt.Println("client close", reason)
		}).SetEncoder(&encoder{}).SetRecvTimeout(time.Second * 1)

		session.BeginRecv(func(s *Socket, msg interface{}) {
			assert.Equal(t, "hello", string(msg.([]byte)))
			close(die)
		})

		session.ShutdownWrite()

		<-die
	}
	fmt.Println("22222222222222")
	{
		dialer := &net.Dialer{}
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		session := NewSocket(conn, OutputBufLimit{}).SetInBoundProcessor(&TestInboundProcessor{buffer: make([]byte, 1024)})

		die := make(chan struct{})

		session.SetCloseCallBack(func(sess *Socket, reason error) {
			fmt.Println("client close", reason)
		}).SetEncoder(&encoder{}).SetRecvTimeout(time.Second * 1)

		session.BeginRecv(func(s *Socket, msg interface{}) {
			assert.Equal(t, "hello", string(msg.([]byte)))
			close(die)
		})

		session.Send("hello")

		session.ShutdownWrite()

		<-die
	}

	listener.Close()
}
*/
