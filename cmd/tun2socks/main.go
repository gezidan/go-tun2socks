package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/eycorsican/go-tun2socks/common/dns"
	"github.com/eycorsican/go-tun2socks/core"
	"github.com/eycorsican/go-tun2socks/filter"
	"github.com/eycorsican/go-tun2socks/tun"
)

var handlerCreater = make(map[string]func(), 0)

func registerHandlerCreater(name string, creater func()) {
	handlerCreater[name] = creater
}

var postFlagsInitFn = make([]func(), 0)

func addPostFlagsInitFn(fn func()) {
	postFlagsInitFn = append(postFlagsInitFn, fn)
}

type CmdArgs struct {
	TunName         *string
	TunAddr         *string
	TunGw           *string
	TunMask         *string
	TunDns          *string
	ProxyType       *string
	VConfig         *string
	Gateway         *string
	SniffingType    *string
	ProxyServer     *string
	ProxyHost       *string
	ProxyPort       *uint16
	ProxyCipher     *string
	ProxyPassword   *string
	DelayICMP       *int
	UdpTimeout      *time.Duration
	Applog          *bool
	DisableDnsCache *bool
}

var args = new(CmdArgs)

var lwipWriter io.Writer

var dnsCache dns.DnsCache

const (
	MTU = 1500
)

func main() {
	args.TunName = flag.String("tunName", "tun1", "TUN interface name")
	args.TunAddr = flag.String("tunAddr", "240.0.0.2", "TUN interface address")
	args.TunGw = flag.String("tunGw", "240.0.0.1", "TUN interface gateway")
	args.TunMask = flag.String("tunMask", "255.255.255.0", "TUN interface netmask, as for IPv6, it's the prefixlen")
	args.TunDns = flag.String("tunDns", "114.114.114.114,223.5.5.5", "DNS resolvers for TUN interface (only need on Windows)")
	args.ProxyType = flag.String("proxyType", "socks", "Proxy handler type, e.g. socks, shadowsocks, v2ray")
	args.DelayICMP = flag.Int("delayICMP", 10, "Delay ICMP packets for a short period of time, in milliseconds")

	flag.Parse()

	// Initialization ops after parsing flags.
	for _, fn := range postFlagsInitFn {
		if fn != nil {
			fn()
		}
	}

	// Open the tun device.
	dnsServers := strings.Split(*args.TunDns, ",")
	tunDev, err := tun.OpenTunDevice(*args.TunName, *args.TunAddr, *args.TunGw, *args.TunMask, dnsServers)
	if err != nil {
		log.Fatalf("failed to open tun device: %v", err)
	}

	// Setup TCP/IP stack.
	lwipWriter := core.NewLWIPStack().(io.Writer)

	// Wrap a writer to delay ICMP packets if delay time is not zero.
	if *args.DelayICMP > 0 {
		log.Printf("ICMP packets will be delayed for %dms", *args.DelayICMP)
		lwipWriter = filter.NewICMPFilter(lwipWriter, *args.DelayICMP).(io.Writer)
	}

	// Wrap a writer to print out processes the creating network connections.
	if *args.Applog {
		log.Printf("App logging is enabled")
		lwipWriter = filter.NewApplogFilter(lwipWriter).(io.Writer)
	}

	// Register TCP and UDP handlers to handle accepted connections.
	if creater, found := handlerCreater[*args.ProxyType]; found {
		creater()
	} else {
		log.Fatal("unsupported proxy type")
	}

	// Register an output callback to write packets output from lwip stack to tun
	// device, output function should be set before input any packets.
	core.RegisterOutputFn(func(data []byte) (int, error) {
		return tunDev.Write(data)
	})

	// Copy packets from tun device to lwip stack, it's the main loop.
	go func() {
		_, err := io.CopyBuffer(lwipWriter, tunDev, make([]byte, MTU))
		if err != nil {
			log.Fatalf("copying data failed: %v", err)
		}
	}()

	log.Printf("Running tun2socks")

	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGHUP)
	<-osSignals
}
