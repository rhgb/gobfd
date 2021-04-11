package bfd

import (
	"errors"
	"go.uber.org/zap"
	"math/rand"
	"time"
)

type SessionRole uint8

const (
	SessionRoleActive  SessionRole = iota
	SessionRolePassive SessionRole = iota
)

type SessionIntervalParams struct {
	desiredMinTxInterval  uint32
	requiredMinRxInterval uint32
}

type Session struct {
	// normative state variables
	state                 State
	remoteState           State
	localDiscr            uint32
	remoteDiscr           uint32
	localDiag             Diagnostic
	desiredMinTxInterval  uint32
	requiredMinRxInterval uint32
	remoteMinRxInterval   uint32
	demandMode            bool
	remoteDemandMode      bool
	detectMult            uint8
	authType              AuthType
	rcvAuthSeq            uint32
	xmitAuthSeq           uint32
	authSeqKnown          bool
	// channels
	CpRxChan             chan ControlPacket
	CpTxChan             chan ControlPacket
	detectionTimeoutChan chan bool
	intervalParamChan    chan SessionIntervalParams
	pollLockChan         chan bool
	periodicSendChan     chan bool
	adminStateChan       chan bool
	// functional properties
	requiredMinEchoRxInterval      uint32
	lastRxTime                     time.Time
	detectionTimer                 *time.Timer
	configuredDesiredMinTxInterval uint32
	effectiveDesiredMinTxInterval  uint32
	effectiveRequiredMinRxInterval uint32
	role                           SessionRole
	txInterval                     uint32
	txTimer                        *time.Timer
	lastTxTime                     time.Time
	polling                        bool
	logger                         *zap.SugaredLogger
	terminated                     bool
}

type SessionConfig struct {
	LocalDiscr                uint32
	DesiredMinTxInterval      uint32
	RequiredMinRxInterval     uint32
	RequiredMinEchoRxInterval uint32
	DemandMode                bool
	DetectMult                uint8
	AuthType                  AuthType
	Role                      SessionRole
}

func NewSession(conf SessionConfig, logger *zap.SugaredLogger) *Session {

	s := &Session{
		state:                          StateDown,
		remoteState:                    StateDown,
		localDiscr:                     conf.LocalDiscr,
		remoteDiscr:                    0,
		localDiag:                      DiagNone,
		desiredMinTxInterval:           GreaterUInt32(conf.DesiredMinTxInterval, 1_000_000),
		requiredMinRxInterval:          conf.RequiredMinRxInterval,
		remoteMinRxInterval:            1,
		demandMode:                     conf.DemandMode,
		remoteDemandMode:               false,
		detectMult:                     conf.DetectMult,
		authType:                       conf.AuthType,
		rcvAuthSeq:                     0,
		xmitAuthSeq:                    rand.Uint32(),
		authSeqKnown:                   false,
		CpRxChan:                       make(chan ControlPacket, 1),
		CpTxChan:                       make(chan ControlPacket, 1),
		detectionTimeoutChan:           make(chan bool, 1),
		intervalParamChan:              make(chan SessionIntervalParams),
		pollLockChan:                   make(chan bool, 1),
		periodicSendChan:               make(chan bool, 1),
		adminStateChan:                 make(chan bool),
		requiredMinEchoRxInterval:      conf.RequiredMinEchoRxInterval,
		configuredDesiredMinTxInterval: conf.DesiredMinTxInterval,
		effectiveDesiredMinTxInterval:  conf.DesiredMinTxInterval,
		effectiveRequiredMinRxInterval: conf.RequiredMinRxInterval,
		role:                           conf.Role,
		polling:                        false,
		logger:                         logger,
	}
	go s.doReceive()
	go s.doPeriodicSendControlPacket()
	s.rescheduleSend()
	return s
}

func (s *Session) doReceive() {
	s.logger.Infof("session %x receiving events...", s.localDiscr)
	for !s.terminated {
		select {
		case adminDown := <-s.adminStateChan:
			s.setAdminDown(adminDown)
		case ip := <-s.intervalParamChan:
			s.updateIntervalParams(&ip)
		case <-s.detectionTimeoutChan:
			s.detectionTimeOut()
		case cp := <-s.CpRxChan:
			s.receive(&cp)
		}
	}
}

