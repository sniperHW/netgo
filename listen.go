package network

import (
	"net"
)

func ListenTCP(nettype string, service string, onNewclient func(*net.TCPConn)) (net.Listener, func(), error) {
	tcpAddr, err := net.ResolveTCPAddr(nettype, service)
	if nil != err {
		return nil, nil, err
	}
	listener, err := net.ListenTCP(nettype, tcpAddr)
	if nil != err {
		return nil, nil, err
	}

	serve := func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				if ne, ok := e.(net.Error); ok && ne.Temporary() {
					continue
				} else {
					return
				}
			} else {
				onNewclient(conn.(*net.TCPConn))
			}
		}
	}

	return listener, serve, nil

}
