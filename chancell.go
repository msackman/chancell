package chancell

import (
	"sync"
)

// Ultimately, this is a lot more complex than it should be because we
// have to use funcs everywhere to hide the channel and type of the
// channel due to Go's lack of generics.

const DefaultChanLength = 16

type ChanCell struct {
	sync.Mutex
	next    *ChanCell
	drained bool
	Open    func()
	Close   func()
}

func (cell *ChanCell) Next() *ChanCell {
	cell.Lock()
	next := cell.next
	if !cell.drained {
		cell.drained = true
		next.Open()
	}
	cell.Unlock()
	next.Lock()
	nextDrained := next.drained
	next.Unlock()
	if nextDrained {
		return next.Next()
	} else {
		return next
	}
}

type CurCellConsumer func(*ChanCell) (bool, CurCellConsumer)

type ChanCellTail struct {
	sync.RWMutex
	Terminated      chan struct{}
	cell            *ChanCell
	n               int
	initNewChanCell func(int, *ChanCell)
}

func NewChanCellTail(initFun func(int, *ChanCell)) (*ChanCell, *ChanCellTail) {
	head := new(ChanCell)
	tail := &ChanCellTail{
		Terminated:      make(chan struct{}),
		cell:            head,
		n:               DefaultChanLength,
		initNewChanCell: initFun,
	}
	tail.Lock()
	tail.initNewChanCell(tail.n, head)
	tail.Unlock()
	head.Open()
	return head, tail
}

func (tail *ChanCellTail) WithCell(fun CurCellConsumer) bool {
	for {
		tail.RLock()
		cell := tail.cell
		if cell == nil {
			tail.RUnlock()
			return false
		}
		success, newFun := fun(cell)
		tail.RUnlock()
		if success {
			return true
		}
		if newFun == nil {
			tail.expand(cell)
		} else {
			fun = newFun
		}
	}
}

func (tail *ChanCellTail) expand(read *ChanCell) {
	tail.Lock()
	if tail.cell == read {
		newCell := new(ChanCell)
		tail.n *= 2
		tail.initNewChanCell(tail.n, newCell)
		read.Lock()
		read.next = newCell
		read.Unlock()
		tail.cell = newCell
		read.Close()
	}
	tail.Unlock()
}

func (tail *ChanCellTail) Terminate() {
	tail.Lock()
	tail.cell = nil
	tail.Unlock()
	close(tail.Terminated)
}

func (tail *ChanCellTail) Wait() {
	<-tail.Terminated
}