func (s *Session) receive(cp *ControlPacket) {
	if cp.YourDiscriminator == 0 && s.state != StateDown && s.state != StateAdminDown {
		return
	}
	// todo auth?
	// remote state update
	s.remoteState = cp.State
	s.remoteDiscr = cp.MyDiscriminator
	s.remoteMinRxInterval = cp.RequiredMinRXInterval
	s.remoteDemandMode = cp.Demand
	// todo If the Required Min Echo RX Interval field is zero, the transmission
	//      of Echo packets, if any, MUST cease.
	// process Final bit
	if cp.Final && s.polling {
		s.polling = false
		s.effectiveDesiredMinTxInterval = s.desiredMinTxInterval
		s.effectiveRequiredMinRxInterval = s.requiredMinRxInterval
		s.pollUnlock()
	}
	// update tx interval
	s.rescheduleSend()
	// state migration
	if s.state == StateAdminDown {
		return
	}
	prevState := s.state
	if cp.State == StateAdminDown {
		if s.state != StateDown {
			s.localDiag = DiagNeighborSignalDown
			s.state = StateDown
		}
	}
	switch s.state {
	case StateDown:
		if cp.State == StateDown {
			s.state = StateInit
		} else if cp.State == StateInit {
			s.state = StateUp
		}
	case StateInit:
		if cp.State == StateInit || cp.State == StateUp {
			s.state = StateUp
		}
	case StateUp:
		if cp.State == StateDown {
			s.localDiag = DiagNeighborSignalDown
			s.state = StateDown
		}
	}
	if prevState != s.state {
		s.logger.Infof("session %x state %v -> %v", s.localDiscr, prevState.String(), s.state.String())
	}
	// reset desired min tx interval
	if prevState != StateUp && s.state == StateUp {
		if s.desiredMinTxInterval > s.configuredDesiredMinTxInterval {
			s.pollTryLock()
			s.desiredMinTxInterval = s.configuredDesiredMinTxInterval
			s.polling = true
		}
	} else if prevState == StateUp && s.state != StateUp {
		s.desiredMinTxInterval = GreaterUInt32(s.configuredDesiredMinTxInterval, 1_000_000)
	}
	// send final
	if cp.Poll {
		s.sendControlPacket(true)
	}
	// re-schedule detection
	s.lastRxTime = time.Now()
	if s.detectionTimer != nil {
		s.detectionTimer.Stop()
	}
	// todo slower detect time when echo function enabled
	detectTime := time.Duration(cp.DetectMult) * time.Duration(GreaterUInt32(cp.DesiredMinTXInterval, s.effectiveRequiredMinRxInterval)) * time.Microsecond
	s.detectionTimer = time.AfterFunc(detectTime, func() { s.detectionTimeoutChan <- true })
}

func (s *Session) pollUnlock() {
	select {
	case <-s.pollLockChan:
	default:
	}
}

func (s *Session) pollTryLock() bool {
	select {
	case s.pollLockChan <- true:
		return true
	default:
		return false
	}
}

func (s *Session) detectionTimeOut() {
	if s.state == StateInit || s.state == StateUp {
		s.logger.Infof("session %x state %v -> %v due to timeout", s.localDiscr, s.state.String(), StateDown.String())
		s.state = StateDown
		s.localDiag = DiagTimeExpired
	}
}

func (s *Session) rescheduleSend() {
	forbid := (s.role == SessionRolePassive && s.remoteDiscr == 0) ||
		s.remoteMinRxInterval == 0
	txIntervalPrev := s.txInterval
	if forbid {
		s.txInterval = 0
	} else {
		s.txInterval = GreaterUInt32(s.effectiveDesiredMinTxInterval, s.remoteMinRxInterval)
	}
	if txIntervalPrev > 0 && s.txInterval == 0 {
		s.logger.Debugf("ceasing tx, prev interval is %v", txIntervalPrev)
		if s.txTimer != nil {
			s.txTimer.Stop()
		}
	} else if txIntervalPrev == 0 && s.txInterval > 0 {
		s.logger.Debugf("starting tx, new interval is %v", s.txInterval)
		s.periodicSendChan <- true
	} else if txIntervalPrev != s.txInterval {
		s.logger.Debugf("changing tx interval from %v to %v", txIntervalPrev, s.txInterval)
		if s.txTimer != nil {
			s.txTimer.Stop()
		}
		next := time.Until(s.lastTxTime.Add(s.txIntervalJittered() * time.Microsecond))
		s.logger.Debugf("next sending in %v μs", next)
		if next <= 0 {
			s.periodicSendChan <- true
		} else {
			time.AfterFunc(next, func() {
				s.periodicSendChan <- true
			})
		}
	}
}

