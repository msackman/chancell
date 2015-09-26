# chancell
Infinite channels for Go

Bounded channels are a bad idea. If two actors want to send messages
to each other and both have channels backed up and full then the send
operation is now blocking and you now have a source of deadlock. If
you have bounded channels, you can *never* be sure that your channels
are long enough: someone using your software in some unexpected
scenario induces a load that you've not tested and suddenly one
channel gets full and you end up in a deadlock. Bounded channels are a
bad idea.

But back-pressure is a good idea. Networks work this way. If you send
to a channel which is very long, you as the sender should be punished:
this hopefully gives more CPU time to the consumer, allowing the
consumer to catch up making the producer sleep for a while and yield
CPU is a good idea. However note that this is yet to be implemented in
this package (though it's not hard to implement, though tuning may be
a challenge).

This package implements unbounded channels on top of Go's
channels. Whenever a channel is full, the producer will safely append
a new channel which is twice the length of the previous and send into
that.

Because Go has no generics, I'm forced to use funcs and thunks
everywhere to hide the type of the chans. This package takes care of
all the locking, but there is boilerplate that you'll have to use in
your actors; this is unavoidable. The implementation is safe for actor
designs where you have multiple actor-workers reading off the same
work-queue.

The following is a commented example showing how I write actors in
Go. Please consider this example to be under the same MIT license as
the rest of this package.

```Go*/
package myactor

import (
	"fmt"
	cc "github.com/msackman/chancell"
	"log"
	"time"
)

type MyActor struct {
	cellTail        *cc.ChanCellTail
	cellHead        *cc.ChanCellHead
	msgChan         <-chan myactorMsg
	enqueueMsgInner func(myactorMsg, *cc.ChanCell, cc.CurCellConsumer) (bool, cc.CurCellConsumer)
}

type myactorMsg interface {
	myactorMsgWitness() // type witness for sum-type
}

type myactorMsgShutdown struct{}

func (self *myactorMsgShutdown) myactorMsgWitness() {}

var myactorMsgShutdownInstance = &myactorMsgShutdown{}

// This is synchronous: i.e. does not return until we know the actor
// really has gone.
func (self *MyActor) Shutdown() {
	self.enqueueMsg(myactorMsgShutdownInstance)
	self.cellTail.Wait()
}

type myactorMsgSpeak string

func (self myactorMsgSpeak) myactorMsgWitness() {}

// This is asynchronous: never blocks
func (self *MyActor) Speak(str string) {
	self.enqueueMsg(myactorMsgSpeak(str))
}

// Sometimes you want a result from the actor. This is how I do it:
type myactorMsgPingPong chan struct{}

func (self myactorMsgPingPong) myactorMsgWitness() {}

func (self *MyActor) PingPong() (time.Duration, error) {
	signalChan := make(chan struct{})
	msg := myactorMsgPingPong(signalChan)
	start := time.Now()
	if self.enqueueSyncMsg(msg, signalChan) {
		return time.Now().Sub(start), nil
	} else {
		return time.Duration(0), fmt.Errorf("MyActor terminated")
	}
}

// Returns true iff the enqueue was successful. Never blocks.
func (self *MyActor) enqueueMsg(msg myactorMsg) bool {
	var f cc.CurCellConsumer
	f = func(cell *cc.ChanCell) (bool, cc.CurCellConsumer) {
		return self.enqueueMsgInner(msg, cell, f)
	}
	return self.cellTail.WithCell(f)
}

// Returns true iff the enqueue was successful and a result is
// available. If the actor terminates, either before the enqueue
// attempt, or whilst waiting for a result, will return false.
func (self *MyActor) enqueueSyncMsg(msg myactorMsg, signalChan chan struct{}) bool {
	if self.enqueueMsg(msg) {
		select {
		case <-signalChan:
			return true
		case <-self.cellTail.Terminated:
			return false
		}
	} else {
		return false
	}
}

func NewMyActor() *MyActor {
	actor := &MyActor{}
	// The following is boilerplate. You don't need to figure it out!
	actor.cellHead, actor.cellTail = cc.NewChanCellTail(
		func(n int, cell *cc.ChanCell) {
			// Create new chan of the desired type and length
			msgChan := make(chan myactorMsg, n)
			// When this cell is opened for reading, stash the chan in
			// the actor struct
			cell.Open = func() { actor.msgChan = msgChan }
			// What to do when the cell needs closing for sending (it's
			// full and we need to add a new cell)
			cell.Close = func() { close(msgChan) }
			// Boilerplate to do the actual enqueuing of the message and
			// detect when the current chan is full.
			actor.enqueueMsgInner = func(msg myactorMsg, curCell *cc.ChanCell, cont cc.CurCellConsumer) (bool, cc.CurCellConsumer) {
				if curCell == cell {
					select {
					case msgChan <- msg:
						return true, nil
					default:
						return false, nil
					}
				} else {
					return false, cont
				}
			}
		})
	// Start up our new actor!
	go actor.actorLoop()
	return actor
}

func (self *MyActor) actorLoop() {
	var (
		err     error
		msgChan <-chan myactorMsg
		msgCell *cc.ChanCell
	)
	nextChanFun := func(cell *cc.ChanCell) { msgChan, msgCell = self.msgChan, cell }
	// Grab the current cell and chan
	self.cellHead.WithCell(nextChanFun)
	terminate := false
	for !terminate {
		if msg, ok := <-msgChan; ok {
			switch msgT := msg.(type) {
			case *myactorMsgShutdown:
				terminate = true // Controlled shutdown, no error
			case myactorMsgSpeak:
				fmt.Printf("And the Actor spaketh: \"%s\"\n", msgT)
			case myactorMsgPingPong:
				close(msgT)
			default:
				err = fmt.Errorf("Received unexpected message: %v", msgT)
			}
			terminate = terminate || err != nil
		} else {
			// The current cell has been closed and we've drained
			// it. Need to advance to the next cell and chan.
			self.cellHead.Next(msgCell, nextChanFun)
		}
	}
	if err != nil {
		log.Println("MyActor Error:", err)
	}
	self.cellTail.Terminate()
}

```
