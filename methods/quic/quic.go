package quic

import (
	"crypto/tls"
	"github.com/lucas-clemente/quic-go"
	"github.com/mgranderath/traceroute/util"
	"golang.org/x/net/ipv4"
	"log"
	"math"
	"net"
	"strconv"
	"time"
)

func SendMessage(destIP net.IP) {
	_, srcPort := util.LocalIPPort(destIP)

	conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(srcPort))

	if err != nil {
		log.Fatal(err)
	}

	err = ipv4.NewPacketConn(conn).SetTTL(15)
	if err != nil {
		log.Fatal(err)
	}

	start := time.Now()
	session, err := quic.Dial(conn, &net.UDPAddr{IP: destIP, Port: 784}, "", &tls.Config{
		InsecureSkipVerify: true,
	}, &quic.Config{
		Versions: []quic.VersionNumber{math.MaxUint32},
	})

	if err != nil {
		log.Fatal(err, time.Since(start))
	}
	if session != nil {
		log.Fatal(session)
	}
}
