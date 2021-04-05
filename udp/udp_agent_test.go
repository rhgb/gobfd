package udp_test

import (
	"github.com/rhgb/gobfd/bfd"
	"github.com/rhgb/gobfd/udp"
	"go.uber.org/zap"
	"strconv"
	"testing"
	"time"
)

func TestNewAgent(t *testing.T) {
	testNewAgent(3784, 3785)
}

func TestNewAgent2(t *testing.T) {
	testNewAgent(3785, 3784)
}

func testNewAgent(port int, remotePort int) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	_, err := udp.NewAgent(udp.AgentConfig{
		SessionConfig: udp.SessionConfig{
			DesiredMinTxInterval:  500_000,
			RequiredMinRxInterval: 500_000,
			DetectMult:            3,
			AuthType:              bfd.AuthTypeNone,
		},
		ListenAddress: "0.0.0.0:" + strconv.Itoa(port),
		PeerAddresses: []string{"127.0.0.1:" + strconv.Itoa(remotePort)},
	}, logger.Sugar())
	if err != nil {
		logger.Sugar().Errorf("error starting agent: %v", err)
		return
	}
	for {
		time.Sleep(time.Second)
	}
}
