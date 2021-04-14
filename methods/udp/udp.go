package udp

import (
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/mgranderath/traceroute/listener_channel"
	"github.com/mgranderath/traceroute/methods"
	"github.com/mgranderath/traceroute/parallel_limiter"
	"github.com/mgranderath/traceroute/signal"
	"github.com/mgranderath/traceroute/taskgroup"
	"github.com/mgranderath/traceroute/util"
	"golang.org/x/net/context"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"log"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"
)

type inflightData struct {
	start time.Time
	ttl   uint16
	done  chan struct{}
}

type opConfig struct {
	destIP net.IP
	wg     *taskgroup.TaskGroup

	icmpConn net.PacketConn

	ctx    context.Context
	cancel context.CancelFunc
}

type results struct {
	inflightRequests sync.Map

	results   map[uint16][]methods.TracerouteHop
	resultsMu sync.Mutex
	err       error

	concurrentRequests *parallel_limiter.ParallelLimiter
	reachedFinalHop    *signal.Signal
}

type Traceroute struct {
	trcrtConfig methods.TracerouteConfig
	opConfig    opConfig
	results     results
}

func New(destIP net.IP, config methods.TracerouteConfig) *Traceroute {
	return &Traceroute{
		opConfig: opConfig{
			destIP: destIP,
		},
		trcrtConfig: config,
	}
}

func (tr *Traceroute) Start() (*map[uint16][]methods.TracerouteHop, error) {
	tr.opConfig.ctx, tr.opConfig.cancel = context.WithCancel(context.Background())

	tr.results = results{
		inflightRequests:   sync.Map{},
		concurrentRequests: parallel_limiter.New(int(tr.trcrtConfig.ParallelRequests)),
		results:            map[uint16][]methods.TracerouteHop{},
		reachedFinalHop:    signal.New(),
	}

	var err error
	tr.opConfig.icmpConn, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, err
	}

	return tr.start()
}

func (tr *Traceroute) addToResult(ttl uint16, hop methods.TracerouteHop) {
	tr.results.resultsMu.Lock()
	defer tr.results.resultsMu.Unlock()
	if tr.results.results[ttl] == nil {
		tr.results.results[ttl] = []methods.TracerouteHop{}
	}

	tr.results.results[ttl] = append(tr.results.results[ttl], hop)
}

func (tr *Traceroute) sendMessage(ttl uint16) {
	srcIP, srcPort := util.LocalIPPort(tr.opConfig.destIP)

	udpConn, err := net.ListenPacket("udp", ":"+strconv.Itoa(srcPort))
	if err != nil {
		log.Fatal(err)
	}

	ipHeader := &layers.IPv4{
		SrcIP:    srcIP,
		DstIP:    tr.opConfig.destIP,
		Protocol: layers.IPProtocolTCP,
		TTL:      uint8(ttl),
	}

	udpHeader := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(tr.trcrtConfig.Port),
	}
	_ = udpHeader.SetNetworkLayerForChecksum(ipHeader)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, udpHeader, gopacket.Payload("HAJSFJHKAJSHFKJHAJKFHKASHKFHHKAFKHFAHSJK")); err != nil {
		tr.results.err = err
		tr.opConfig.cancel()
		return
	}

	err = ipv4.NewPacketConn(udpConn).SetTTL(int(ttl))
	if err != nil {
		tr.results.err = err
		tr.opConfig.cancel()
		return
	}

	start := time.Now()
	if _, err := udpConn.WriteTo(buf.Bytes(), &net.UDPAddr{IP: tr.opConfig.destIP, Port: tr.trcrtConfig.Port}); err != nil {
		tr.results.err = err
		tr.opConfig.cancel()
		return
	}

	inflight := inflightData{
		start: start,
		ttl:   ttl,
		done:  make(chan struct{}),
	}

	tr.results.inflightRequests.Store(uint16(srcPort), inflight)

	go func() {
		for {
			select {
			case <-tr.opConfig.ctx.Done():
				udpConn.Close()
				return
			case <-inflight.done:
				udpConn.Close()
				return
			default:
			}
			reply := make([]byte, 1500)
			err = udpConn.SetReadDeadline(time.Now().Add(time.Second))
			if err != nil {
				continue
			}
			n, peer, err := udpConn.ReadFrom(reply)
			if err != nil {
				continue
			}

			val, ok := tr.results.inflightRequests.LoadAndDelete(uint16(srcPort))
			if !ok {
				continue
			}

			request := val.(inflightData)
			elapsed := time.Since(request.start)
			if peer.(*net.UDPAddr).IP.String() == tr.opConfig.destIP.String() {
				tr.results.reachedFinalHop.Signal()
			}
			tr.addToResult(request.ttl, methods.TracerouteHop{
				Success: true,
				Address: &net.IPAddr{IP: peer.(*net.UDPAddr).IP},
				N:       &n,
				TTL:     request.ttl,
				RTT:     &elapsed,
			})
			tr.opConfig.wg.Done()
			tr.results.concurrentRequests.Finished()
			close(request.done)
		}
	}()
}

