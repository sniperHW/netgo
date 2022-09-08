package network

//go test -race -covermode=atomic -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	"fmt"
	gorilla "github.com/gorilla/websocket"
	"net"
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
							s.SetRecvDeadline(time.Now().Add(time.Second))
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
