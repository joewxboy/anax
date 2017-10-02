package agreementbot

import (
	"fmt"
	"github.com/boltdb/bolt"
	"github.com/golang/glog"
	"github.com/open-horizon/anax/abstractprotocol"
	"github.com/open-horizon/anax/basicprotocol"
	"github.com/open-horizon/anax/config"
	"github.com/open-horizon/anax/events"
	"github.com/open-horizon/anax/exchange"
	"github.com/open-horizon/anax/metering"
	"github.com/open-horizon/anax/policy"
	"github.com/open-horizon/anax/worker"
	"math/rand"
	"time"
)

type BasicProtocolHandler struct {
	*BaseConsumerProtocolHandler
	agreementPH *basicprotocol.ProtocolHandler
	Work        chan AgreementWork // outgoing commands for the workers
}

func NewBasicProtocolHandler(name string, cfg *config.HorizonConfig, db *bolt.DB, pm *policy.PolicyManager, messages chan events.Message) *BasicProtocolHandler {
	if name == basicprotocol.PROTOCOL_NAME {
		return &BasicProtocolHandler{
			BaseConsumerProtocolHandler: &BaseConsumerProtocolHandler{
				name:             name,
				pm:               pm,
				db:               db,
				config:           cfg,
				httpClient:       cfg.Collaborators.HTTPClientFactory.NewHTTPClient(nil),
				agbotId:          cfg.AgreementBot.ExchangeId,
				token:            cfg.AgreementBot.ExchangeToken,
				deferredCommands: nil,
				messages:         messages,
			},
			agreementPH: basicprotocol.NewProtocolHandler(cfg.Collaborators.HTTPClientFactory.NewHTTPClient(nil), pm),
			Work:        make(chan AgreementWork),
		}
	} else {
		return nil
	}
}

func (c *BasicProtocolHandler) String() string {
	return fmt.Sprintf("Name: %v, "+
		"PM: %v, "+
		"DB: %v, "+
		"Agreement PH: %v",
		c.Name(), c.pm, c.db, c.agreementPH)
}

func (c *BasicProtocolHandler) Initialize() {

	glog.V(5).Infof(BsCPHlogString(fmt.Sprintf("initializing: %v ", c)))
	// Set up random number gen. This is used to generate agreement id strings.
	random := rand.New(rand.NewSource(int64(time.Now().Nanosecond())))

	// Setup a lock to protect concurrent agreement processing
	agreementLockMgr := NewAgreementLockManager()

	// Set up agreement worker pool based on the current technical config.
	for ix := 0; ix < c.config.AgreementBot.AgreementWorkers; ix++ {
		agw := NewBasicAgreementWorker(c, c.config, c.db, c.pm, agreementLockMgr)
		go agw.start(c.Work, random)
	}

}

func (c *BasicProtocolHandler) AgreementProtocolHandler(typeName string, name string, org string) abstractprotocol.ProtocolHandler {
	return c.agreementPH
}

func (c *BasicProtocolHandler) WorkQueue() chan AgreementWork {
	return c.Work
}

func (c *BasicProtocolHandler) AcceptCommand(cmd worker.Command) bool {

	switch cmd.(type) {
	case *NewProtocolMessageCommand:
		return true
	case *AgreementTimeoutCommand:
		return true
	case *PolicyChangedCommand:
		return true
	case *PolicyDeletedCommand:
		return true
	case *WorkloadUpgradeCommand:
		return true
	case *MakeAgreementCommand:
		return true
	}
	return false
}

func (c *BasicProtocolHandler) PersistAgreement(wi *InitiateAgreement, proposal abstractprotocol.Proposal, workerID string) error {

	return c.BaseConsumerProtocolHandler.PersistBaseAgreement(wi, proposal, workerID, "", "")
}

func (c *BasicProtocolHandler) PersistReply(r abstractprotocol.ProposalReply, pol *policy.Policy, workerID string) error {

	return c.BaseConsumerProtocolHandler.PersistReply(r, pol, workerID)
}

func (c *BasicProtocolHandler) HandleBlockchainEvent(cmd *BlockchainEventCommand) {
	return
}