func (tr *Traceroute) handleICMPMessage(msg listener_channel.ReceivedMessage, data []byte) {
	header := methods.GetICMPResponsePayload(data)
	srcPort := methods.GetUDPSrcPort(header)
	val, ok := tr.results.inflightRequests.LoadAndDelete(srcPort)
	if !ok {
		return
	}
	request := val.(inflightData)
	elapsed := time.Since(request.start)
	if msg.Peer.String() == tr.opConfig.destIP.String() {
		tr.results.reachedFinalHop.Signal()
	}
	tr.addToResult(request.ttl, methods.TracerouteHop{
		Success: true,
		Address: msg.Peer,
		N:       msg.N,
		TTL:     request.ttl,
		RTT:     &elapsed,
	})
	tr.results.concurrentRequests.Finished()
	tr.opConfig.wg.Done()
	close(request.done)
}

func (tr *Traceroute) icmpListener() {
	lc := listener_channel.New(tr.opConfig.icmpConn)

	defer lc.Stop()

	go lc.Start()

	for {
		select {
		case <-tr.opConfig.ctx.Done():
			return
		case msg := <-lc.Messages:
			if msg.N == nil {
				continue
			}
			rm, err := icmp.ParseMessage(1, msg.Msg[:*msg.N])
			if err != nil {
				log.Println(err)
				continue
			}
			switch rm.Type {
			case ipv4.ICMPTypeTimeExceeded:
				body := rm.Body.(*icmp.TimeExceeded).Data
				tr.handleICMPMessage(msg, body)
			case ipv4.ICMPTypeDestinationUnreachable:
				body := rm.Body.(*icmp.DstUnreach).Data
				tr.handleICMPMessage(msg, body)
			default:
				log.Println("received icmp message of unknown type", rm.Type)
			}
		}
	}
}

func (tr *Traceroute) timeoutLoop() {
	ticker := time.NewTicker(tr.trcrtConfig.Timeout / 4)
	go func() {
		for range ticker.C {
			tr.results.inflightRequests.Range(func(key, value interface{}) bool {
				request := value.(inflightData)
				expired := time.Since(request.start) > tr.trcrtConfig.Timeout
				if !expired {
					return true
				}
				tr.results.inflightRequests.Delete(key)
				tr.addToResult(request.ttl, methods.TracerouteHop{
					Success: false,
					TTL:     request.ttl,
				})
				tr.results.concurrentRequests.Finished()
				tr.opConfig.wg.Done()
				close(request.done)
				return true
			})
		}
	}()
	select {
	case <-tr.opConfig.ctx.Done():
		ticker.Stop()
	}
}

func (tr *Traceroute) sendLoop() {
	rand.Seed(time.Now().UTC().UnixNano())

	wg := taskgroup.New()
	tr.opConfig.wg = wg

	defer func() {
		log.Println("before wait")
		wg.Wait()
		log.Println("after wait")
	}()

	for ttl := uint16(1); ttl <= tr.trcrtConfig.MaxHops; ttl++ {
		select {
		case <-tr.results.reachedFinalHop.Chan():
			return
		default:
		}
		for i := 0; i < int(tr.trcrtConfig.NumMeasurements); i++ {
			select {
			case <-tr.opConfig.ctx.Done():
				return
			case <-tr.results.concurrentRequests.Start():
				wg.Add()
				go tr.sendMessage(ttl)
			}
		}
	}
}

func (tr *Traceroute) start() (*map[uint16][]methods.TracerouteHop, error) {
	go tr.timeoutLoop()
	go tr.icmpListener()

	tr.sendLoop()
	tr.opConfig.cancel()
	tr.opConfig.icmpConn.Close()

	if tr.results.err != nil {
		return nil, tr.results.err
	}

	result := methods.ReduceFinalResult(tr.results.results, tr.trcrtConfig.MaxHops, tr.opConfig.destIP)

	return &result, tr.results.err
}
