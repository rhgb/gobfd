package udp

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"github.com/rhgb/gobfd/bfd"
	"go.uber.org/zap"
	"math/rand"
	"net"
	"strconv"
	"time"
)

type AgentConfig struct {
	SessionConfig
	IPv6Only      bool
	IPv4Only      bool
	ListenAddress string
	PeerAddresses []string
	DiscrPrefix   uint16
}

type SessionConfig struct {
	DesiredMinTxInterval  uint32
	RequiredMinRxInterval uint32
	DetectMult            uint8
	AuthType              bfd.AuthType
}

type Agent struct {
	network            string
	listenAddr         *net.UDPAddr
	listenConn         *net.UDPConn
	peerAddresses      []string
	sessionByAddr      map[string]*bfd.Session
	sessionByDiscr     map[uint32]*bfd.Session
	dialByAddr         map[string]*net.UDPConn
	discrBase          uint32
	nextSessionIndex   uint16
	sessionConfig      SessionConfig
	rxCpChan           chan receivedControlPacket
	activeConnChan     chan *net.UDPAddr
	deactivateConnChan chan *net.UDPAddr
	logger             *zap.SugaredLogger
}

type receivedControlPacket struct {
	cp   bfd.ControlPacket
	addr *net.UDPAddr
}

type SessionSummary struct {
	Addr        string
	LocalDiscr  string
	RemoteDiscr string
	State       string
}

func seedRand() error {
	var b [8]byte
	_, err := cryptorand.Read(b[:])
	if err == nil {
		rand.Seed(int64(binary.LittleEndian.Uint64(b[:])))
	}
	return err
}

func NewAgent(conf AgentConfig, logger *zap.SugaredLogger) (agent *Agent, err error) {
	logger.Infof("seeding rand")
	err = seedRand()
	if err != nil {
		return nil, err
	}
	logger.Infof("creating new agent with config %v", conf)
	network := "udp"
	if conf.IPv4Only && !conf.IPv6Only {
		network = "udp4"
	} else if conf.IPv6Only && !conf.IPv4Only {
		network = "udp6"
	}
	agent = &Agent{
		network:          network,
		peerAddresses:    conf.PeerAddresses,
		sessionByAddr:    make(map[string]*bfd.Session),
		sessionByDiscr:   make(map[uint32]*bfd.Session),
		dialByAddr:       make(map[string]*net.UDPConn),
		sessionConfig:    conf.SessionConfig,
		rxCpChan:         make(chan receivedControlPacket, 256),
		activeConnChan:   make(chan *net.UDPAddr, 256),
		logger:           logger,
		nextSessionIndex: 1,
	}
	if conf.DiscrPrefix != 0 {
		agent.discrBase = uint32(conf.DiscrPrefix) << 16
		logger.Infof("discriminator base is %x", agent.discrBase)
	} else {
		agent.generateDiscrBase()
	}
	err = agent.listen(conf.ListenAddress)
	if err != nil {
		return nil, err
	}
	go agent.sessionControlLoop()
	for _, addr := range agent.peerAddresses {
		go agent.activateConnToAddr(addr)
	}
	return agent, nil
}

func (a *Agent) generateDiscrBase() {
	a.logger.Debugf("generating discriminator base...")
	a.discrBase = rand.Uint32()
	a.logger.Infof("generated discriminator base is %x", a.discrBase)
}

func (a *Agent) nextDiscr() uint32 {
	next := a.discrBase + uint32(a.nextSessionIndex)
	a.nextSessionIndex++
	a.logger.Debugf("new discriminator %x", next)
	return next
}

func (a *Agent) sessionControlLoop() {
	a.logger.Infof("starting session control loop...")
	for {
		select {
		case rxcp := <-a.rxCpChan:
			a.logger.Debugf("session control receiving control packet")
			var session *bfd.Session
			if rxcp.cp.YourDiscriminator != 0 {
				a.logger.Debugf("selecting session by discr %x", rxcp.cp.YourDiscriminator)
				session = a.sessionByDiscr[rxcp.cp.YourDiscriminator]
			} else {
				addrStr := rxcp.addr.IP.String()
				a.logger.Debugf("selecting session by addr %v", addrStr)
				session = a.sessionByAddr[addrStr]
			}
			if session != nil {
				session.CpRxChan <- rxcp.cp
			} else {
				a.logger.Debugf("no matching session exists, dropping packet")
			}
		case addr := <-a.activeConnChan:
			addrStr := addr.IP.String()
			if a.sessionByAddr[addrStr] == nil {
				a.logger.Infof("creating session for addr %v", addrStr)
				a.newSession(addr)
			} else {
				a.logger.Infof("session already created for addr %v, ignoring", addrStr)
			}
		case addr := <-a.deactivateConnChan:
			addrStr := addr.IP.String()
			session := a.sessionByAddr[addrStr]
			if session != nil {
				a.logger.Infof("deleting session %v for addr %v", session.LocalDiscr(), addrStr)
				delete(a.sessionByDiscr, session.LocalDiscr())
				delete(a.sessionByAddr, addrStr)
				session.Close()
			} else {
				a.logger.Warnf("session destroy engaged but no session found for %v", addrStr)
			}
		}
	}
}

func (a *Agent) listen(addr string) error {
	listenAddr, err := net.ResolveUDPAddr(a.network, addr)
	if err != nil {
		a.logger.Errorf("error resolving address: %v, error: %v", addr, err)
		return err
	}
	conn, err := net.ListenUDP(a.network, listenAddr)
	a.logger.Infof("listening on %v network address %v...", a.network, listenAddr)
	if err != nil {
		a.logger.Errorf("error listening on address: %v, error: %v", listenAddr.String(), err)
		return err
	}
	a.listenAddr = listenAddr
	a.listenConn = conn
	go a.doListen()
	return nil
}

