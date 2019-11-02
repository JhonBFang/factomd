// Copyright 2017 Factom Foundation
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package state

import (
	"fmt"
	"time"

	"github.com/FactomProject/factomd/common/constants"
	"github.com/FactomProject/factomd/common/constants/runstate"
	"github.com/FactomProject/factomd/common/interfaces"
	"github.com/FactomProject/factomd/common/messages"
	llog "github.com/FactomProject/factomd/log"
	"github.com/FactomProject/factomd/util/atomic"
	"github.com/FactomProject/factomd/worker"
)

var ValidationDebug bool = false

// This is the tread with access to state. It does process and update state
func (s *State) DoProcessing(w *worker.Thread) {
	s.validatorLoopThreadID = atomic.Goid()
	s.RunState = runstate.Running

	slp := false
	i3 := 0

	for s.GetRunState() == runstate.Running {

		select {
		case <-w.Exit:
			return
		default:
		}

		p1 := true
		p2 := true
		i1 := 0
		i2 := 0

		if ValidationDebug {
			s.LogPrintf("executeMsg", "start validate.process")
		}

		for i1 = 0; p1 && i1 < 20; i1++ {
			p1 = s.Process()
		}

		if ValidationDebug {
			s.LogPrintf("executeMsg", "start validate.updatestate")
		}
		for i2 = 0; p2 && i2 < 20; i2++ {
			p2 = s.UpdateState()
		}
		if !p1 && !p2 {
			// No work? Sleep for a bit
			time.Sleep(10 * time.Millisecond)
			s.ValidatorLoopSleepCnt++
			i3++
			slp = true
		} else if slp {
			slp = false
			if ValidationDebug {
				s.LogPrintf("executeMsg", "DoProcessing() slept %d times", i3)
				i3 = 0
			}

		}

	}
	fmt.Println("Closing the Database on", s.GetFactomNodeName())
	s.DB.Close()
	s.StateSaverStruct.StopSaving()
	fmt.Println(s.GetFactomNodeName(), "closed")
}

func (s *State) ValidatorLoop(w *worker.Thread) {

	CheckGrants()

	// We should only generate 1 EOM for each height/minute/vmindex
	lastHeight, lastMinute, lastVM := -1, -1, -1

	w.Spawn(s.DoProcessing)

	w.Run(func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("A panic state occurred in ValidatorLoop.", r)
				llog.LogPrintf("recovery", "A panic state occurred in ValidatorLoop. %v", r)
				shutdown(s)
			}
		}()

		// Look for pending messages, and get one if there is one.
		for { // this is the message sort
			var msg interfaces.IMsg

			select {
			case <-s.ShutdownChan: // Check if we should shut down.
				shutdown(s)
				time.Sleep(10 * time.Second) // wait till database close is complete
				return
			case c := <-s.tickerQueue: // Look for pending messages, and get one if there is one.
				if !s.RunLeader || !s.DBFinished { // don't generate EOM if we are not ready to execute as a leader or are loading the DBState messages
					continue
				}
				currentMinute := s.CurrentMinute
				if currentMinute == 10 { // if we are between blocks
					currentMinute = 9 // treat minute 10 as an extension of minute 9
				}
				if lastHeight == int(s.LLeaderHeight) && lastMinute == currentMinute && s.LeaderVMIndex == lastVM {
					// This eom was already generated. We shouldn't generate it again.
					// This does mean we missed an EOM boundary, and the next EOM won't occur for another
					// "minute". This could cause some serious sliding, as minutes could be an addition 100%
					// in length.
					if c == -1 { // This means we received a normal eom cadence timer
						c = 8 // Send 8 retries on a 1/10 of the normal minute period
					}
					if c > 0 {
						go func() {
							// We sleep for 1/10 of a minute, and try again
							time.Sleep(s.GetMinuteDuration() / 10)
							s.tickerQueue <- c - 1
						}()
					}
					s.LogPrintf("timer", "retry %d", c)
					s.LogPrintf("validator", "retry %d  %d-:-%d %d", c, s.LLeaderHeight, currentMinute, s.LeaderVMIndex)
					continue // Already generated this eom
				}

				lastHeight, lastMinute, lastVM = int(s.LLeaderHeight), currentMinute, s.LeaderVMIndex

				eom := new(messages.EOM)
				eom.Timestamp = s.GetTimestamp()
				eom.ChainID = s.GetIdentityChainID()
				{
					// best guess info... may be wrong -- just for debug
					eom.DBHeight = s.LLeaderHeight
					eom.VMIndex = s.LeaderVMIndex
					eom.Minute = byte(currentMinute)
				}

				eom.Sign(s)
				eom.SetLocal(true) // local EOMs are really just timeout indicators that we need to generate an EOM
				msg = eom
				s.LogMessage("validator", fmt.Sprintf("generated c:%d  %d-:-%d %d", c, s.LLeaderHeight, s.CurrentMinute, s.LeaderVMIndex), eom)
			case msg = <-s.inMsgQueue.Channel:
				s.LogMessage("InMsgQueue", "dequeue", msg)
				s.inMsgQueue.Metric(msg).Dec()
			case msg = <-s.inMsgQueue2.Channel:
				s.LogMessage("InMsgQueue2", "dequeue", msg)
				s.inMsgQueue2.Metric(msg).Dec()
			}

			if t := msg.Type(); t == constants.ACK_MSG {
				s.LogMessage("ackQueue", "enqueue ValidatorLoop", msg)
				s.ackQueue <- msg
			} else {
				s.LogMessage("msgQueue", "enqueue ValidatorLoop", msg)
				s.msgQueue <- msg
			}
		}
	})
}

func shouldShutdown(state *State) bool {
	select {
	case <-state.ShutdownChan:
		shutdown(state)
		return true
	default:
		return false
	}
}

func shutdown(state *State) {
	state.RunState = runstate.Stopping
	fmt.Println("Closing the Database on", state.GetFactomNodeName())
	state.StateSaverStruct.StopSaving()
	state.DB.Close()
	fmt.Println("Database on", state.GetFactomNodeName(), "closed")
	state.RunState = runstate.Stopped
}
