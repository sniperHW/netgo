package main

import (
	"encoding/binary"
	"errors"
	"github.com/sniperHW/netgo"
	"github.com/sniperHW/netgo/example/pb"
	"google.golang.org/protobuf/proto"
	"log"
	"net"
	"sync/atomic"
	"time"
)

type PBCodec struct {
	r    int
	w    int
	buff []byte
}

func (codec *PBCodec) Decode(b []byte) (interface{}, error) {
	o := &pb.Echo{}
	if err := proto.Unmarshal(b, o); nil != err {
		return nil, err
	} else {
		return o, nil
	}
}

func (codec *PBCodec) Encode(b []byte, o interface{}) []byte {
	if _, ok := o.(*pb.Echo); !ok {
		return b
	} else {
		if data, err := proto.Marshal(o.(*pb.Echo)); nil != err {
			return b
		} else {
			bu := make([]byte, 4)
			binary.BigEndian.PutUint32(bu, uint32(len(data)))
			b = append(b, bu...)
			return append(b, data...)
		}
	}
}

func (codec *PBCodec) read(readable netgo.ReadAble, deadline time.Time) (int, error) {
	if err := readable.SetReadDeadline(deadline); err != nil {
		return 0, err
	} else {
		return readable.Read(codec.buff[codec.w:])
	}
}

func (codec *PBCodec) Recv(readable netgo.ReadAble, deadline time.Time) (pkt []byte, err error) {
	const lenHead int = 4
	for {
		rr := codec.r
		pktLen := 0
		if (codec.w-rr) >= lenHead && uint32(codec.w-rr-lenHead) >= binary.BigEndian.Uint32(codec.buff[rr:]) {
			pktLen = int(binary.BigEndian.Uint32(codec.buff[rr:]))
			rr += lenHead
		}

		if pktLen > 0 {
			if pktLen > (len(codec.buff) - lenHead) {
				err = errors.New("pkt too large")
				return
			}
			if (codec.w - rr) >= pktLen {
				pkt = codec.buff[rr : rr+pktLen]
				rr += pktLen
				codec.r = rr
				if codec.r == codec.w {
					codec.r = 0
					codec.w = 0
				}
				return
			}
		}

		if codec.r > 0 {
			//移动到头部
			copy(codec.buff, codec.buff[codec.r:codec.w])
			codec.w = codec.w - codec.r
			codec.r = 0
		}

		var n int
		n, err = codec.read(readable, deadline)
		if n > 0 {
			codec.w += n
		}
		if nil != err {
			return
		}
	}
}

const (
	logicService string = "localhost:8111"
	gateService  string = "localhost:8110"
)

func runLogicSvr() {
	_, serve, _ := netgo.ListenTCP("tcp", logicService, func(conn *net.TCPConn) {
		codec := &PBCodec{buff: make([]byte, 4096)}
		netgo.NewAsynSocket(netgo.NewTcpSocket(conn, codec),
			netgo.AsynSocketOption{
				Codec:    codec,
				AutoRecv: true,
			}).SetPacketHandler(func(as *netgo.AsynSocket, packet interface{}) {
			as.Send(packet)
		}).Recv()
	})

	go serve()

}

func runGateSvr() {
	dialer := &net.Dialer{}
	_, serve, _ := netgo.ListenTCP("tcp", gateService, func(conn *net.TCPConn) {
		go func() {
			cli := netgo.NewTcpSocket(conn)
			var (
				logic netgo.Socket
			)

			for i := 0; i < 3; i++ {
				if logicConn, err := dialer.Dial("tcp", logicService); nil != err {
					time.Sleep(time.Second)
				} else {
					logic = netgo.NewTcpSocket(logicConn.(*net.TCPConn))
					break
				}
			}

			if nil == logic {
				cli.Close()
			} else {
				defer func() {
					cli.Close()
					logic.Close()
				}()

				for {
					var n int
					//recv a from client
					dataClient, err := cli.Recv()
					if nil != err {
						return
					}

					//send to logic
					n, err = logic.Send(dataClient)
					if nil != err {
						return
					}

					for n > 0 {
						//recv response from logic
						dataLogic, err := logic.Recv()
						if nil != err {
							return
						}
						n -= len(dataLogic)
						//response client
						_, err = cli.Send(dataLogic)
						if nil != err {
							return
						}
					}
				}
			}
		}()
	})
	go serve()
}

func runClient() {
	dialer := &net.Dialer{}
	var (
		s netgo.Socket
	)

	codec := &PBCodec{buff: make([]byte, 4096)}

	for {
		if conn, err := dialer.Dial("tcp", gateService); nil != err {
			time.Sleep(time.Second)
		} else {
			s = netgo.NewTcpSocket(conn.(*net.TCPConn), codec)
			break
		}
	}

	okChan := make(chan struct{})
	count := int32(0)

	as := netgo.NewAsynSocket(s, netgo.AsynSocketOption{
		Codec: codec,
	}).SetCloseCallback(func(_ *netgo.AsynSocket, err error) {
		log.Println("client closed err:", err)
	}).SetPacketHandler(func(as *netgo.AsynSocket, packet interface{}) {
		c := atomic.AddInt32(&count, 1)
		log.Println("go echo resp", c)
		if c == 100 {
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

func main() {
	runLogicSvr()
	runGateSvr()
	runClient()
}
