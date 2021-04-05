package main

import (
	"github.com/mgranderath/traceroute/methods"
	"github.com/mgranderath/traceroute/methods/tcp"
	"github.com/mgranderath/traceroute/methods/udp"
	"log"
	"net"
	"time"
)

func main() {
	ip := net.ParseIP("94.140.14.14")
	tcpTraceroute := tcp.New(ip, methods.TracerouteConfig{
		MaxHops:          20,
		NumMeasurements:  3,
		ParallelRequests: 18,
		Port:             53,
		Timeout:          time.Second / 2,
	})
	res, err := tcpTraceroute.Start()
	if err != nil {
		log.Fatal(err)
	}
	log.Println(res)
	udpTraceroute := udp.New(ip, methods.TracerouteConfig{
		MaxHops:          20,
		NumMeasurements:  3,
		ParallelRequests: 24,
		Port:             53,
		Timeout:          time.Second / 2,
	})
	res, err = udpTraceroute.Start()
	if err != nil {
		log.Fatal(err)
	}
	log.Println(res)
}
