package main

//go test -race -covermode=atomic -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	"crypto/sha1"
	gorilla "github.com/gorilla/websocket"
	"github.com/sniperHW/netgo"
	"github.com/sniperHW/netgo/example/pb"
	"github.com/xtaci/kcp-go/v5"
	"golang.org/x/crypto/pbkdf2"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func serverSocket(s netgo.Socket) {
	log.Println("on new client")
	netgo.NewAsynSocket(s, netgo.AsynSocketOption{
		Decoder:         &PBDecoder{},
		Packer:          &PBPacker{},
		AutoRecv:        true,
		AutoRecvTimeout: time.Second,
	}).SetCloseCallback(func(_ *netgo.AsynSocket, err error) {
		log.Println("server closed err:", err)
	}).SetPacketHandler(func(as *netgo.AsynSocket, packet interface{}) {
		as.Send(packet)
	}).Recv(time.Now().Add(time.Second))
}

func clientSocket(s netgo.Socket) {
	okChan := make(chan struct{})
	count := int32(0)

	as := netgo.NewAsynSocket(s, netgo.AsynSocketOption{
		Decoder: &PBDecoder{},
		Packer:  &PBPacker{},
	}).SetCloseCallback(func(_ *netgo.AsynSocket, err error) {
		log.Println("client closed err:", err)
	}).SetPacketHandler(func(as *netgo.AsynSocket, packet interface{}) {
		if atomic.AddInt32(&count, 1) == 100 {
			close(okChan)
		} else {
			as.Recv()
		}
	}).Recv()
	for i := 0; i < 100; i++ {
		as.Send(&pb.Echo{Msg: "hello"})
	}
	<-okChan
	as.Close(nil)
}

func TestEchoKCP(t *testing.T) {

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
				serverSocket(netgo.NewKcpSocket(conn, &PacketReceiver{buff: make([]byte, 4096)}))
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
			clientSocket(netgo.NewKcpSocket(conn, &PacketReceiver{buff: make([]byte, 4096)}))
		} else {
			log.Fatal(err)
		}
	}

	listener.Close()
}

func TestEchoTCP(t *testing.T) {
	listener, serve, _ := netgo.ListenTCP("tcp", "localhost:8110", func(conn *net.TCPConn) {
		log.Println("on client")
		serverSocket(netgo.NewTcpSocket(conn, &PacketReceiver{buff: make([]byte, 4096)}))
	})

	go serve()

	dialer := &net.Dialer{}

	{
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		clientSocket(netgo.NewTcpSocket(conn.(*net.TCPConn), &PacketReceiver{buff: make([]byte, 4096)}))
	}

	listener.Close()
}

func TestEchoWebSocket(t *testing.T) {

	tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

	listener, _ := net.ListenTCP("tcp", tcpAddr)

	upgrader := &gorilla.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		log.Println("on client")
		serverSocket(netgo.NewWebSocket(conn, &PacketReceiver{buff: make([]byte, 4096)}))
	})

	go func() {
		http.Serve(listener, nil)
	}()

	u := url.URL{Scheme: "ws", Host: "localhost:8110", Path: "/echo"}
	dialer := gorilla.DefaultDialer

	{
		conn, _, _ := dialer.Dial(u.String(), nil)
		clientSocket(netgo.NewWebSocket(conn, &PacketReceiver{buff: make([]byte, 4096)}))
	}

	listener.Close()
}