func (s *Session) txIntervalJittered() time.Duration {
	interval := s.txInterval
	if interval == 0 {
		return 0
	}
	if s.detectMult == 1 {
		interval = interval - uint32(float64(interval)*(0.1+rand.Float64()*0.15))
	} else {
		interval = interval - uint32(float64(interval)*rand.Float64()*0.25)
	}
	return time.Duration(interval)
}

func (s *Session) sendControlPacket(final bool) {
	s.CpTxChan <- ControlPacket{
		Version:                   1,
		Diagnostic:                s.localDiag,
		State:                     s.state,
		Poll:                      !final && s.polling,
		Final:                     final,
		ControlPlaneIndependent:   false,
		AuthenticationPresent:     s.authType != AuthTypeNone,
		Demand:                    s.state == StateUp && s.remoteState == StateUp && s.demandMode,
		Multipoint:                false,
		DetectMult:                s.detectMult,
		Length:                    24, // todo auth
		MyDiscriminator:           s.localDiscr,
		YourDiscriminator:         s.remoteDiscr,
		DesiredMinTXInterval:      s.desiredMinTxInterval,
		RequiredMinRXInterval:     s.requiredMinRxInterval,
		RequiredMinEchoRXInterval: s.requiredMinEchoRxInterval,
		AuthenticationSection:     nil, // todo auth
	}
}

func (s *Session) doPeriodicSendControlPacket() {
	for !s.terminated {
		select {
		case <-s.periodicSendChan:
			s.lastTxTime = time.Now()
			s.sendControlPacket(false)
			nextInterval := s.txIntervalJittered() * time.Microsecond
			if nextInterval > 0 {
				s.txTimer = time.AfterFunc(nextInterval, func() {
					s.periodicSendChan <- true
				})
			}
		case <-time.After(time.Second):
			// escape for terminate
		}
	}
}

func (s *Session) updateIntervalParams(params *SessionIntervalParams) {
	if s.requiredMinRxInterval == params.requiredMinRxInterval &&
		s.configuredDesiredMinTxInterval == params.desiredMinTxInterval {
		s.pollUnlock()
		return
	}
	calcDesiredMinTxInterval := params.desiredMinTxInterval
	if s.state != StateUp {
		calcDesiredMinTxInterval = GreaterUInt32(params.desiredMinTxInterval, 1_000_000)
	}
	shouldPoll := s.requiredMinRxInterval != params.requiredMinRxInterval ||
		s.desiredMinTxInterval != calcDesiredMinTxInterval

	s.configuredDesiredMinTxInterval = params.desiredMinTxInterval
	s.requiredMinRxInterval = params.requiredMinRxInterval
	s.desiredMinTxInterval = calcDesiredMinTxInterval

	if shouldPoll {
		s.polling = true
		if !(s.state == StateUp && s.desiredMinTxInterval > s.effectiveDesiredMinTxInterval) {
			s.effectiveDesiredMinTxInterval = s.desiredMinTxInterval
		}
		if !(s.state == StateUp && s.requiredMinRxInterval < s.effectiveRequiredMinRxInterval) {
			s.effectiveRequiredMinRxInterval = s.requiredMinRxInterval
		}
	}
}

func (s *Session) setAdminDown(adminDown bool) {
	if s.state != StateAdminDown && adminDown {
		s.logger.Infof("session %x state %v -> %v due to admin down", s.localDiscr, s.state.String(), StateAdminDown.String())
		s.state = StateAdminDown
		s.localDiag = DiagAdminDown
	} else if s.state == StateAdminDown && !adminDown {
		s.logger.Infof("session %x state %v -> %v due to admin up", s.localDiscr, s.state.String(), StateDown.String())
		s.state = StateDown
	}
}

func (s *Session) ManipulateIntervals(desiredMinTXInterval uint32, requiredMinRXInterval uint32) error {
	if s.pollTryLock() {
		s.intervalParamChan <- SessionIntervalParams{
			desiredMinTxInterval:  desiredMinTXInterval,
			requiredMinRxInterval: requiredMinRXInterval,
		}
		return nil
	} else {
		return errors.New("polling in progress")
	}
}

func (s *Session) SetAdminDown(adminDown bool) {
	s.adminStateChan <- adminDown
}

func (s *Session) State() State {
	return s.state
}

func (s *Session) LocalDiscr() uint32 {
	return s.localDiscr
}

func (s *Session) RemoteDiscr() uint32 {
	return s.remoteDiscr
}

func (s *Session) Close() {
	s.logger.Infof("session %x is closing, current state %v", s.localDiscr, s.state.String())
	if s.txTimer != nil {
		s.txTimer.Stop()
	}
	if s.detectionTimer != nil {
		s.detectionTimer.Stop()
	}
	s.terminated = true
}
