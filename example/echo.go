package main

import (
	"encoding/binary"
	"errors"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/sniperHW/network"
)

type PBDecoder struct {
}

func (d *PBDecoder) Decode(b []byte) (interface{}, error) {
	o := &Echo{}
	if err := proto.Unmarshal(b, o); nil != err {
		return nil, err
	} else {
		return o, nil
	}
}

type PBPacker struct {
}

func (e *PBPacker) Pack(b []byte, o interface{}) []byte {
	if _, ok := o.(*Echo); !ok {
		return b
	} else {
		if data, err := proto.Marshal(o.(*Echo)); nil != err {
			return b
		} else {
			bu := make([]byte, 4)
			binary.BigEndian.PutUint32(bu, uint32(len(data)))
			b = append(b, bu...)
			return append(b, data...)
		}
	}
}

type PacketReceiver struct {
	r    int
	w    int
	buff []byte
}

func (r *PacketReceiver) read(readable network.ReadAble, deadline time.Time) (n int, err error) {
	if deadline.IsZero() {
		readable.SetReadDeadline(time.Time{})
		n, err = readable.Read(r.buff[r.w:])
	} else {
		readable.SetReadDeadline(deadline)
		n, err = readable.Read(r.buff[r.w:])
	}
	return
}

func (r *PacketReceiver) Recv(readable network.ReadAble, deadline time.Time) (pkt []byte, err error) {
	const lenHead int = 4
	for {
		rr := r.r
		pktLen := 0
		if (r.w-rr) >= lenHead && uint32(r.w-rr-lenHead) >= binary.BigEndian.Uint32(r.buff[rr:]) {
			pktLen = int(binary.BigEndian.Uint32(r.buff[rr:]))
			rr += lenHead
		}

		if pktLen > 0 {
			if pktLen > (len(r.buff) - lenHead) {
				err = errors.New("pkt too large")
				return
			}
			if (r.w - rr) >= pktLen {
				pkt = r.buff[rr : rr+pktLen]
				rr += pktLen
				r.r = rr
				if r.r == r.w {
					r.r = 0
					r.w = 0
				}
				return
			}
		}

		if r.r > 0 {
			//移动到头部
			copy(r.buff, r.buff[r.r:r.w])
			r.w = r.w - r.r
			r.r = 0
		}

		var n int
		n, err = r.read(readable, deadline)
		if n > 0 {
			r.w += n
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
	tcpAddr, _ := net.ResolveTCPAddr("tcp", logicService)
	listener, _ := net.ListenTCP("tcp", tcpAddr)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			} else {
				network.NewAsynSocket(network.NewTcpSocket(conn.(*net.TCPConn), &PacketReceiver{buff: make([]byte, 4096)}),
					network.AsynSocketOption{
						Decoder:  &PBDecoder{},
						Packer:   &PBPacker{},
						AutoRecv: true,
					}).SetPacketHandler(func(as *network.AsynSocket, packet interface{}) {
					as.Send(packet)
				}).Recv()
			}
		}
	}()
}

func runGateSvr() {
	dialer := &net.Dialer{}
	tcpAddr, _ := net.ResolveTCPAddr("tcp", gateService)
	listener, _ := net.ListenTCP("tcp", tcpAddr)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			} else {
				go func() {
					cli := network.NewTcpSocket(conn.(*net.TCPConn))
					var (
						logic network.Socket
					)

					for i := 0; i < 3; i++ {
						if logicConn, err := dialer.Dial("tcp", logicService); nil != err {
							time.Sleep(time.Second)
						} else {
							logic = network.NewTcpSocket(logicConn.(*net.TCPConn))
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
			}
		}
	}()
}

func runClient() {
	dialer := &net.Dialer{}
	var (
		s network.Socket
	)

	for {
		if conn, err := dialer.Dial("tcp", gateService); nil != err {
			time.Sleep(time.Second)
		} else {
			s = network.NewTcpSocket(conn.(*net.TCPConn), &PacketReceiver{buff: make([]byte, 4096)})
			break
		}
	}

	okChan := make(chan struct{})
	count := int32(0)

	as := network.NewAsynSocket(s, network.AsynSocketOption{
		Decoder: &PBDecoder{},
		Packer:  &PBPacker{},
	}).SetCloseCallback(func(_ *network.AsynSocket, err error) {
		log.Println("client closed err:", err)
	}).SetPacketHandler(func(as *network.AsynSocket, packet interface{}) {
		c := atomic.AddInt32(&count, 1)
		log.Println("go echo resp", c)
		if c == 100 {
			close(okChan)
		} else {
			as.Recv()
		}
	}).Recv()

	for i := 0; i < 100; i++ {
		as.Send(&Echo{Msg: proto.String("hello")})
	}
	<-okChan
	as.Close(nil)
}

func main() {
	runLogicSvr()
	runGateSvr()
	runClient()
}
