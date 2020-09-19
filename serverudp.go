package main

import (
	"net"
	"sync/atomic"
	"time"

	"github.com/aler9/gortsplib"
)

type udpBufAddrPair struct {
	buf  []byte
	addr *net.UDPAddr
}

type serverUDP struct {
	p          *program
	pc         *net.UDPConn
	streamType gortsplib.StreamType
	readBuf    *multiBuffer

	writec chan udpBufAddrPair
	done   chan struct{}
}

func newServerUDP(p *program, port int, streamType gortsplib.StreamType) (*serverUDP, error) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{
		Port: port,
	})
	if err != nil {
		return nil, err
	}

	l := &serverUDP{
		p:          p,
		pc:         pc,
		streamType: streamType,
		readBuf:    newMultiBuffer(2, clientUDPReadBufferSize),
		writec:     make(chan udpBufAddrPair),
		done:       make(chan struct{}),
	}

	l.log("opened on :%d", port)
	return l, nil
}

func (l *serverUDP) log(format string, args ...interface{}) {
	var label string
	if l.streamType == gortsplib.StreamTypeRtp {
		label = "RTP"
	} else {
		label = "RTCP"
	}
	l.p.log("[UDP/"+label+" listener] "+format, args...)
}

func (l *serverUDP) run() {
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for w := range l.writec {
			l.pc.SetWriteDeadline(time.Now().Add(l.p.conf.WriteTimeout))
			l.pc.WriteTo(w.buf, w.addr)
		}
	}()

	for {
		buf := l.readBuf.next()
		n, addr, err := l.pc.ReadFromUDP(buf)
		if err != nil {
			break
		}

		pub := l.p.udpPublishersMap.get(makeUDPPublisherAddr(addr.IP, addr.Port))
		if pub == nil {
			continue
		}

		// client sent RTP on RTCP port or vice-versa
		if pub.streamType != l.streamType {
			continue
		}

		atomic.StoreInt64(pub.client.udpLastFrameTimes[pub.trackId], time.Now().Unix())

		pub.client.rtcpReceivers[pub.trackId].OnFrame(l.streamType, buf[:n])

		l.p.readersMap.forwardFrame(pub.client.path,
			pub.trackId,
			l.streamType,
			buf[:n])

	}

	close(l.writec)
	<-writeDone

	close(l.done)
}

func (l *serverUDP) close() {
	l.pc.Close()
	<-l.done
}

func (l *serverUDP) write(data []byte, addr *net.UDPAddr) {
	l.writec <- udpBufAddrPair{data, addr}
}