func (c *BasicProtocolHandler) CreateMeteringNotification(mp policy.Meter, ag *Agreement) (*metering.MeteringNotification, error) {

	return metering.NewMeteringNotification(mp, ag.AgreementCreationTime, uint64(ag.DataVerificationCheckRate), ag.DataVerificationMissedCount, ag.CurrentAgreementId, ag.ProposalHash, ag.ConsumerProposalSig, "", ag.ProposalSig, "")
}

func (c *BasicProtocolHandler) TerminateAgreement(ag *Agreement, reason uint, workerId string) {
	var messageTarget interface{}
	if whisperTo, pubkeyTo, err := c.BaseConsumerProtocolHandler.GetDeviceMessageEndpoint(ag.DeviceId, workerId); err != nil {
		glog.Errorf(BCPHlogstring2(workerId, fmt.Sprintf("error obtaining message target for cancel message: %v", err)))
	} else if mt, err := exchange.CreateMessageTarget(ag.DeviceId, nil, pubkeyTo, whisperTo); err != nil {
		glog.Errorf(BCPHlogstring2(workerId, fmt.Sprintf("error creating message target: %v", err)))
	} else {
		messageTarget = mt
	}
	c.BaseConsumerProtocolHandler.TerminateAgreement(ag, reason, messageTarget, workerId, c)
}

func (c *BasicProtocolHandler) GetTerminationCode(reason string) uint {
	switch reason {
	case TERM_REASON_POLICY_CHANGED:
		return basicprotocol.AB_CANCEL_POLICY_CHANGED
	// case TERM_REASON_NOT_FINALIZED_TIMEOUT:
	//     return basicprotocol.AB_CANCEL_NOT_FINALIZED_TIMEOUT
	case TERM_REASON_NO_DATA_RECEIVED:
		return basicprotocol.AB_CANCEL_NO_DATA_RECEIVED
	case TERM_REASON_NO_REPLY:
		return basicprotocol.AB_CANCEL_NO_REPLY
	case TERM_REASON_USER_REQUESTED:
		return basicprotocol.AB_USER_REQUESTED
	case TERM_REASON_DEVICE_REQUESTED:
		return basicprotocol.CANCEL_USER_REQUESTED
	case TERM_REASON_NEGATIVE_REPLY:
		return basicprotocol.AB_CANCEL_NEGATIVE_REPLY
	case TERM_REASON_CANCEL_DISCOVERED:
		return basicprotocol.AB_CANCEL_DISCOVERED
	case TERM_REASON_CANCEL_FORCED_UPGRADE:
		return basicprotocol.AB_CANCEL_FORCED_UPGRADE
	// case TERM_REASON_CANCEL_BC_WRITE_FAILED:
	//     return basicprotocol.AB_CANCEL_BC_WRITE_FAILED
	default:
		return 999
	}
}

func (c *BasicProtocolHandler) GetTerminationReason(code uint) string {
	return basicprotocol.DecodeReasonCode(uint64(code))
}

func (c *BasicProtocolHandler) SetBlockchainWritable(ev *events.AccountFundedMessage) {
	return
}

func (c *BasicProtocolHandler) IsBlockchainWritable(typeName string, name string, org string) bool {
	return true
}

func (c *BasicProtocolHandler) CanCancelNow(ag *Agreement) bool {
	return true
}

func (c *BasicProtocolHandler) HandleDeferredCommands() {
	return
}

func (b *BasicProtocolHandler) PostReply(agreementId string, proposal abstractprotocol.Proposal, reply abstractprotocol.ProposalReply, consumerPolicy *policy.Policy, org string, workerId string) error {

	if err := b.agreementPH.RecordAgreement(proposal, reply, "", "", consumerPolicy, org); err != nil {
		return err
	} else {
		glog.V(3).Infof(BCPHlogstring2(workerId, fmt.Sprintf("recorded agreement %v", agreementId)))
	}

	return nil

}

// ==========================================================================================================
// Utility functions

var BsCPHlogString = func(v interface{}) string {
	return fmt.Sprintf("AgreementBot Basic Protocol Handler %v", v)
}