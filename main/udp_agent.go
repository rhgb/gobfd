package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/rhgb/gobfd/bfd"
	"github.com/rhgb/gobfd/udp"
	"go.uber.org/zap"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	loggerInstance, _ := zap.NewDevelopment()
	defer loggerInstance.Sync()
	logger := loggerInstance.Sugar()

	agentConfig := udp.AgentConfig{}
	helpFlag := flag.Bool("h", false, "print help")
	flag.BoolVar(&agentConfig.IPv4Only, "4", false, "use IPv4 only")
	flag.BoolVar(&agentConfig.IPv6Only, "6", false, "use IPv6 only")
	flag.StringVar(&agentConfig.ListenAddress, "l", ":3784", "BFD agent listen address")

	idFlag := flag.Uint("id", 0, "agent id (used as discriminator prefix), uint16")

	minTxIntervalFlag := flag.Uint("tx", 100_000, "bfd.DesiredMinTxInterval in microseconds")
	minRxIntervalFlag := flag.Uint("rx", 100_000, "bfd.RequiredMinRxInterval in microseconds")
	detectMultFlag := flag.Uint("mult", 5, "bfd.DetectMult")

	remoteAddrsFlag := flag.String("target", "", "static target system addresses (separate by comma)")
	dnsNameFlag := flag.String("lookup", "", "DNS name to lookup (only A records are recognized)")
	targetPortFlag := flag.Int("lookup-port", 3784, "target port (used in combination with -lookup)")

	httpManageListenAddrFlag := flag.String("manage-listen", ":8080", "http listen address for manage endpoints")
	flag.Parse()

	if *helpFlag {
		println("Usage: agent [-options]")
		flag.PrintDefaults()
		os.Exit(0)
	}

	if agentConfig.IPv4Only && agentConfig.IPv6Only {
		logger.Fatalf("option -4 and -6 cannot appear at same time")
	}

	if *idFlag > math.MaxUint16 {
		logger.Fatalf("illegal -id value %v", *idFlag)
	}
	agentConfig.DiscrPrefix = uint16(*idFlag)

	if *minTxIntervalFlag > math.MaxUint32 {
		logger.Fatalf("illegal -tx value %v", *minTxIntervalFlag)
	}
	agentConfig.DesiredMinTxInterval = uint32(*minTxIntervalFlag)

	if *minRxIntervalFlag > math.MaxUint32 {
		logger.Fatalf("illegal -rx value %v", *minRxIntervalFlag)
	}
	agentConfig.RequiredMinRxInterval = uint32(*minRxIntervalFlag)

	if *detectMultFlag > math.MaxUint8 {
		logger.Fatalf("illegal -mult value %v", *detectMultFlag)
	}
	agentConfig.DetectMult = uint8(*detectMultFlag)

	staticPeers := make([]string, 0)
	if len(*remoteAddrsFlag) > 0 {
		staticPeers = bfd.UniqueStringsSorted(strings.Split(*remoteAddrsFlag, ","))
	}
	dnsPeers := resolveDnsPeers(agentConfig, *dnsNameFlag, *targetPortFlag, logger)
	peers := append(dnsPeers, staticPeers...)
	logger.Infof("peers to connect: %v", peers)
	agentConfig.PeerAddresses = peers

	agent, err := udp.NewAgent(agentConfig, logger)
	if err != nil {
		logger.Fatalf("error creating bfd agent, error: %v", err)
	}
	err = startAgentManager(*httpManageListenAddrFlag, agent, logger)
	if err != nil {
		logger.Fatalf("error start manage endpoint, error: %v", err)
	}
	go watchDnsName(agent, staticPeers, agentConfig, *dnsNameFlag, *targetPortFlag, logger)
	for {
		time.Sleep(time.Second)
	}
}

func resolveDnsPeers(agentConfig udp.AgentConfig, dnsName string, targetPort int, logger *zap.SugaredLogger) []string {
	peers := make([]string, 0)
	if len(dnsName) > 0 {
		if targetPort <= 0 || targetPort > 65535 {
			logger.Fatalf("illegal -lookup-port value %v", targetPort)
		}
		localAddrs, err := net.InterfaceAddrs()
		if err != nil {
			logger.Errorf("cannot get local addresses, error: %v", err)
		}
		ips, err := net.LookupIP(dnsName)
		if err != nil {
			logger.Fatalf("error lookup dns name %v, error: %v", dnsName, err)
		}
		logger.Infof("resolved addresses for name %v: %v", dnsName, ips)
		peers = make([]string, 0, len(ips))
	outer:
		for _, ip := range ips {
			if agentConfig.IPv4Only && ip.To4() == nil {
				continue
			}
			if agentConfig.IPv6Only && ip.To4() != nil {
				continue
			}
			for _, addr := range localAddrs {
				ipNet, ok := addr.(*net.IPNet)
				if ok && ipNet.Contains(ip) {
					logger.Debugf("target %v belongs to local addr %v, skipping", ip.String(), addr.String())
					continue outer
				}
			}
			peers = append(peers, fmt.Sprintf("%v:%v", ip.String(), targetPort))
		}
	}
	return peers
}

func startAgentManager(listenAddr string, agent *udp.Agent, logger *zap.SugaredLogger) error {
	http.HandleFunc("/summary", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" {
			writer.WriteHeader(405)
			return
		}
		jsonBytes, err := json.Marshal(agent.Summary())
		if err != nil {
			logger.Errorf("error marshal summary, error: %v", err)
			writer.WriteHeader(500)
			return
		}

		writer.Header().Add("Content-Type", "application/json")
		writer.WriteHeader(200)
		_, _ = writer.Write(jsonBytes)
	})
	logger.Infof("starting manage endpoint at %v...", listenAddr)
	return http.ListenAndServe(listenAddr, nil)
}

func watchDnsName(agent *udp.Agent, staticPeers []string, agentConfig udp.AgentConfig, dnsNameFlag string, targetPortFlag int, logger *zap.SugaredLogger) {
	for range time.Tick(10 * time.Second) {
		dnsPeers := resolveDnsPeers(agentConfig, dnsNameFlag, targetPortFlag, logger)
		agent.UpdatePeerAddresses(append(dnsPeers, staticPeers...))
	}
}
