package main

//go test -race -covermode=atomic -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	"crypto/sha1"
	"github.com/golang/protobuf/proto"
	gorilla "github.com/gorilla/websocket"
	"github.com/sniperHW/network"
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

func serverSocket(s network.Socket) {
	log.Println("on new client")
	as := network.NewAsynSocket(s, network.AsynSocketOption{
		Decoder: &PBDecoder{},
		Packer:  &PBPacker{},
	}).SetCloseCallback(func(_ *network.AsynSocket, err error) {
		log.Println("server closed err:", err)
	}).SetPacketHandler(func(as *network.AsynSocket, packet interface{}) {
		as.Send(packet)
		as.Recv(time.Second)
	})
	as.Recv(time.Second)
}

func clientSocket(s network.Socket) {
	okChan := make(chan struct{})
	count := int32(0)

	as := network.NewAsynSocket(s, network.AsynSocketOption{
		Decoder: &PBDecoder{},
		Packer:  &PBPacker{},
	}).SetCloseCallback(func(_ *network.AsynSocket, err error) {
		log.Println("client closed err:", err)
	}).SetPacketHandler(func(as *network.AsynSocket, packet interface{}) {
		if atomic.AddInt32(&count, 1) == 100 {
			close(okChan)
		} else {
			as.Recv()
		}
	})

	as.Recv()
	for i := 0; i < 100; i++ {
		as.Send(&Echo{Msg: proto.String("hello")})
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
				s, _ := network.NewKcpSocket(conn, &PacketReceiver{buff: make([]byte, 4096)})
				serverSocket(s)
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
			s, _ := network.NewKcpSocket(conn, &PacketReceiver{buff: make([]byte, 4096)})
			clientSocket(s)
		} else {
			log.Fatal(err)
		}
	}

	listener.Close()
}

func TestEchoTCP(t *testing.T) {

	tcpAddr, _ := net.ResolveTCPAddr("tcp", "localhost:8110")

	listener, _ := net.ListenTCP("tcp", tcpAddr)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			} else {
				log.Println("on client")
				s, _ := network.NewTcpSocket(conn, &PacketReceiver{buff: make([]byte, 4096)})
				serverSocket(s)
			}
		}
	}()

	dialer := &net.Dialer{}

	{
		conn, _ := dialer.Dial("tcp", "localhost:8110")
		s, _ := network.NewTcpSocket(conn, &PacketReceiver{buff: make([]byte, 4096)})
		clientSocket(s)
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
		s, _ := network.NewWebSocket(conn, &PacketReceiver{buff: make([]byte, 4096)})
		serverSocket(s)
	})

	go func() {
		http.Serve(listener, nil)
	}()

	u := url.URL{Scheme: "ws", Host: "localhost:8110", Path: "/echo"}
	dialer := gorilla.DefaultDialer

	{
		conn, _, _ := dialer.Dial(u.String(), nil)
		s, _ := network.NewWebSocket(conn, &PacketReceiver{buff: make([]byte, 4096)})
		clientSocket(s)
	}

	listener.Close()
}
