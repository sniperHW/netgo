package main

import (
	"encoding/binary"
	"errors"
	"github.com/golang/protobuf/proto"
	"github.com/sniperHW/network"
	"time"
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

func (e *PBPacker) Pack(b []byte, o interface{}) ([]byte, error) {
	if _, ok := o.(*Echo); !ok {
		return b, errors.New("unsupport object")
	} else {
		if data, err := proto.Marshal(o.(*Echo)); nil != err {
			return b, err
		} else {
			bu := make([]byte, 4)
			binary.BigEndian.PutUint32(bu, uint32(len(data)))
			b = append(b, bu...)
			return append(b, data...), nil
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
