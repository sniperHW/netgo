package network

//go test -race -covermode=atomic -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	"crypto/sha1"
	"fmt"
	gorilla "github.com/gorilla/websocket"
	"github.com/xtaci/kcp-go/v5"
	"golang.org/x/crypto/pbkdf2"
	"log"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestKcpSocket(t *testing.T) {
	key := pbkdf2.Key([]byte("demo pass"), []byte("demo salt"), 1024, 32, sha1.New)
	block, _ := kcp.NewAESBlockCrypt(key)

	var (
		listener *kcp.Listener
		err      error
	)

	if listener, err = kcp.ListenWithOptions("127.0.0.1:12345", block, 10, 3); err == nil {
		go func() {
			for {
				conn, err := listener.AcceptKCP()
				if err != nil {
					return
				}

				fmt.Println("on new client")

				s, _ := NewKcpSocket(conn)
				go func() {
					for {
						packet, err := s.Recv()
						if nil != err {
							fmt.Println("server recv err:", err)
							break
						}
						s.Send(packet)
					}
					s.Close()
				}()
			}
		}()
	} else {
		log.Fatal(err)
	}

	{

		key := pbkdf2.Key([]byte("demo pass"), []byte("demo salt"), 1024, 32, sha1.New)
		block, _ := kcp.NewAESBlockCrypt(key)
		// dial to the echo server
		if conn, err := kcp.DialWithOptions("127.0.0.1:12345", block, 10, 3); err == nil {
			s, _ := NewKcpSocket(conn)

			s.Send([]byte("hello"))

			packet, err := s.Recv()

			fmt.Println("client", string(packet), err)

			s.Close()
		} else {
			log.Fatal(err)
		}
	}

	listener.Close()
}

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
			s, _ := NewWebSocket(conn)
			go func() {
				for {
					packet, err := s.Recv()
					if nil != err {
						fmt.Println("server recv err:", err)
						break
					}
					s.Send(packet)
				}
				s.Close()
			}()
		})

		go func() {
			http.Serve(listener, nil)
		}()

		{

			u := url.URL{Scheme: "ws", Host: "localhost:8110", Path: "/echo"}
			dialer := gorilla.DefaultDialer

			conn, _, _ := dialer.Dial(u.String(), nil)

			respChan := make(chan struct{})
			s, _ := NewWebSocket(conn)

			conn.WriteMessage(gorilla.PingMessage, []byte("hello"))

			conn.SetPongHandler(func(appData string) error {
				fmt.Println("on pong")
				close(respChan)
				return nil
			})

			go func() {
				//触发recv接收Pong
				s.Recv()
			}()

			<-respChan

			s.Close()
		}

		{
			u := url.URL{Scheme: "ws", Host: "localhost:8110", Path: "/echo"}
			dialer := gorilla.DefaultDialer

			conn, _, _ := dialer.Dial(u.String(), nil)

			s, _ := NewWebSocket(conn)

			s.Send([]byte("hello"))

			packet, err := s.Recv()

			fmt.Println("client", string(packet), err)

			s.Close()
		}

		listener.Close()

	}
}

func TestAsynSocket(t *testing.T) {

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
					s, _ := NewTcpSocket(conn)
					as, _ := NewAsynSocket(s, AsynSocketOption{
						CloseCallBack: func(_ *AsynSocket, err error) {
							fmt.Println("server closed err:", err)
						},
						HandlePakcet: func(as *AsynSocket, packet interface{}, err error) {
							fmt.Println("server HandlePakcet")
							if nil != err {
								as.Close(err)
							} else {
								fmt.Println("server on packet", string(packet.([]byte)))
								as.Push(packet)
								as.Recv(time.Second)
							}
						},
					})
					as.Recv(time.Second)
				}
			}
		}()

		dialer := &net.Dialer{}

		{
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			s, _ := NewTcpSocket(conn)

			okChan := make(chan struct{})

			as, _ := NewAsynSocket(s, AsynSocketOption{
				CloseCallBack: func(_ *AsynSocket, err error) {
					fmt.Println("client closed err:", err)
				},
				HandlePakcet: func(as *AsynSocket, packet interface{}, err error) {
					if nil != err {
						fmt.Println("on client recv err", err)
						as.Close(err)
						close(okChan)
					} else {
						fmt.Println("client", string(packet.([]byte)))
						close(okChan)
					}
				},
			})

			as.Send([]byte("hello"))
			as.Recv()
			<-okChan
			as.Close(nil)
		}

		{
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			s, _ := NewTcpSocket(conn)
			okChan := make(chan struct{})
			as, _ := NewAsynSocket(s, AsynSocketOption{
				CloseCallBack: func(_ *AsynSocket, err error) {
					fmt.Println("client closed err:", err)
				},
				HandlePakcet: func(as *AsynSocket, packet interface{}, err error) {
					if nil != err {
						as.Close(err)
						close(okChan)
					} else {
						fmt.Println("client", string(packet.([]byte)))
						close(okChan)
					}
				},
			})
			as.Recv()
			<-okChan
			as.Close(nil)
		}

		listener.Close()
	}

	{

		okChan := make(chan struct{})

		tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

		listener, _ := net.ListenTCP("tcp", tcpAddr)

		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				} else {
					fmt.Println("on client")
					i := 0
					s, _ := NewTcpSocket(conn)
					as, _ := NewAsynSocket(s, AsynSocketOption{
						CloseCallBack: func(_ *AsynSocket, err error) {
							fmt.Println("server closed err:", err)
						},
						HandlePakcet: func(as *AsynSocket, packet interface{}, err error) {
							if nil != err {
								as.Close(err)
							} else {
								i = i + len(packet.([]byte))
								fmt.Println(i)
								if i == 100*5 {
									close(okChan)
								} else {
									as.Recv(time.Second)
								}
							}
						},
					})
					as.Recv(time.Second)
				}
			}
		}()

		dialer := &net.Dialer{}

		{
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			s, _ := NewTcpSocket(conn)

			as, _ := NewAsynSocket(s, AsynSocketOption{
				SendChanSize: 1000,
				CloseCallBack: func(_ *AsynSocket, err error) {
					fmt.Println("client closed err:", err)
				},
				HandlePakcet: func(as *AsynSocket, packet interface{}, err error) {
					if nil != err {
						as.Close(err)
					}
				},
			})

			for i := 0; i < 100; i++ {
				as.Push([]byte("hello"))
			}

			as.Close(nil)

			<-okChan
		}

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
					s, _ := NewTcpSocket(conn)
					go func() {
						for {
							packet, err := s.Recv(time.Now().Add(time.Second))
							if nil != err {
								fmt.Println("server recv err:", err)
								break
							}
							s.Send(packet)
						}
						s.Close()
					}()
				}
			}
		}()

		dialer := &net.Dialer{}

		{
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			s, _ := NewTcpSocket(conn)
			s.Send([]byte("hello"))
			packet, err := s.Recv()
			fmt.Println("client", string(packet), err)
			s.Close()
		}

		{
			conn, _ := dialer.Dial("tcp", "localhost:8110")
			s, _ := NewTcpSocket(conn)
			packet, err := s.Recv()
			fmt.Println("client", string(packet), err)
			s.Close()

		}

		listener.Close()
	}
}