func (a *Agent) doListen() {
	curr := make([]byte, 52)
	for {
		bytes, addr, err := a.listenConn.ReadFromUDP(curr)
		if bytes > 0 {
			packet := curr[:bytes]
			if len(packet) >= bfd.ControlPacketByteLength(packet) {
				cp, err := bfd.UnmarshalReceivedControlPacket(packet)
				if err == nil {
					a.rxCpChan <- receivedControlPacket{
						cp:   *cp,
						addr: addr,
					}
				}
			}
		}
		if err != nil {
			a.logger.Errorf("error reading from udp, error: %v", err)
		}
		if bytes == 0 && err == nil {
			a.logger.Errorf("read 0 bytes from udp without error, backing-off")
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func (a *Agent) newSession(remoteAddr *net.UDPAddr) *bfd.Session {
	discr := a.nextDiscr()
	a.logger.Debugf("creating new session with discriminator %x", discr)
	if a.sessionByDiscr[discr] != nil {
		a.logger.Fatalf("generated session discriminator conflicts: %x", discr)
	}
	s := bfd.NewSession(bfd.SessionConfig{
		LocalDiscr:                discr,
		DesiredMinTxInterval:      a.sessionConfig.DesiredMinTxInterval,
		RequiredMinRxInterval:     a.sessionConfig.RequiredMinRxInterval,
		RequiredMinEchoRxInterval: 0,
		DemandMode:                false,
		DetectMult:                a.sessionConfig.DetectMult,
		AuthType:                  a.sessionConfig.AuthType,
		Role:                      bfd.SessionRoleActive,
	}, a.logger)
	a.sessionByDiscr[discr] = s
	a.sessionByAddr[remoteAddr.IP.String()] = s
	go a.doForwardPacket(s, remoteAddr)
	return s
}

func (a *Agent) doForwardPacket(session *bfd.Session, remoteAddr *net.UDPAddr) {
	for cp := range session.CpTxChan {
		data, err := cp.MarshalBinary()
		if err != nil {
			a.logger.Errorf("error marshal packet to binary, error: %v", err)
			continue
		}
		conn, err := a.getUdpDial(remoteAddr)
		if err != nil {
			a.logger.Errorf("error getting udp dial for addr: %v, error: %v", remoteAddr.String(), err)
			continue
		}
		_, err = conn.Write(data)
		if err != nil {
			a.logger.Errorf("error sending data to addr: %v, error: %v", remoteAddr.String(), err)
		}
	}
}

func (a *Agent) activateConnToAddr(address string) {
	resolved, err := net.ResolveUDPAddr(a.network, address)
	if err != nil {
		a.logger.Errorf("error resolving peer address: %v, error: %v", address, err)
		return
	}
	a.logger.Infof("address %v resolved to %v", address, resolved.String())
	a.activeConnChan <- resolved
}

func (a *Agent) deactivateConnToAddr(address string) {
	resolved, err := net.ResolveUDPAddr(a.network, address)
	if err != nil {
		a.logger.Errorf("error resolving peer address: %v, error: %v", address, err)
		return
	}
	a.logger.Infof("address %v resolved to %v", address, resolved.String())
	a.deactivateConnChan <- resolved
}

// Caution: this method cannot be invoked concurrently.
func (a *Agent) UpdatePeerAddresses(addresses []string) {
	existing := bfd.UniqueStringsSorted(a.peerAddresses)
	incoming := bfd.UniqueStringsSorted(addresses)
	a.peerAddresses = incoming
	toAdd := make([]string, 0, len(addresses))
	toDel := make([]string, 0, len(a.peerAddresses))
	i := 0
	j := 0
	for i < len(existing) || j < len(incoming) {
		if j >= len(incoming) || existing[i] < incoming[j] {
			toDel = append(toDel, existing[i])
			i++
		} else if i >= len(existing) || existing[i] > incoming[j] {
			toAdd = append(toAdd, incoming[j])
			j++
		} else {
			i++
			j++
		}
	}
	if len(toAdd) > 0 {
		a.logger.Infof("adding new peers %v", toAdd)
		for _, addr := range toAdd {
			go a.activateConnToAddr(addr)
		}
	}
	if len(toDel) > 0 {
		a.logger.Infof("removing peers %v", toDel)
		for _, addr := range toDel {
			go a.deactivateConnToAddr(addr)
		}
	}
}

func (a *Agent) Summary() []SessionSummary {
	sum := make([]SessionSummary, 0, len(a.sessionByAddr))
	for addr, session := range a.sessionByAddr {
		sum = append(sum, SessionSummary{
			Addr:        addr,
			LocalDiscr:  strconv.FormatInt(int64(session.LocalDiscr()), 16),
			RemoteDiscr: strconv.FormatInt(int64(session.RemoteDiscr()), 16),
			State:       session.State().String(),
		})
	}
	return sum
}

func (a *Agent) getUdpDial(remoteAddr *net.UDPAddr) (*net.UDPConn, error) {
	conn := a.dialByAddr[remoteAddr.String()]
	var err error
	if conn == nil {
		conn, err = net.DialUDP(a.network, nil, remoteAddr)
		if err != nil {
			return nil, err
		}
		a.dialByAddr[remoteAddr.String()] = conn
	}
	return conn, nil
}
